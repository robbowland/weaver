package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/session"
)

func TestMain(m *testing.M) {
	// This test binary, run as codex (TestCodexPaneNotOnRex): a process of
	// that name that waits.
	if os.Getenv("WVR_TEST_AS_CODEX") == "1" {
		time.Sleep(10 * time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// A Codex hook run in a Rex session's environment is placed in no pane:
// not in the one tmux pane running codex in its directory, where a hook
// from tmux's side of things goes.
func TestCodexPaneNotOnRex(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux")
	}
	dir := t.TempDir()
	work := filepath.Join(dir, "work")
	codex := filepath.Join(dir, "bin", "codex")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(codex), 0o755); err != nil {
		t.Fatal(err)
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

	e := &session.Env{StateDir: dir + "/state", TSock: []string{"-S", sock}}
	if at, ok := codexPane(e, "", "on-tmux", work); !ok || string(at.pane.id) != pane || at.inPane {
		t.Fatalf("from tmux's side: placed %+v %v, want pane %s", at, ok, pane)
	}
	e.RexID, e.Session, e.SKey = "session:x", "x", "rex-x"
	if at, ok := codexPane(e, "", "on-rex", work); ok {
		t.Errorf("from a Rex session: placed in tmux pane %s", at.pane.id)
	}
	if files.Exists(filepath.Join(e.StateDir, "codex", "on-rex")) {
		t.Error("from a Rex session: bound to a tmux pane")
	}
}
