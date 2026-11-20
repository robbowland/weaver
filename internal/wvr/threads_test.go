package wvr

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/rex"
	"github.com/robbowland/weaver/internal/rex/rextest"
	"github.com/robbowland/weaver/internal/session"
)

// A thread closed before its review panel is recorded as bound: its panel
// is the pane in the agent's window started for that agent.
func TestThreadPanelByToken(t *testing.T) {
	panes := []mux.Pane{
		{ID: "4", Window: "2", Cmd: "/bin/weaver _panel 6"},
		{ID: "5", Window: "3", Cmd: "/bin/weaver _panel 6"},
		{ID: "6", Window: "3", Cmd: "/bin/weaver _agent 456 claude"},
		{ID: "7", Window: "3", Cmd: "fish"},
	}
	if rp, tok := threadPanel(panes, panes[2]); rp != "5" || tok != "456" {
		t.Errorf("got %q %q, want 5 456", rp, tok)
	}
	// Another agent's panel is not its own, nor is any pane of an agent
	// weaver did not start.
	if rp, _ := threadPanel(panes, mux.Pane{ID: "8", Window: "3", Cmd: "/bin/weaver _agent 123 claude"}); rp != "" {
		t.Errorf("another agent's panel: got %q", rp)
	}
	if rp, _ := threadPanel(panes, mux.Pane{ID: "9", Window: "3", Cmd: "claude"}); rp != "" {
		t.Errorf("not a thread's agent: got %q", rp)
	}
}

