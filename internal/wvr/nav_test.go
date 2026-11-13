package wvr

import (
	"os"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/session"
)

// Home is the window weaver placed at 0 that shows no view, whatever its
// label says: renamed, it is still home, and another window labelled home
// is not.
func TestIsHome(t *testing.T) {
	for w, want := range map[mux.Window]bool{
		{Name: "home", Index: 0}:                 true,
		{Name: "renamed", Index: 0}:              true,
		{Name: "home", Index: -1}:                false,
		{Name: "home", Index: 900, View: "home"}: false,
	} {
		if got := isHome(w); got != want {
			t.Errorf("isHome(%+v) = %v, want %v", w, got, want)
		}
	}
}

// A UI lock left by a press killed before it wrote when it took it (its
// directory alone) goes stale as one that says when does: never holding
// alt+1 off for good.
func TestUILockLeftWithoutItsTime(t *testing.T) {
	dir := t.TempDir()
	e := &Env{&session.Env{StateDir: dir, SKey: "rex-w"}}
	d := dir + "/rex-w.ui"
	if err := os.Mkdir(d, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(d, old, old); err != nil {
		t.Fatal(err)
	}
	release, ok := e.tryUILock()
	if !ok {
		t.Fatal("a lock left a minute ago without its time holds the UI")
	}
	release()
	// One just taken (its time not written yet) holds.
	if err := os.Mkdir(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.tryUILock(); ok {
		t.Error("a lock just taken did not hold")
	}
}

// _pos puts a board's cursor on a thread's row among the rows the board
// shows (those it last loaded), leaving its keys and what its refresh
// compares with as they are: new rows of its own would put the cursor by
// rows not on screen, and have the refresh find nothing changed.
func TestPosInTheRowsShown(t *testing.T) {
	d := t.TempDir()
	t.Setenv("FZF_SOCK", d+"/b.sock")
	shown := "h0\t\t\t\t2\tNEEDS YOU\n\t\t\t\t\t\na4\t4\t3\t\t\tx\t01 x\na7\t7\t6\t\t\ty\t02 y\n"
	if err := os.WriteFile(d+"/b.rows", []byte(shown), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(d+"/b.keys", []byte("keys\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := &Env{&session.Env{StateDir: d + "/state", SKey: "rex-w"}}
	if got := e.Pos("7"); got != "pos(4)\n" {
		t.Errorf("Pos(7) = %q, want pos(4), its row as shown", got)
	}
	if got := e.Pos("9"); got != "" {
		t.Errorf("Pos of a pane with no row: %q", got)
	}
	if b, _ := os.ReadFile(d + "/b.rows"); string(b) != shown {
		t.Errorf("the rows shown, rewritten: %q", b)
	}
	if b, _ := os.ReadFile(d + "/b.keys"); string(b) != "keys\n" {
		t.Errorf("the board's keys, rewritten: %q", b)
	}
}
