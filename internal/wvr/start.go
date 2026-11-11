package wvr

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/robbowland/weaver/internal/rex"
	"github.com/robbowland/weaver/internal/setup"
	"github.com/robbowland/weaver/internal/store"
)

// Start is `wvr [start [session]]` outside a session: weaver's Rex
// session name ($WEAVER_SESSION, else weaver), made with home in its
// first window when it is not running, or runs only as Rex brought it back
// after a restart (rex.Ensure: one wvr at a time, none made while Rex does
// not answer; the husk ended), then shown in the Rex app, which it can be
// only with Remote Control on in Rex's settings (else wvr says where it
// is). Weaver's keys for Rex are written each time (weaver.lua beside
// Rex's init.lua) for init.lua to load, and Rex told to read its
// configuration again when they are new to it.
func (e *Env) Start(name string) error {
	if name == "" {
		name = os.Getenv("WEAVER_SESSION")
	}
	if name == "" {
		name = "weaver"
	}
	e.SelfSetup()
	keys, loads, changed, err := e.writeRexKeys()
	switch {
	case err != nil:
		fmt.Fprintln(os.Stderr, "weaver: could not write weaver's keys for Rex: "+err.Error())
	case !loads:
		fmt.Fprintf(os.Stderr, "weaver: for its keys, add require(\"weaver\") to %s, then run rex config reload\n", tildify(filepath.Join(filepath.Dir(keys), "init.lua")))
	case changed || !rex.KeysLoaded():
		// Rex reads its configuration only when told: one read before
		// weaver.lua was there (or was this) has none of its keys.
		if err := rex.ReloadConfig(); err != nil {
			fmt.Fprintln(os.Stderr, "weaver: Rex did not read weaver's keys: run rex config reload ("+err.Error()+")")
		}
	}
	here, _ := os.Getwd()
	s, husk, err := rex.Ensure(e.RexDir(), name, here, []string{e.Self, "home"}, func() error {
		skey := rex.Key(name)
		if err := os.MkdirAll(e.StateDir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(e.StateDir+"/"+skey+".layout", []byte("weaver\n"), 0o644); err != nil {
			return err
		}
		os.Remove(e.StateDir + "/" + skey + ".last")
		return nil
	})
	if err != nil {
		return fmt.Errorf("weaver could not start on Rex (is Rex running?): %w", err)
	}
	if s.ShowInApp() != nil {
		fmt.Printf("weaver: the %s session runs in Rex: switch to it there (with Remote Control on in Rex's settings, wvr shows it)\n", name)
	}
	if husk != nil {
		fmt.Printf("weaver: Rex restarted and brought the %s session back without what ran in it: started again, its threads with it\n", name)
		// Last: this wvr may be running in it, or in one of its threads'
		// sessions, which Rex brought back as empty.
		if err := (rex.Fleet{Home: *husk}).Quit(); err != nil {
			fmt.Fprintf(os.Stderr, "weaver: could not end what Rex brought back (%s): close it in Rex: %v\n", husk.ID, err)
		}
	}
	return nil
}

// writeRexKeys writes weaver's keys for Rex (config/rex.lua, run by this
// wvr) to weaver.lua beside the init.lua Rex reads ($REX_CONFIG, else
// rex/init.lua in the configuration directory): its path, whether init.lua
// loads it, and whether it was not already as written.
func (e *Env) writeRexKeys() (f string, loads, changed bool, err error) {
	dir, err := setup.Runtime()
	if err != nil {
		return "", false, false, err
	}
	src, err := os.ReadFile(dir + "/config/rex.lua")
	if err != nil {
		return "", false, false, err
	}
	init := os.Getenv("REX_CONFIG")
	if init == "" {
		conf := os.Getenv("XDG_CONFIG_HOME")
		if conf == "" {
			conf = os.Getenv("HOME") + "/.config"
		}
		init = filepath.Join(conf, "rex", "init.lua")
	}
	f = filepath.Join(filepath.Dir(init), "weaver.lua")
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		return f, false, false, err
	}
	// Inside Lua strings: a path's \ and " escaped.
	lua := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace
	keys := strings.NewReplacer("@WVR@", lua(e.Self), "@SESSIONS@", lua(e.RexDir()+"/sessions"),
		"@REXBIN@", lua(rex.BinDir()), "--[[@SETTINGS@]]", luaSettings(lua)).Replace(string(src))
	if old, err := os.ReadFile(f); err != nil || string(old) != keys {
		if err := store.WriteAtomic(f, []byte(keys)); err != nil {
			return f, false, false, err
		}
		changed = true
	}
	b, _ := os.ReadFile(init)
	return f, loadsWeaver(string(b)), changed, nil
}

// rexSettings are the variables a wvr run by a key reads that Rex's server
// does not have: it runs keys' commands with an app's environment, not a
// shell's. Where weaver keeps its state (its sessions' record, which the keys
// themselves are baked with), finds its runtime and config, a thread's
// widths, and its views.
var rexSettings = []string{"XDG_STATE_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME",
	"WEAVER_AGENT_WIDTH", "WEAVER_AGENT_MIN_COLS", "WEAVER_VIEWS"}

// luaSettings is those of rexSettings set here as a Lua table's fields,
// each value escaped by lua (set but empty kept: WEAVER_VIEWS= means none).
func luaSettings(lua func(string) string) string {
	var f []string
	for _, k := range rexSettings {
		if v, ok := os.LookupEnv(k); ok {
			f = append(f, k+` = "`+lua(v)+`"`)
		}
	}
	return strings.Join(f, ", ")
}

// reRequireWeaver is a Lua require of weaver: require("weaver"), require
// "weaver", pcall(require, "weaver"), with either quote.
var reRequireWeaver = regexp.MustCompile(`\brequire[\s(,]*["']weaver["']`)

// loadsWeaver: does init.lua (its text) load weaver.lua? Not in a comment.
func loadsWeaver(init string) bool {
	for _, l := range strings.Split(init, "\n") {
		if code, _, _ := strings.Cut(l, "--"); reRequireWeaver.MatchString(code) {
			return true
		}
	}
	return false
}

// Boot is `wvr _boot SOCKET`: what the weaver session needs as it starts,
// behind its home board (whose fzf listens on SOCKET): old states swept, the
// threads it lost back (unless WEAVER_RESTORE=0), the status line's counts
// and the windows' labels, whose push reloads the board, and the worktrees
// weaver made that are done with (said on the status line).
func (e *Env) Boot(sock string) {
	e.GC()
	if os.Getenv("WEAVER_RESTORE") != "0" {
		for _, l := range e.LostRows() {
			if l.Held == "" && l.Skey == e.SKey {
				e.ResumeSession()
				break
			}
		}
	}
	for i := 0; i < 20; i++ {
		if st, err := os.Stat(sock); err == nil && st.Mode()&os.ModeSocket != 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	e.PushStatusAndLabels()
	if e.WeaverSession() {
		e.StartViews()
	}
	if n := len(e.SweepWorktrees(io.Discard)); n > 0 {
		e.Mux().SayToSession(4000, fmt.Sprintf("weaver: removed %d old worktree%s, nothing in them lost (their branches stay)", n, plural(n)))
	}
}

// Sweep is `wvr gc`: what is gone or left behind (agents' states, old
// files, orphaned nvims, dead sockets), then the worktrees weaver made that
// are done with.
func (e *Env) Sweep() {
	e.GC()
	e.SweepWorktrees(os.Stdout)
}
