package wvr

import (
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