// A thread still being opened holds its number; one left behind (older
// than its agent ever waits) does not.
func TestThreadNumSkipsLeftovers(t *testing.T) {
	e := &Env{&session.Env{StateDir: t.TempDir(), SKey: "rex-s"}}
	put := func(tok, rec string, age time.Duration) {
		d := filepath.Join(e.threadsNew(), e.SKey+"_"+tok)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(d, "thread.json")
		if err := os.WriteFile(f, []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		if err := os.Chtimes(f, at, at); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(e.StateDir, e.SKey+"_3.thread"), []byte(`{"num":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	put("1", `{"num":2}`, time.Second)
	put("2", `{"num":3}`, time.Minute)
	n, used := e.threadNum()
	if n != 3 || !used[1] || !used[2] || used[3] {
		t.Errorf("got %d %v, want 3 with 1 and 2 taken", n, used)
	}
}

// Weaver's per-agent files are named by their pane.
func TestPaneOfFile(t *testing.T) {
	if p := session.PaneOfFile("/s/rex-w_1_42.thread"); p != "42" {
		t.Errorf("paneOfFile: %q", p)
	}
}

// A thread's number is the lowest its session's threads (opening ones too)
// leave free: session "a" does not count session "a_b"'s.
func TestThreadNum(t *testing.T) {
	dir := t.TempDir()
	e := &Env{&session.Env{StateDir: dir, SKey: "rex-a"}}
	for f, n := range map[string]string{
		"rex-a_1.thread":              `{"num":1}`,
		"rex-a_b_7.thread":            `{"num":2}`,
		"new/rex-a_123/thread.json":   `{"num":3}`,
		"new/rex-a_b_456/thread.json": `{"num":4}`,
	} {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	n, used := e.threadNum()
	if n != 2 || !used[1] || !used[3] || used[2] || used[4] {
		t.Errorf("threadNum = %d %v, want 2 with 1 and 3 used", n, used)
	}
}

// A thread's review panel bound with a pid Rex did not say (0) is recorded
// by its pane alone, and never over the record that the panel made of
// itself as it started (it may start first); a known pid is recorded.
func TestBindPanelUnknownPid(t *testing.T) {
	e := &Env{&session.Env{StateDir: t.TempDir(), SKey: "rex-w"}}
	nf := e.PaneFile("5", "panel")
	read := func() string {
		t.Helper()
		b, err := os.ReadFile(nf)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	e.bindPanel("5", "6", 0)
	if got := read(); got != "6\n\n" {
		t.Errorf("unknown pid, no record: %q, want the pane alone", got)
	}
	own := "6\n" + strconv.Itoa(os.Getpid()) + "\n"
	if err := os.WriteFile(nf, []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	e.bindPanel("5", "6", 0)
	if got := read(); got != own {
		t.Errorf("unknown pid over the panel's own record: %q, want %q", got, own)
	}
	if err := os.WriteFile(nf, []byte("3\n4242\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.bindPanel("5", "6", 1)
	if got := read(); got != "6\n\n" {
		t.Errorf("unknown pid over another pane's record: %q, want the pane alone", got)
	}
	e.bindPanel("5", "6", 4321)
	if got := read(); got != "6\n4321\n" {
		t.Errorf("known pid: %q", got)
	}
}

// A thread's base is recorded as a branch's name, never a SHA: a
// remote-tracking branch as it is, a local one as the remote's when the
// remote has it, @ as the branch checked out; else none.
func TestBaseBranch(t *testing.T) {
	d := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", d, "-c", "user.email=t@e", "-c", "user.name=t"}, args...)...).Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "base")
	git("remote", "add", "origin", "https://example.com/o/r.git")
	git("update-ref", "refs/remotes/origin/main", "HEAD")
	git("branch", "-q", "local-only")
	sha := git("rev-parse", "HEAD")
	for base, want := range map[string]string{
		"origin/main": "origin/main", "main": "origin/main", "local-only": "local-only",
		"@": "origin/main", sha: "", "nope": "", "": "",
	} {
		if got := baseBranch(d, base); got != want {
			t.Errorf("baseBranch(%q) = %q, want %q", base, got, want)
		}
	}
}

// A thread's first prompt asks its agent to name it (when you did not),
// and to rename its branch, the one weaver named from the task.
func TestNameNotes(t *testing.T) {
	if n := titleNote(); !strings.HasPrefix(n, "weaver ") || !strings.Contains(n, "`wvr title <name>`") {
		t.Errorf("titleNote: %q", n)
	}
	if n := branchNote("add-a-flag"); !strings.HasPrefix(n, "weaver ") || !strings.Contains(n, "branch add-a-flag from") ||
		!strings.Contains(n, "`wvr branch <name>`") {
		t.Errorf("branchNote: %q", n)
	}
}

// A process a command outside weaver's session starts (wvr _behind, which
// the form's alt+⏎ starts; wvr _width) acts on the session that command
// chose: its environment names that session, as a pane of it would, and
// no pane of the one it was started from.
func TestChildrenOutsideTheSession(t *testing.T) {
	rextest.Server(t, rex.Bin())
	// A shell in another Rex session, weaver's demo running.
	t.Setenv("REX_SESSION", "")
	_ = os.Unsetenv("REX_SESSION")
	t.Setenv("REX_BLOCK", "block:elsewhere")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("WEAVER_SESSION", "demo")
	e := Load()
	home, err := rex.Start(e.RexDir(), "demo", "/", []string{"/bin/sleep", "600"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = home.Quit() })
	// wvr as a stand-in: what a wvr started there would load (Load, as
	// main does before its session check).
	d := t.TempDir()
	t.Setenv("WVR_LOAD_TO", d+"/loaded")
	e.Self = d + "/wvr"
	if err := os.WriteFile(e.Self, []byte("#!/bin/sh\nexec "+session.Quote(os.Args[0])+" -test.run='^TestLoadAsAChild$'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := e.toWeaverSession(); err != nil {
		t.Fatal(err)
	}
	if err := e.startBehind("say hello", form{}); err != nil {
		t.Fatal(err)
	}
	var got []byte
	for i := 0; i < 100 && len(got) == 0; i++ {
		time.Sleep(50 * time.Millisecond)
		got, _ = os.ReadFile(d + "/loaded")
	}
	if want := "demo " + home.ID + " \n"; string(got) != want {
		t.Errorf("wvr _behind, started outside, loads %q; want %q (demo, no pane)", got, want)
	}
}

// TestLoadAsAChild is no test of its own: TestChildrenOutsideTheSession's
// stand-in for wvr, which writes the session it loads to $WVR_LOAD_TO.
func TestLoadAsAChild(t *testing.T) {
	out := os.Getenv("WVR_LOAD_TO")
	if out == "" {
		t.Skip("TestChildrenOutsideTheSession runs it")
	}
	e := Load()
	if err := os.WriteFile(out, []byte(e.Session+" "+e.RexID+" "+string(e.MyPane)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
