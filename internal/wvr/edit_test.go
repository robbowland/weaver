package wvr

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
)

// Where Alt v says the agent is (its nvim's reviews follow it): its
// state's root, else its directory, else its thread's worktree, and the
// file it was at under it; never a guess (weaver's own directory) when none
// of those is there.
func TestAgentWhere(t *testing.T) {
	td, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &Env{&session.Env{StateDir: td + "/state", SKey: "tmux-s"}}
	wt := td + "/wt"
	if err := os.MkdirAll(wt+"/src", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wt+"/src/a.go", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(e.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name       string
		state      *agent.Record
		thread     *store.Thread
		root, file string
		known      bool
	}{
		{"its state's root and file", &agent.Record{Root: wt, Cwd: "/", File: wt + "/src/a.go"}, nil, wt, wt + "/src/a.go", true},
		{"its directory, while it has no root", &agent.Record{Cwd: wt}, nil, wt, "", true},
		{"a file outside its root, dropped", &agent.Record{Root: wt + "/src", File: td + "/elsewhere.go"}, nil, wt + "/src", "", true},
		{"its thread's worktree, before its first hook", nil, &store.Thread{Agent: agent.Codex, Worktree: wt}, wt, "", true},
		{"a root that has gone", &agent.Record{Root: td + "/gone"}, nil, "", "", false},
		{"nothing known", nil, nil, "", "", false},
	} {
		os.Remove(e.StateFile("5"))
		os.Remove(e.ThreadFile("5"))
		if c.state != nil {
			if err := store.Write(e.StateFile("5"), c.state); err != nil {
				t.Fatal(err)
			}
		}
		if c.thread != nil {
			if err := store.Write(e.ThreadFile("5"), c.thread); err != nil {
				t.Fatal(err)
			}
		}
		_, root, file, known := e.agentWhere("5")
		if root != c.root || file != c.file || known != c.known {
			t.Errorf("%s: %q %q %v, want %q %q %v", c.name, root, file, known, c.root, c.file, c.known)
		}
	}
}

// Rex's init.lua loads weaver's keys however it requires them, but not
// from a comment.
func TestLoadsWeaver(t *testing.T) {
	for init, want := range map[string]bool{
		`require("weaver")`:                  true,
		`require "weaver"`:                   true,
		`pcall(require, "weaver")`:           true,
		`local ok = pcall(require,'weaver')`: true,
		`-- pcall(require, "weaver")`:        false,
		`require("weaver_extra")`:            false,
		`rex.bind("cmd+d", "pane.split")`:    false,
	} {
		if got := loadsWeaver("x = 1\n" + init + "\n"); got != want {
			t.Errorf("loadsWeaver(%q) = %v, want %v", init, got, want)
		}
	}
}
