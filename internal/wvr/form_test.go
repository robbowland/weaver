package wvr

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
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

// The form's footer keeps every hint in its place as the cursor moves:
// enter's slot is as wide on every row, start's naming the key that starts
// the thread in the background too, which a click on it presses.
func TestFormFooter(t *testing.T) {
	rows := []string{"start", "name", "from", "mode", "repo", "where", "branch", "base"}
	at := map[string]int{}
	for _, id := range rows {
		line := reSGR.ReplaceAllString(formRowFooter(id), "")
		at[id] = ansi.StringWidth(line[:strings.Index(line, "↑↓")])
	}
	for _, id := range rows {
		if at[id] != at["start"] {
			t.Errorf("↑↓ at %d on %s, %d on start: the footer moves", at[id], id, at["start"])
		}
	}
	start := reSGR.ReplaceAllString(formRowFooter("start"), "")
	if !strings.HasPrefix(start, "⏎ START   alt+⏎ IN THE BACKGROUND") {
		t.Errorf("start's footer %q: no alt+⏎ after ⏎", start)
	}
	col := ansi.StringWidth(start[:strings.Index(start, "alt+⏎")]) + 1 // fzf counts columns from 1
	hs := formHints("start")
	if h, ok := hintAt(hs, formGap, col); !ok || hintAct(h.key, "wvr") != "trigger(alt-enter)" {
		t.Errorf("a click on alt+⏎ at column %d: %q, want trigger(alt-enter)", col, h.key)
	}
	if h, _ := hintAt(hs, formGap, 1); hintAct(h.key, "wvr") != "trigger(enter)" {
		t.Errorf("a click on ⏎: %q, want trigger(enter)", h.key)
	}
}

// fzf's alt-enter is weaver's alt+⏎, both ways.
func TestKeyLabelEnter(t *testing.T) {
	if l := keyLabel(formBackground); l != "alt+⏎" || fzfKey(l) != formBackground {
		t.Errorf("keyLabel(%q) = %q, back %q", formBackground, l, fzfKey(l))
	}
}

// wvr new from a shell outside weaver's session starts the thread in
// weaver's session, from the directory it runs in; with weaver's not
// running, it says so.
func TestNewOutsideTheSession(t *testing.T) {
	e, home := outside(t)
	repo, _ := filepath.EvalSymlinks(t.TempDir())
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"-c", "user.email=t@e", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	t.Chdir(repo)
	if err := e.New([]string{"--here", "--no-focus", "say hello"}); err != nil {
		t.Fatalf("wvr new outside: %v", err)
	}
	recs := e.openingRecords()
	if e.Session != "demo" || e.RexID != home.ID || len(recs) != 1 {
		t.Fatalf("wvr new outside: in %q (%s), %d threads opening; want demo (%s), one", e.Session, e.RexID, len(recs), home.ID)
	}
	if th, err := store.ReadThread(recs[0]); err != nil || th.Worktree != repo || th.Task != "say hello" {
		t.Errorf("its thread: %+v (%v), want say hello in %s", th, err, repo)
	}
	if err := home.Quit(); err != nil {
		t.Fatal(err)
	}
	e = &Env{&session.Env{StateDir: e.StateDir, SockDir: e.SockDir, ClaudeDir: e.ClaudeDir, Self: e.Self}}
	if err := e.New([]string{"--here", "--no-focus", "say hello"}); err == nil || !strings.Contains(err.Error(), "no Rex session demo of weaver's is running") {
		t.Errorf("wvr new with weaver's not running: %v, want it said", err)
	}
}
