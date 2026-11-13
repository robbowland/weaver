package wvr

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/session"
)

// A fetch's pid left behind (its wvr _fetch killed, the machine shut down)
// is no fetch once another process has that pid: the form neither waits
// on it nor, when the wait ends, ends that process's group.
func TestFetchPidTakenAgain(t *testing.T) {
	dir := t.TempDir()
	repo := dir + "/repo"
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	e := &Env{&session.Env{StateDir: dir + "/state", Self: "/usr/bin/false"}}
	// Another process, a group of its own, now has the pid the fetch had.
	other := exec.Command("/bin/sleep", "60")
	other.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	ended := make(chan struct{})
	go func() { _ = other.Wait(); close(ended) }()
	t.Cleanup(func() { _ = other.Process.Kill(); <-ended })
	f := e.fetchFile(repo)
	if err := os.MkdirAll(dir+"/state/fetch", 0o755); err != nil {
		t.Fatal(err)
	}
	// As the fetch recorded itself: its pid, and when it started.
	if err := os.WriteFile(f+".pid", []byte(strconv.Itoa(other.Process.Pid)+"\nMon Jan  1 00:00:00 2001\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	e.fetchWait(repo, 500*time.Millisecond)
	if took := time.Since(start); took >= 500*time.Millisecond {
		t.Errorf("waited %v on a pid another process has", took.Round(10*time.Millisecond))
	}
	select {
	case <-ended:
		t.Error("the process that took the fetch's pid was ended")
	case <-time.After(300 * time.Millisecond):
	}
}
