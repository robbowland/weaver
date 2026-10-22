package wvr

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/session"
)

// A board or view starts again in the weaver wvr resolves to: the same file
// changed since it started (an upgrade in place), another file ahead on
// $PATH (install.sh's over Homebrew's), or another once the one it started
// as has gone; not while it is the one wvr finds.
func TestSuccessor(t *testing.T) {
	brew, local := t.TempDir(), t.TempDir()
	put := func(dir string, at time.Time) string {
		p := filepath.Join(dir, "wvr")
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
		return p
	}
	old := time.Now().Add(-time.Hour)
	self := put(brew, old)
	t.Setenv("PATH", local+":"+brew)
	e := &Env{&session.Env{Self: self}}
	born := filepath.Join(t.TempDir(), "view.born")
	if _, ok := e.successor(born); ok {
		t.Error("a successor of a view never marked")
	}
	e.markBorn(born)
	if next, ok := e.successor(born); ok {
		t.Errorf("started again in %s, the weaver it runs", next)
	}
	// install.sh's, ahead on $PATH, built before the view started.
	ahead := put(local, old)
	if next, ok := e.successor(born); !ok || next != ahead {
		t.Errorf("another install ahead on PATH: %q %v", next, ok)
	}
	// It goes again: Homebrew's, the one the view started as, is not new.
	if err := os.Remove(ahead); err != nil {
		t.Fatal(err)
	}
	if next, ok := e.successor(born); ok {
		t.Errorf("started again in %s, the weaver it runs", next)
	}
	// Upgraded in place.
	put(brew, time.Now().Add(time.Minute))
	if next, ok := e.successor(born); !ok || next != self {
		t.Errorf("upgraded in place: %q %v", next, ok)
	}
	// Started from install.sh's, since removed: Homebrew's.
	e.Self = put(local, old)
	e.markBorn(born)
	if err := os.Remove(e.Self); err != nil {
		t.Fatal(err)
	}
	if next, ok := e.successor(born); !ok || next != self {
		t.Errorf("the one it started as gone: %q %v", next, ok)
	}
}
