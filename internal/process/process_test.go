package process

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
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
func TestAlive(t *testing.T) {
	for p, want := range map[int]bool{0: false, 1: false, -1: false, os.Getpid(): true, deadPid(t): false} {
		if got := Alive(p); got != want {
			t.Errorf("Alive(%d) = %v, want %v", p, got, want)
		}
	}
}

// A recorded agent's pid of 1 or less is no process weaver knows: never
// running; no pid at all cannot be checked, and runs. One whose start time
// is not the recorded one is another process, given the pid again.
func TestStillRuns(t *testing.T) {
	me := strconv.Itoa(os.Getpid())
	for pid, want := range map[string]bool{"0": false, "1": false, me: true, "": true, strconv.Itoa(deadPid(t)): false} {
		if got := StillRuns(pid, "", nil); got != want {
			t.Errorf("StillRuns(%q) = %v, want %v", pid, got, want)
		}
	}
	if StillRuns(me, "Thu Jan  1 00:00:00 1970", nil) {
		t.Error("a pid given again, its start time another: runs")
	}
}

// The start time from the kernel reads as ps prints it (StillRuns compares
// it with proc_start, which hooks take from ps or Claude Code).
func TestStartsMatchPs(t *testing.T) {
	pids := []int{os.Getpid(), os.Getppid(), 1}
	got := Starts(pids)
	for _, p := range pids {
		c := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(p))
		c.Env = append(os.Environ(), "TZ=UTC", "LC_ALL=C")
		out, err := c.Output()
		if err != nil {
			t.Fatal(err)
		}
		want := strings.Join(strings.Fields(string(out)), " ")
		if g := strings.Join(strings.Fields(got[p]), " "); g != want {
			t.Errorf("pid %d: kernel %q, ps %q", p, g, want)
		}
		// launchd (pid 1) is root's: kill -0 is refused.
		if p != 1 && !StillRuns(strconv.Itoa(p), string(out), got) {
			t.Errorf("pid %d: not running with ps's start time", p)
		}
	}
}

// A process runs under another when it is that one, its child, or further
// down; one beside it does not.
func TestRuns(t *testing.T) {
	c := exec.Command("/bin/sh", "-c", "/bin/sleep 30 & wait")
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Process.Kill(); _ = c.Wait() })
	for i := 0; i < 50 && !Runs(c.Process.Pid, "sleep"); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if !Runs(c.Process.Pid, "sleep") {
		t.Error("the sleep under the shell: not found")
	}
	if Runs(c.Process.Pid, "nvim") || Runs(os.Getpid(), "sleepy") {
		t.Error("a process that does not run there: found")
	}
}
