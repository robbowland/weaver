package wvr

import (
	"testing"

	"github.com/robbowland/weaver/internal/mux"
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
