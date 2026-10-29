package wvr

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/rex/rextest"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
)

// On Rex a Codex thread would never report (its hooks' events are placed
// among tmux's panes only): starting one, bringing a lost one back and
// adopting one are refused, saying so. Claude Code's are not, nor Codex's
// on tmux.
func TestCodexRefusedOnRex(t *testing.T) {
	dir := t.TempDir()
	// Whatever runs past the refusal finds no Rex.
	rextest.NoRex(t)
	e := &Env{&session.Env{StateDir: dir + "/state", LostDir: dir + "/lost", SockDir: dir + "/sock",
		Session: "w", SKey: "rex-w", RexID: "session:w", TSock: []string{"-S", dir + "/no-tmux.sock"}}}
	if err := e.followable(agent.Claude); err != nil {
		t.Errorf("Claude Code on Rex: %v", err)
	}
	if err := (&Env{&session.Env{SKey: "tmux-w"}}).followable(agent.Codex); err != nil {
		t.Errorf("Codex on tmux: %v", err)
	}

	if err := e.threadCreate("a task", form{Here: dir, Where: WhereHere, Agent: agent.Codex}, true); !errors.Is(err, errCodexOnRex) {
		t.Errorf("new: %v, want it refused", err)
	}
	lo := store.Lost{SKey: "rex-w", State: agent.Record{Agent: "codex", SessionID: "c0dex"},
		Thread: store.Thread{Num: 1, Name: "demo", Agent: agent.Codex, Worktree: dir}}
	if _, err := e.threadReopen("1", lo, true); !errors.Is(err, errCodexOnRex) {
		t.Errorf("resume: %v, want it refused", err)
	}
	if err := store.Write(e.StateFile("7"), agent.Record{Agent: "codex", SessionID: "c0dex", State: agent.StateIdle, Cwd: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.adoptPane("7"); !errors.Is(err, errCodexOnRex) {
		t.Errorf("adopt: %v, want it refused", err)
	}
	if files.Exists(e.ThreadFile("7")) {
		t.Error("adopt: it was made a thread")
	}
	if err := store.Write(e.ThreadFile("8"), store.Thread{Num: 2, Agent: agent.Codex, Worktree: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.restartAgent("8", true); !errors.Is(err, errCodexOnRex) {
		t.Errorf("restart: %v, want it refused", err)
	}
}

// A pane is a number of any length: one of four digits or more (Rex's
// numbers only grow, and a long-lived tmux server's do) when weaver follows
// an agent there; else, as the start of a session id could be, no pane.
func TestPaneArg(t *testing.T) {
	dir := t.TempDir()
	e := &Env{&session.Env{StateDir: dir, SKey: "rex-w"}}
	for _, f := range []string{"rex-w_1234.json", "rex-w_20250.thread", "rex-w_31337.nvim"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		arg  string
		want mux.PaneID
		ok   bool
	}{
		{"1234", "1234", true},
		{"20250", "20250", true},
		{"31337", "31337", true},
		{"%4567", "4567", true},
		{"12", "12", true},
		{"999", "999", true},
		{"4567", "", false},     // no agent there: a session id's start
		{"1234abcd", "", false}, // a session id's start
		{"", "", false},
	} {
		p, ok := e.paneArg(c.arg)
		if ok != c.ok || ok && p != c.want {
			t.Errorf("paneArg(%q) = %q, %v; want %q, %v", c.arg, p, ok, c.want, c.ok)
		}
	}
}
