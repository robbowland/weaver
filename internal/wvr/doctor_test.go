package wvr

import (
	"os"
	"strings"
	"testing"

	"github.com/robbowland/weaver/internal/setup"
)

func TestAtLeast(t *testing.T) {
	for _, c := range []struct {
		v     string
		least []int
		want  bool
	}{{"3.5", []int{3, 3}, true}, {"3.2", []int{3, 3}, false}, {"0.74.5", []int{0, 62}, true}, {"0.61.9", []int{0, 62}, false}, {"4", []int{3, 3}, true}, {"3.3", []int{3, 3}, true}} {
		if got := atLeast(c.v, c.least...); got != c.want {
			t.Errorf("atLeast(%s, %v) = %v", c.v, c.least, got)
		}
	}
}

// A tuicr config of your own is left alone, and doctor says which of the
// settings weaver's panel leans on it lacks: q that quits, edits that show
// as they are made.
func TestBroughtCheckOwnTuicrConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home+"/.config")
	t.Setenv("CLAUDE_CONFIG_DIR", home+"/none")
	for _, f := range setup.BroughtFiles() {
		if err := f.Write(); err != nil {
			t.Fatal(err)
		}
	}
	cfg := home + "/.config/tuicr/config.toml"
	if err := os.WriteFile(cfg, []byte("theme = \"dark\"\nq_quits = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := broughtCheck(); c.ok || !strings.Contains(c.found, "diff_watch_interval_ms") || strings.Contains(c.found, "q_quits") {
		t.Errorf("your config without diff_watch_interval_ms: %+v", c)
	}
	if err := os.WriteFile(cfg, []byte("q_quits = true\ndiff_watch_interval_ms = 250\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := broughtCheck(); !c.ok {
		t.Errorf("your config with both: %+v", c)
	}
}
