package wvr

import (
	"testing"

	"github.com/robbowland/weaver/internal/mux"
)

// Home is the window named home that shows no view, wherever it is: a
// thread that took index 0 while home was closed is not home, and home
// made again elsewhere is.
func TestIsHome(t *testing.T) {
	for w, want := range map[mux.Window]bool{
		{Name: "home", Index: 0}:                 true,
		{Name: "home", Index: 4}:                 true,
		{Name: "01 fix it", Index: 0}:            false,
		{Name: "home", Index: 900, View: "home"}: false,
	} {
		if got := isHome(w); got != want {
			t.Errorf("isHome(%+v) = %v, want %v", w, got, want)
		}
	}
}
