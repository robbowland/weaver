package session

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/agent"
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
