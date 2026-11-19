package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robbowland/weaver/internal/rex"
	"github.com/robbowland/weaver/internal/rex/rextest"
)

// TestMain runs wvr itself when a test asks (WVR_TEST_MAIN: its arguments,
// one per line), so a test sees what it says and how it exits.
func TestMain(m *testing.M) {
	if a, ok := os.LookupEnv("WVR_TEST_MAIN"); ok {
		os.Args = append([]string{"wvr"}, strings.Split(a, "\n")...)
		main()
		return
	}
	os.Exit(m.Run())
}

// A key's wvr (WEAVER_KEY=1, as weaver.lua runs it) that fails says why as
// a message where the key was pressed as well as on stderr, which only
// Rex's log reads; one that has said it already or was cancelled says
// nothing more; outside a key, stderr alone.
func TestKeySaysItsError(t *testing.T) {
	rextest.Server(t, rex.Bin())
	state := t.TempDir()
	home, err := rex.Start(filepath.Join(state, "weaver", "rex"), "demo", "/", []string{"/bin/sleep", "600"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = home.Quit() })
	wvr := func(key bool, args ...string) (string, int) {
		t.Helper()
		c := exec.Command(os.Args[0], "-test.run=^$")
		c.Env = append(os.Environ(), "WVR_TEST_MAIN="+strings.Join(args, "\n"), "XDG_STATE_HOME="+state, "REX_SESSION="+home.ID,
			"VISUAL=/usr/bin/true")
		if key {
			c.Env = append(c.Env, "WEAVER_KEY=1")
		}
		var stderr strings.Builder
		c.Stderr = &stderr
		err := c.Run()
		code := 0
		if x, ok := err.(*exec.ExitError); ok {
			code = x.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return stderr.String(), code
	}
	// The labels of the session's messages (floating layers) shown now.
	said := func() string {
		out, err := exec.Command(rex.Bin(), "--autostart=false", "api", "call", "session.view", `{"session_id":"`+home.ID+`"}`).Output()
		if err != nil {
			t.Fatal(err)
		}
		var v struct {
			Windows []struct {
				Layers []struct {
					Kind   string `json:"kind"`
					Blocks []struct {
						Label string `json:"label"`
					} `json:"blocks"`
				} `json:"layers"`
			} `json:"windows"`
		}
		if err := json.Unmarshal(out, &v); err != nil {
			t.Fatal(err)
		}
		var ls []string
		for _, w := range v.Windows {
			for _, l := range w.Layers {
				for _, b := range l.Blocks {
					if l.Kind == "floating" {
						ls = append(ls, b.Label)
					}
				}
			}
		}
		return strings.Join(ls, "|")
	}
	const usage = "weaver: usage: wvr go home|local|back|next|prev"
	if out, code := wvr(false, "go", "nowhere"); out != usage+"\n" || code != 1 || said() != "" {
		t.Errorf("wvr go nowhere from a terminal: %q, exit %d, said %q; want the usage on stderr alone, 1", out, code, said())
	}
	// _form without its arguments: an exit of 1 with nothing to say, as
	// cli.ErrSaid; wvr new --edit with the task left empty: cancelled.
	if out, code := wvr(true, "_form"); out != "" || code != 1 || said() != "" {
		t.Errorf("a key's wvr that has said all: %q, exit %d, said %q; want nothing more, 1", out, code, said())
	}
	if out, code := wvr(true, "new", "--edit"); out != "" || code != 130 || said() != "" {
		t.Errorf("a key's wvr cancelled: %q, exit %d, said %q; want nothing, 130", out, code, said())
	}
	if out, code := wvr(true, "go", "nowhere"); out != usage+"\n" || code != 1 || said() != usage {
		t.Errorf("a key's wvr go nowhere: %q, exit %d, said %q; want %q on stderr and said, 1", out, code, said(), usage)
	}
}
