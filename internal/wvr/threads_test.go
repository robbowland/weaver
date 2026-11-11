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
		os.Chtimes(f, at, at)
	}
	os.WriteFile(filepath.Join(e.StateDir, e.SKey+"_3.thread"), []byte(`{"num":1}`), 0o644)
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
