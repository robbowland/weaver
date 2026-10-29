package process

import (
	"os"
	"os/exec"
	"testing"
)

// A recorded pid of 1 or less is no process weaver knows: kill(0, 0) would
// signal this process's own group and succeed, so it is never alive.
func TestAlive(t *testing.T) {
	c := exec.Command("/usr/bin/true")
	if err := c.Run(); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[int]bool{0: false, 1: false, -1: false, os.Getpid(): true, c.Process.Pid: false} {
		if got := Alive(p); got != want {
			t.Errorf("Alive(%d) = %v, want %v", p, got, want)
		}
	}
}
