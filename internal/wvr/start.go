package wvr

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/oneline"
	"github.com/robbowland/weaver/internal/rex"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/setup"
	"github.com/robbowland/weaver/internal/store"
	"github.com/robbowland/weaver/internal/tmux"
)

// Start is `wvr [start [session]]` outside a session: attach to the
// weaver session on weaver's own tmux server (with config/tmux.conf, read
// again each time, so an install reaches a running server), or start it
// with home in window 0. Its panes know their session
// ($WEAVER_TMUX_SESSION); an outer tmux's or zellij's variables stay out
// of it.
func (e *Env) Start(name string) error {
	if name == "" {
		name = os.Getenv("WEAVER_SESSION")
	}
	if name == "" {
		name = "weaver"
	}
	if e.OnRex() {
		return e.startRex(name)
	}
	bin, err := exec.LookPath("tmux")
	if err != nil {
		return errors.New("tmux is not installed: wvr doctor --fix installs what weaver needs")
	}
	e.SelfSetup()
	dir, err := setup.Runtime()
	if err != nil {
		return err
	}
	conf := dir + "/config/tmux.conf"
	if !files.Exists(conf) {
		return errors.New("missing " + conf)
	}
	var env []string
	for _, v := range os.Environ() {
		k, _, _ := strings.Cut(v, "=")
		switch k {
		case "ZELLIJ", "ZELLIJ_SESSION_NAME", "ZELLIJ_PANE_ID", "TMUX", "TMUX_PANE":
			continue
		}
		env = append(env, v)
	}
	base := append(append([]string{bin}, e.TSock...), "-f", conf)
	has := exec.Command(base[0], append(append([]string{}, base[1:]...), "has-session", "-t", "="+name)...)
	has.Env = env
	if has.Run() == nil {
		return fmt.Errorf("tmux: %w", syscall.Exec(bin, append(base, "source-file", conf, ";", "attach-session", "-t", "="+name), env))
	}
	skey := session.KeyOf(name)
	os.MkdirAll(e.StateDir, 0o755)
	os.WriteFile(e.StateDir+"/"+skey+".layout", []byte("weaver\n"), 0o644)
	os.Remove(e.StateDir + "/" + skey + ".last")
	// The server keeps the directory it starts in: from /, never a worktree
	// weaver close may remove under it (its new panes would die at once).
	here, _ := os.Getwd()
	if os.Chdir("/") != nil {
		return errors.New("cannot cd /")
	}
	return fmt.Errorf("tmux: %w", syscall.Exec(bin, append(base, "new-session", "-s", name, "-n", "home", "-c", tmux.Text(here), "-e", "WEAVER_TMUX_SESSION="+name,
		e.Self, "home", ";", "set-environment", "-t", "="+name, "WEAVER_TMUX_SESSION", name, ";", "source-file", conf), env))
}

