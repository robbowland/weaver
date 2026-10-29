package wvr

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/session"
)

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
