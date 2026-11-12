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

// tuicr is Rob's fork or not good enough: missing, it is installed;
// upstream's, it is replaced by hand (the fork's tuicr has its name); a
// fork older than 0.28, upgraded.
func TestTuicrCheck(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	if c := tuicrCheck(); c.ok || c.remedy != (brewInstall{"robbowland/kegs/tuicr"}) {
		t.Errorf("none: %+v", c)
	}
	for _, x := range []struct {
		says, found string
		ok          bool
		remedy      remedy
	}{
		{"tuicr 0.28.0-rb1", "0.28.0-rb1", true, nil},
		{"tuicr 0.29.1-rb3", "0.29.1-rb3", true, nil},
		{"tuicr 0.28.0", "0.28.0, upstream's: weaver needs its fork", false, byHand{"brew uninstall tuicr && brew install robbowland/kegs/tuicr"}},
		{"tuicr 0.27.2-rb1", "0.27.2-rb1, weaver needs 0.28.0-rb1 or newer", false, brewUpgrade{"robbowland/kegs/tuicr"}},
	} {
		if err := os.WriteFile(bin+"/tuicr", []byte("#!/bin/sh\necho '"+x.says+"'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if c := tuicrCheck(); c.ok != x.ok || c.found != x.found || c.remedy != x.remedy {
			t.Errorf("%s: %+v", x.says, c)
		}
	}
}
