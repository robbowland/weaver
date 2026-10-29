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

// A recorded pid of 1 or less is no process weaver knows: kill(0, 0) would
// signal this process's own group and succeed, so it is never alive.
func TestPidAlive(t *testing.T) {
	me := os.Getpid()
	for pid, want := range map[int]bool{0: false, 1: false, -1: false, me: true, deadPid(t): false} {
		if got := PidAlive(pid); got != want {
			t.Errorf("PidAlive(%d) = %v, want %v", pid, got, want)
		}
	}
	for pid, want := range map[string]bool{"0": false, "1": false, strconv.Itoa(me): true, "": true} {
		if got := AgentAlive(pid, "", nil); got != want {
			t.Errorf("AgentAlive(%q) = %v, want %v", pid, got, want)
		}
	}
}

// An nvim recorded with a pid of 0 (one the multiplexer did not say) has
// not been seen to run: never its agent's nvim. One recorded without a pid
// is its pane until it records itself.
func TestNvimPaneUnknownPid(t *testing.T) {
	dir := t.TempDir()
	e := &Env{StateDir: dir, SKey: "rex-w"}
	for rec, want := range map[string]mux.PaneID{
		"6\n0\n":                                 "",
		"6\n1\n":                                 "",
		"6\n" + strconv.Itoa(deadPid(t)) + "\n":  "",
		"6\n" + strconv.Itoa(os.Getpid()) + "\n": "6",
		"6\n\n":                                  "6",
	} {
		if err := os.WriteFile(e.PaneFile("5", "nvim"), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := e.NvimPane("5"); got != want {
			t.Errorf("record %q: NvimPane = %q, want %q", rec, got, want)
		}
	}
}
