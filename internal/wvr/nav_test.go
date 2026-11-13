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
