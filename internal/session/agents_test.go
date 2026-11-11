package session

import (
	"os"
	"os/exec"
	"strconv"
	"testing"

	"github.com/robbowland/weaver/internal/mux"
)

// deadPid is the pid of a process that has exited.
func deadPid(t *testing.T) int {
	t.Helper()
	c := exec.Command("/usr/bin/true")
	if err := c.Run(); err != nil {
		t.Fatal(err)
	}
	return c.Process.Pid
}

// A panel recorded with a pid of 0 (one the multiplexer did not say) has
// not been seen to run: never its agent's panel. One recorded without a pid
// is its pane until it records itself.
func TestPanelPaneUnknownPid(t *testing.T) {
	dir := t.TempDir()
	e := &Env{StateDir: dir, SKey: "rex-w"}
	for rec, want := range map[string]mux.PaneID{
		"6\n0\n":                                 "",
		"6\n1\n":                                 "",
		"6\n" + strconv.Itoa(deadPid(t)) + "\n":  "",
		"6\n" + strconv.Itoa(os.Getpid()) + "\n": "6",
		"6\n\n":                                  "6",
	} {
		if err := os.WriteFile(e.PaneFile("5", "panel"), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := e.PanelPane("5"); got != want {
			t.Errorf("record %q: PanelPane = %q, want %q", rec, got, want)
		}
	}
}
