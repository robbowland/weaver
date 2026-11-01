package process

import (
	"encoding/binary"
	"os"
	"os/exec"
	"reflect"
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

func TestParseArgs(t *testing.T) {
	b := binary.LittleEndian.AppendUint32(nil, 2)
	b = append(b, "/usr/bin/uv\x00\x00\x00\x00uv\x00run\x00TMUX_PANE=%1\x00PWD=/w\x00\x00junk"...)
	argv, env, ok := parseArgs(b)
	if !ok || !reflect.DeepEqual(argv, []string{"uv", "run"}) || !reflect.DeepEqual(env, map[string]string{"TMUX_PANE": "%1", "PWD": "/w"}) {
		t.Fatalf("got %q %q %v", argv, env, ok)
	}
	if _, _, ok := parseArgs(binary.LittleEndian.AppendUint32(nil, 3)); ok {
		t.Fatal("a truncated block parsed")
	}
}

// End ends what TERM ends, kills what ignores it, and never signals a pid
// of 1 or less (kill(0) is this process's own group).
func TestEnd(t *testing.T) {
	polite := exec.Command("/bin/sleep", "600")
	stubborn := exec.Command("/bin/sh", "-c", "trap '' TERM; while :; do sleep 1; done")
	for _, c := range []*exec.Cmd{polite, stubborn} {
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan struct{}, 2)
	for _, c := range []*exec.Cmd{polite, stubborn} {
		go func() { _ = c.Wait(); done <- struct{}{} }()
	}
	if n := End([]int{0, 1, -1, polite.Process.Pid, stubborn.Process.Pid}); n != 2 {
		t.Errorf("asked %d to end, want 2", n)
	}
	for range 2 {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("a process End was given still runs")
		}
	}
}
