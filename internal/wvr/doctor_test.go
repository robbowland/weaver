package wvr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robbowland/weaver/internal/rex"
	"github.com/robbowland/weaver/internal/rex/rextest"
	"github.com/robbowland/weaver/internal/session"
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
// fork older than 0.28.0-rb2, upgraded.
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
		{"tuicr 0.28.0-rb2", "0.28.0-rb2", true, nil},
		{"tuicr 0.29.0-rb1", "0.29.0-rb1", true, nil},
		{"tuicr 0.28.0", "0.28.0, upstream's: weaver needs its fork", false, byHand{"brew uninstall tuicr && brew install robbowland/kegs/tuicr"}},
		{"tuicr 0.28.0-rb1", "0.28.0-rb1, weaver needs 0.28.0-rb2 or newer", false, brewUpgrade{"robbowland/kegs/tuicr"}},
		{"tuicr 0.27.2-rb3", "0.27.2-rb3, weaver needs 0.28.0-rb2 or newer", false, brewUpgrade{"robbowland/kegs/tuicr"}},
	} {
		if err := os.WriteFile(bin+"/tuicr", []byte("#!/bin/sh\necho '"+x.says+"'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if c := tuicrCheck(); c.ok != x.ok || c.found != x.found || c.remedy != x.remedy {
			t.Errorf("%s: %+v", x.says, c)
		}
	}
}

// Rex's keys: an init.lua that does not load weaver.lua leaves every
// weaver key doing nothing, which doctor says; plain wvr's writing the keys
// leaves init.lua alone, and --fix's remedy adds the require (init.lua
// backed up beside it) and has Rex read it, after which Rex binds them.
func TestRexKeysCheck(t *testing.T) {
	at, err := os.MkdirTemp("/tmp", "wvr-keys")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(at) })
	init := filepath.Join(at, "init.lua")
	if err := os.WriteFile(init, []byte("-- mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	rextest.ServerAt(t, rex.Bin(), at)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", home+"/data")
	t.Setenv("REX_CONFIG", init)
	e := &Env{&session.Env{StateDir: home + "/state", Self: "/usr/bin/true"}}
	if loads, _, err := e.writeRexKeys(); err != nil || loads {
		t.Fatalf("writing the keys: loads %v, %v", loads, err)
	}
	if b, _ := os.ReadFile(init); string(b) != "-- mine" {
		t.Fatalf("plain wvr's writing the keys changed init.lua: %q", b)
	}
	c := e.rexKeysCheck()
	fix, ok := c.remedy.(selfFix)
	if c.ok || c.weight != needed || !ok || !strings.Contains(fix.text(), `require("weaver")`) {
		t.Fatalf("init.lua without weaver: %+v", c)
	}
	if err := fix.do(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(init); string(b) != "-- mine\nrequire(\"weaver\")\n" {
		t.Errorf("init.lua fixed: %q", b)
	}
	if bk, _ := filepath.Glob(init + ".bak.*"); len(bk) != 1 {
		t.Errorf("backups: %q, want one beside init.lua", bk)
	} else if b, _ := os.ReadFile(bk[0]); string(b) != "-- mine" {
		t.Errorf("backup: %q", b)
	}
	if c := e.rexKeysCheck(); !c.ok {
		t.Errorf("fixed: %+v", c)
	}
	if loaded, err := rex.KeysLoaded(); !loaded {
		t.Errorf("fixed, and Rex binds no weaver key (%v)", err)
	}
}

// Rex not running: init.lua loading weaver is all there is to know, as Rex
// reads it when it starts; one that does not load it, a failure.
func TestRexKeysCheckNoRex(t *testing.T) {
	rextest.NoRex(t)
	init := filepath.Join(t.TempDir(), "init.lua")
	t.Setenv("REX_CONFIG", init)
	e := &Env{&session.Env{StateDir: t.TempDir(), Self: "/usr/bin/true"}}
	if c := e.rexKeysCheck(); c.ok {
		t.Errorf("no init.lua: %+v", c)
	}
	if err := os.WriteFile(init, []byte("pcall(require, 'weaver')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := e.rexKeysCheck(); !c.ok || c.remedy != nil {
		t.Errorf("init.lua loading weaver, Rex not running: %+v", c)
	}
}

// Remote Control is the Rex app's setting, read where the app keeps it;
// no setting to read, no check.
func TestRemoteControlCheck(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	if _, ok := remoteControlCheck(); ok {
		t.Error("nothing to read it with: a check")
	}
	for _, x := range []struct {
		says string
		ok   bool
	}{{"1", true}, {"0", false}} {
		script := "#!/bin/sh\n[ \"$2 $3\" = \"com.superlogical.rex rex.client.remoteControl\" ] && echo " + x.says + "\n"
		if err := os.WriteFile(bin+"/defaults", []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		c, ok := remoteControlCheck()
		if !ok || c.ok != x.ok || c.weight != optional || (c.remedy == nil) == !x.ok {
			t.Errorf("the setting %s: %+v %v", x.says, c, ok)
		}
	}
	if err := os.WriteFile(bin+"/defaults", []byte("#!/bin/sh\necho 'does not exist' >&2; exit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := remoteControlCheck(); ok {
		t.Error("never set: a check")
	}
}
