package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/tmux"
)

func TestMain(m *testing.M) {
	// This test binary, run as codex (TestCodexPanePlacement): a process of
	// that name that waits.
	if os.Getenv("WVR_TEST_AS_CODEX") == "1" {
		time.Sleep(10 * time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// A Codex hook is placed by tmux's panes, whatever started the daemon it
// runs in: from a daemon a Rex block started, a tmux Codex thread still
// reports. A Codex running in a Rex block in a directory leaves that
// directory naming no tmux pane (the event may be its session's); one in
// another directory, or a binding made before, does not.
func TestCodexPanePlacement(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux")
	}
	dir := t.TempDir()
	work, other := filepath.Join(dir, "work"), filepath.Join(dir, "other")
	codex := filepath.Join(dir, "bin", "codex")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{work, other, filepath.Dir(codex)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(codex, b, 0o755); err != nil {
		t.Fatal(err)
	}
	sock := dir + "/tmux.sock"
	tm := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("tmux", append([]string{"-S", sock, "-f", "/dev/null"}, args...)...).Output()
		if err != nil {
			t.Fatalf("tmux %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	tm("new-session", "-d", "-s", "s", "-c", work, "-e", "WVR_TEST_AS_CODEX=1", codex)
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", sock, "kill-server").Run() })
	pane := ""
	for i := 0; ; i++ {
		if f := strings.Fields(tm("list-panes", "-a", "-F", "#{pane_id} #{pane_current_command}")); len(f) == 2 && f[1] == "codex" {
			pane = strings.TrimPrefix(f[0], "%")
			break
		}
		if i == 50 {
			t.Fatal("the pane never ran codex")
		}
		time.Sleep(100 * time.Millisecond)
	}
	// A Codex run by hand in a Rex block, in dir.
	inRex := func(dir string) {
		t.Helper()
		c := exec.Command(codex)
		c.Dir = dir
		c.Env = []string{"WVR_TEST_AS_CODEX=1", "PWD=" + dir, "REX_SESSION=session:t", "REX_BLOCK=block:t"}
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Process.Kill(); _ = c.Wait() })
	}
	placed := func(e *session.Env, sid string) bool {
		t.Helper()
		at, ok := codexPane(e, "", sid, work)
		if ok && (string(at.pane.id) != pane || at.inPane) {
			t.Fatalf("%s: placed %+v, want pane %s", sid, at, pane)
		}
		return ok
	}

	// The daemon's environment a Rex block's.
	e := &session.Env{StateDir: dir + "/state", TSock: []string{"-S", sock}, RexID: "session:t", Session: "t", SKey: "rex-t"}
	if !placed(e, "s-1") {
		t.Error("a tmux Codex, from a daemon a Rex block started: not placed")
	}
	inRex(other)
	if !placed(e, "s-2") {
		t.Error("a tmux Codex, beside a Rex block's Codex in another directory: not placed")
	}
	inRex(work)
	if placed(e, "s-3") {
		t.Error("a Rex block's Codex in its directory: placed in the tmux pane")
	}
	if files.Exists(filepath.Join(e.StateDir, "codex", "s-3")) {
		t.Error("a Rex block's Codex in its directory: bound to the tmux pane")
	}
	if !placed(e, "s-1") {
		t.Error("a session bound before: not placed")
	}
}

// An event placed in a tmux pane acts on tmux, whatever the daemon's
// environment: in the pane's session and life when it runs in the
// daemon. Its background run acts on that session too, not on its
// environment's.
func TestCodexEventActsOnTmux(t *testing.T) {
	rexEnv := func() *session.Env {
		return &session.Env{RexID: "session:t", Session: "t", SKey: "rex-t", Where: agent.Where{Server: 7, Session: "session:t"}}
	}
	w := agent.Where{Server: 42, Session: "$3"}
	pe := codexPlace{pane: paneInfo{id: "5", session: "work", where: w}}.env(rexEnv())
	if pe.RexID != "" || pe.Session != "work" || pe.SKey != "tmux-work" || pe.Where != w {
		t.Errorf("placed from the daemon: %+v", pe)
	}
	if _, ok := pe.Mux().(tmux.Session); !ok {
		t.Errorf("placed from the daemon: acts on %T", pe.Mux())
	}
	if pe := (codexPlace{pane: paneInfo{id: "5"}, inPane: true}).env(rexEnv()); pe.RexID != "" {
		t.Errorf("in its pane: %+v", pe)
	}

	bg := rexEnv()
	(&hk{Session: "work", SKey: "tmux-work"}).actOn(bg)
	if bg.RexID != "" || bg.Session != "work" || bg.SKey != "tmux-work" {
		t.Errorf("background run: %+v", bg)
	}
	old := rexEnv()
	(&hk{SKey: "rex-t"}).actOn(old)
	if old.RexID != "session:t" || old.Session != "t" {
		t.Errorf("background run of an older weaver: %+v", old)
	}
}

// A Codex works where it started, or in its --cd.
func TestCodexDir(t *testing.T) {
	for _, c := range []struct {
		argv []string
		want string
	}{
		{[]string{"codex"}, "/w"},
		{[]string{"codex", "resume", "abc"}, "/w"},
		{[]string{"codex", "-C", "sub"}, "/w/sub"},
		{[]string{"codex", "--cd", "/x"}, "/x"},
		{[]string{"codex", "--cd=/y"}, "/y"},
		{[]string{"codex", "-C/z"}, "/z"},
	} {
		if got := codexDir(c.argv, "/w"); got != c.want {
			t.Errorf("codexDir(%q) = %q, want %q", c.argv, got, c.want)
		}
	}
}
