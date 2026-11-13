package session

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/process"
	"github.com/robbowland/weaver/internal/rex/rextest"
)

// An agent that last reported before the machine started lost its session,
// even when a server started since has its old server's pid and a session
// of its name.
func TestLostAcrossABoot(t *testing.T) {
	rextest.NoRex(t)
	e := &Env{StateDir: t.TempDir()}
	w := agent.Where{Session: "session:s"}
	if lost, known := e.newLives().lost(process.BootTime()-60, w); !lost || !known {
		t.Errorf("it reported before the machine started: lost %v (known %v), want lost", lost, known)
	}
	if lost, known := e.newLives().lost(time.Now().Unix(), w); lost || known {
		t.Errorf("Rex does not answer: lost %v (known %v), want not known", lost, known)
	}
}

// A thread lost with its session whose lost record cannot be written (its
// directory not writable) keeps its files, for a later look to keep it:
// never forgotten for want of a record of it.
func TestRetireKeepsALostThreadItCannotRecord(t *testing.T) {
	rextest.NoRex(t)
	dir := t.TempDir()
	e := &Env{StateDir: dir + "/state", LostDir: dir + "/lost", SKey: "rex-s"}
	for _, d := range []string{e.StateDir, e.LostDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sf := e.StateFile("3")
	tf := e.PaneFile("3", "thread")
	if err := os.WriteFile(tf, []byte(`{"num":1,"name":"t","session_id":"conv"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// It last reported before the machine started: lost, Rex asked nothing.
	if err := os.WriteFile(sf, []byte(fmt.Sprintf(`{"pane":3,"agent":"claude","state":"working","ts":%d,"session_id":"conv","pid":7}`+"\n", process.BootTime()-60)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(e.LostDir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(e.LostDir, 0o755) }()
	e.Retire(sf, "7")
	if _, err := os.Stat(tf); err != nil {
		t.Errorf("its thread record: %v, want kept", err)
	}
	if _, err := os.Stat(sf); err != nil {
		t.Errorf("its state: %v, want kept", err)
	}
	_ = os.Chmod(e.LostDir, 0o755)
	e.Retire(sf, "7")
	if ls, _ := os.ReadDir(e.LostDir); len(ls) != 1 {
		t.Errorf("once it can be: %d lost records, want 1", len(ls))
	}
}

// A thread whose agent you exited (its pane kept, shown exited) lasts
// until you close it: the machine starting again since keeps it as lost,
// as any thread; marked as kept (as quitting does), so does its session
// going. Its session gone otherwise (its window closed by hand) forgets
// it, ended on purpose.
func TestExitedThreadLasts(t *testing.T) {
	rextest.NoRex(t)
	for _, c := range []struct {
		name   string
		ts     int64
		reason string
		want   int // lost records
	}{
		{"exited, then the machine started again", process.BootTime() - 60, "prompt_input_exit", 1},
		{"logged out, then the machine started again", process.BootTime() - 60, "logout", 1},
	} {
		dir := t.TempDir()
		e := &Env{StateDir: dir + "/state", LostDir: dir + "/lost", SKey: "rex-s"}
		for _, d := range []string{e.StateDir, e.LostDir} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		sf := e.StateFile("3")
		if err := os.WriteFile(e.PaneFile("3", "thread"), []byte(`{"num":1,"name":"t","session_id":"conv"}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(sf, []byte(fmt.Sprintf(`{"pane":3,"agent":"claude","state":"ended","ts":%d,"session_id":"conv","pid":7,"end_reason":%q}`+"\n", c.ts, c.reason)), 0o644); err != nil {
			t.Fatal(err)
		}
		e.Retire(sf, "7")
		if ls, _ := os.ReadDir(e.LostDir); len(ls) != c.want {
			t.Errorf("%s: %d lost records, want %d", c.name, len(ls), c.want)
		}
	}
}

// A thread whose agent ended long ago, being started again (wvr restart),
// is not gc's to forget while its pane's new agent starts.
func TestRestartingIsNotForgotten(t *testing.T) {
	rextest.NoRex(t)
	dir := t.TempDir()
	e := &Env{StateDir: dir + "/state", LostDir: dir + "/lost", SKey: "rex-s"}
	if err := os.MkdirAll(e.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sf, tf := e.StateFile("3"), e.PaneFile("3", "thread")
	if err := os.WriteFile(tf, []byte(`{"num":1,"name":"t","session_id":"conv"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gone := exec.Command("/usr/bin/true")
	if err := gone.Run(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sf, []byte(fmt.Sprintf(`{"pane":3,"agent":"claude","state":"ended","ts":%d,"session_id":"conv","pid":%d,"end_reason":"prompt_input_exit"}`+"\n",
		time.Now().Unix()-7200, gone.Process.Pid)), 0o644); err != nil {
		t.Fatal(err)
	}
	e.Restarting(sf)
	e.gcAgents()
	if !files.Exists(sf) || !files.Exists(tf) {
		t.Errorf("being started again: state %v, record %v; want both kept", files.Exists(sf), files.Exists(tf))
	}
}