// startRex is Start on Rex ($WEAVER_MUX=rex): weaver's Rex session name,
// made with home in its first window when it is not running, then shown in
// the Rex app, which it can be only with Remote Control on in Rex's
// settings (else wvr says where it is). Weaver's keys for Rex are written
// each time (~/.config/rex/weaver.lua) for init.lua to load.
func (e *Env) startRex(name string) error {
	e.SelfSetup()
	keys, loaded, err := e.writeRexKeys()
	if err != nil {
		fmt.Fprintln(os.Stderr, "weaver: could not write weaver's keys for Rex: "+err.Error())
	} else if !loaded {
		fmt.Fprintf(os.Stderr, "weaver: for its keys, add require(\"weaver\") to %s, then run rex config reload\n", tildify(filepath.Join(filepath.Dir(keys), "init.lua")))
	}
	s, ok := rex.Named(e.RexDir(), name)
	if !ok {
		skey := rex.Key(name)
		if err := os.MkdirAll(e.StateDir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(e.StateDir+"/"+skey+".layout", []byte("weaver\n"), 0o644); err != nil {
			return err
		}
		os.Remove(e.StateDir + "/" + skey + ".last")
		here, _ := os.Getwd()
		if s, err = rex.Start(e.RexDir(), name, here, []string{e.Self, "home"}); err != nil {
			return fmt.Errorf("weaver could not start on Rex (is Rex running?): %w", err)
		}
	}
	if s.ShowInApp() != nil {
		fmt.Printf("weaver: the %s session runs in Rex: switch to it there (with Remote Control on in Rex's settings, wvr shows it)\n", name)
	}
	return nil
}

// writeRexKeys writes weaver's keys for Rex (config/rex.lua, run by this
// wvr) to weaver.lua beside Rex's init.lua: its path, and whether init.lua
// loads it.
func (e *Env) writeRexKeys() (string, bool, error) {
	dir, err := setup.Runtime()
	if err != nil {
		return "", false, err
	}
	src, err := os.ReadFile(dir + "/config/rex.lua")
	if err != nil {
		return "", false, err
	}
	conf := os.Getenv("XDG_CONFIG_HOME")
	if conf == "" {
		conf = os.Getenv("HOME") + "/.config"
	}
	f := filepath.Join(conf, "rex", "weaver.lua")
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		return f, false, err
	}
	if err := store.WriteAtomic(f, []byte(strings.ReplaceAll(string(src), "@WVR@", e.Self))); err != nil {
		return f, false, err
	}
	init, _ := os.ReadFile(filepath.Join(filepath.Dir(f), "init.lua"))
	return f, strings.Contains(string(init), `require("weaver")`) || strings.Contains(string(init), `require "weaver"`), nil
}

// Boot is `wvr _boot SOCKET`: what the weaver session needs as it starts,
// behind its home board (whose fzf listens on SOCKET): old states swept, the
// threads it lost back (unless WEAVER_RESTORE=0), the status line's counts
// and the windows' labels, whose push reloads the board, and the worktrees
// weaver made that are done with (said on the status line).
func (e *Env) Boot(sock string) {
	e.GC()
	if os.Getenv("WEAVER_RESTORE") != "0" {
		also := strings.TrimPrefix(e.SKey, "tmux-")
		for _, l := range e.LostRows() {
			if l.Skey == e.SKey || l.Skey == also {
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

// Sweep is `wvr gc [--dry-run]`: what is gone or left behind (agents'
// states, old files, orphaned nvims, what agents left running in panes
// that have gone, dead sockets), then the worktrees weaver made that are
// done with. --dry-run lists what agents left running, and ends nothing.
func (e *Env) Sweep(args []string) {
	if len(args) > 0 && (args[0] == "--dry-run" || args[0] == "-n") {
		ls := e.Leftovers()
		for _, l := range ls {
			fmt.Printf("would end %d (%s, %s; pane %s, %s): %s\n", l.Pid, session.Processes(len(l.Procs)),
				memOf(l.Procs), l.Pane, tildify(l.Dir), oneline.Cut(l.Cmd, 80))
		}
		if len(ls) == 0 {
			fmt.Println("weaver: nothing left running by threads that have gone")
		}
		return
	}
	if n := e.SweepLeftovers(); n > 0 {
		fmt.Printf("weaver: ended %s left running by threads that have gone\n", session.Processes(n))
	}
	e.GC()
	e.SweepWorktrees(os.Stdout)
}

// memOf is how much memory processes pids hold between them, as top
// counts it (their footprint: ps's resident sets leave out what macOS has
// compressed), as "98M".
func memOf(pids []int) string {
	args := []string{"-l", "1", "-s", "0", "-stats", "pid,mem"}
	for _, p := range pids {
		args = append(args, "-pid", strconv.Itoa(p))
	}
	out, _ := exec.Command("top", args...).Output()
	var mb float64
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) != 2 || !digits(f[0]) {
			continue
		}
		v := strings.TrimRight(f[1], "+-")
		if len(v) < 2 {
			continue
		}
		n, err := strconv.ParseFloat(v[:len(v)-1], 64)
		if err != nil {
			continue
		}
		switch v[len(v)-1] {
		case 'K':
			mb += n / 1024
		case 'M':
			mb += n
		case 'G':
			mb += n * 1024
		}
	}
	return strconv.Itoa(int(mb+0.5)) + "M"
}
