package wvr

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/tmux"
)

// A thread closed before its nvim is recorded as bound: its nvim is the
// pane in the agent's window started for that agent (or, as before, for
// the agent's token).
func TestThreadNvimByToken(t *testing.T) {
	panes := []tmux.Pane{
		{ID: "4", Tab: "2", Cmd: "/bin/weaver nvim --thread 123"},
		{ID: "5", Tab: "3", Cmd: "/bin/weaver nvim --thread 456"},
		{ID: "6", Tab: "3", Cmd: "/bin/weaver _agent 456 claude"},
		{ID: "7", Tab: "3", Cmd: "fish"},
		{ID: "10", Tab: "4", Cmd: "/bin/weaver nvim --fresh 11"},
		{ID: "11", Tab: "4", Cmd: "/bin/weaver _agent 789 claude"},
	}
	if nv, tok := threadNvim(panes, panes[2]); nv != "5" || tok != "456" {
		t.Errorf("got %q %q, want 5 456", nv, tok)
	}
	if nv, tok := threadNvim(panes, panes[5]); nv != "10" || tok != "789" {
		t.Errorf("--fresh: got %q %q, want 10 789", nv, tok)
	}
	// Another window's nvim is not its own, nor is any pane of an agent
	// weaver did not start.
	if nv, _ := threadNvim(panes, tmux.Pane{ID: "8", Tab: "3", Cmd: "/bin/weaver _agent 123 claude"}); nv != "" {
		t.Errorf("another window's nvim: got %q", nv)
	}
	if nv, _ := threadNvim(panes, tmux.Pane{ID: "9", Tab: "3", Cmd: "claude"}); nv != "" {
		t.Errorf("not a thread's agent: got %q", nv)
	}
}

// A thread still being opened holds its number; one left behind (older
// than its nvim ever waits) does not.
func TestThreadNumSkipsLeftovers(t *testing.T) {
	e := &Env{&session.Env{StateDir: t.TempDir(), SKey: "tmux-s"}}
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
	if p := session.PaneOfFile("/s/tmux-w_1_42.thread"); p != "42" || p.Target() != "%42" {
		t.Errorf("paneOfFile: %q", p)
	}
}
