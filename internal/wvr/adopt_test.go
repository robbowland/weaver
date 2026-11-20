package wvr

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
)

// A pane is a number of any length: one of four digits or more (Rex's
// numbers only grow) when weaver follows
// an agent there; else, as the start of a session id could be, no pane.
func TestPaneArg(t *testing.T) {
	dir := t.TempDir()
	e := &Env{&session.Env{StateDir: dir, SKey: "rex-w"}}
	for _, f := range []string{"rex-w_1234.json", "rex-w_20250.thread", "rex-w_31337.panel"} {
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

// `wvr rename [PANE] [NAME]` and `wvr hold [PANE] [NOTE]` take a pane
// first, when one is given: a number, or Rex's id for its block
// ($REX_BLOCK, the one an agent has). A block id weaver gave no number is
// refused, never made the first word of a name.
func TestLeadingPane(t *testing.T) {
	dir := t.TempDir()
	e := &Env{&session.Env{StateDir: dir, SKey: "rex-w"}}
	if err := os.MkdirAll(e.RexDir()+"/u", 0o755); err != nil {
		t.Fatal(err)
	}
	for f, s := range map[string]string{dir + "/rex-w_1234.json": "{}\n", e.RexDir() + "/u/block_01aa": "1234\n"} {
		if err := os.WriteFile(f, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name, pane, words, err string
		args                   []string
	}{
		{name: "a name only", args: []string{"Greeter", "demo"}, words: "Greeter demo"},
		{name: "a pane and a name", args: []string{"1234", "Greeter demo"}, pane: "1234", words: "Greeter demo"},
		{name: "a block id and a name", args: []string{"block:01aa", "Greeter demo"}, pane: "1234", words: "Greeter demo"},
		{name: "a block id weaver never numbered", args: []string{"block:02bb", "Greeter demo"}, err: "no pane block:02bb"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, words, err := e.leadingPane(c.args)
			if got := fmt.Sprint(err); (c.err != "" && got != c.err) || (c.err == "" && err != nil) {
				t.Fatalf("error %v, want %q", err, c.err)
			}
			if string(p) != c.pane || strings.Join(words, " ") != c.words {
				t.Errorf("pane %q, words %q; want %q, %q", p, strings.Join(words, " "), c.pane, c.words)
			}
		})
	}
}

// Every pane argument a command takes as a pane alone (close's, review's,
// new's --from) is read as one: a number, or Rex's id for a block weaver
// numbered; anything else is no pane, which the command refuses rather
// than act on another (close's focused agent, new's default repository).
func TestPaneArgsOfCommands(t *testing.T) {
	dir := t.TempDir()
	e := &Env{&session.Env{StateDir: dir, SKey: "rex-w"}}
	if err := os.MkdirAll(e.RexDir()+"/u", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.RexDir()+"/u/block_01aa", []byte("1234\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		arg, pane string
		ok        bool
	}{{"12", "12", true}, {"1234", "1234", true}, {"block:01aa", "1234", true}, {"block:02bb", "", false}, {"demo", "", false}} {
		if p, ok := e.parsePane(c.arg); ok != c.ok || ok && string(p) != c.pane {
			t.Errorf("parsePane(%q) = %q, %v; want %q, %v", c.arg, p, ok, c.pane, c.ok)
		}
	}
	for _, run := range []struct {
		name string
		cmd  func([]string) error
		args []string
	}{
		{"close", e.Close, []string{"block:02bb"}},
		{"review", e.Review, []string{"block:02bb"}},
		{"new --from", e.New, []string{"--from", "block:02bb", "say hello"}},
	} {
		if err := run.cmd(run.args); err == nil || !strings.Contains(err.Error(), "no pane block:02bb") {
			t.Errorf("%s %q: %v, want it refused: no pane block:02bb", run.name, run.args, err)
		}
	}
}

// An agent is adopted only between turns and with nothing running in the
// background: its pane closes, and what runs in it (a background command,
// a subagent) would end with it.
func TestAdoptRefusesWorkUnderWay(t *testing.T) {
	e := &Env{&session.Env{StateDir: t.TempDir(), SKey: "rex-w"}}
	for name, st := range map[string]agent.Record{
		"working":            {State: agent.StateWorking},
		"needing you":        {State: agent.StateNeedsYou},
		"a background task":  {State: agent.StateDone, Background: 1},
		"a subagent running": {State: agent.StateDone, Subagents: []agent.Subagent{{ID: "a"}}},
	} {
		st.SessionID = "aaaaaaaa-0000-4000-8000-000000000001"
		if err := store.Write(e.StateFile("5"), st); err != nil {
			t.Fatal(err)
		}
		if _, err := e.adoptPane("5"); err == nil || !strings.Contains(err.Error(), "under way") {
			t.Errorf("%s: adopted (%v)", name, err)
		}
	}
}
