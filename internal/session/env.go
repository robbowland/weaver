// Package session is weaver: what it knows of its Rex session, its state
// files, the hook engine, the dashboards and threads.
package session

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/rex"
)

// Env is where weaver runs: its Rex session, the pane it runs in, and
// where its files live.
type Env struct {
	Session   string      // the session's name; empty outside one
	SKey      string      // the session's key in file names: rex-<name>
	RexID     string      // the session's Rex id
	MyPane    mux.PaneID  // the pane weaver runs in; empty in a popup
	Where     agent.Where // the life of the session its agents run in (Rex's, or the pane an event is from)
	StateDir  string
	LogDir    string
	LostDir   string
	SockDir   string
	ClaudeDir string
	Self      string // this binary, as it was run (never resolved past a link)
}

// Load reads the environment: the Rex session and pane weaver acts on
// (rex.Chosen; outside one, none), and where weaver keeps its files.
func Load() *Env {
	e := &Env{}
	home := os.Getenv("HOME")
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		state = home + "/.local/state"
	}
	e.StateDir = state + "/weaver"
	e.LogDir = e.StateDir + "/log"
	e.LostDir = e.StateDir + "/lost"
	e.SockDir = "/tmp/weaver-" + strconv.Itoa(os.Getuid())
	e.ClaudeDir = os.Getenv("CLAUDE_CONFIG_DIR")
	if e.ClaudeDir == "" {
		e.ClaudeDir = home + "/.claude"
	}
	e.Self = Executable()
	if !rex.Chosen(e.RexDir()) {
		return e
	}
	h := rex.FromEnv(e.RexDir())
	e.RexID, e.Session, e.MyPane, e.Where = h.ID, h.Name, h.Pane, h.Where
	if e.Session != "" {
		e.SKey = rex.Key(e.Session)
	}
	return e
}

// Executable is the path this binary was run by: a link stays a link
// (Homebrew's bin/wvr outlives an upgrade; the Cellar path it points to
// does not).
func Executable() string {
	p, err := os.Executable()
	if err != nil || p == "" {
		p = os.Args[0]
	}
	if !filepath.IsAbs(p) {
		if a, err := filepath.Abs(p); err == nil {
			p = a
		}
	}
	return p
}

// Mux is the session weaver acts on: the Rex session e.RexID.
func (e *Env) Mux() mux.Mux {
	return rex.Session{ID: e.RexID, Name: e.Session, Dir: e.RexDir()}
}

// RexRestored: is this a Rex session Rex brought back after its server
// restarted, everything weaver ran in it gone (rex.Session.Restored)?
func (e *Env) RexRestored() bool {
	return e.RexID != "" && rex.Session{ID: e.RexID, Name: e.Session, Dir: e.RexDir()}.Restored()
}

// RexDir is where weaver keeps what it knows of Rex (rex's registry).
func (e *Env) RexDir() string { return e.StateDir + "/rex" }

// SockDirReady makes weaver's socket directory, or checks the one there:
// a directory (not a link) of yours, for you alone. What weaver keeps in it
// (fzf's key files, peek's actions) it runs, so one another user made is
// refused, never used.
func (e *Env) SockDirReady() error {
	if err := os.MkdirAll(e.SockDir, 0o700); err != nil {
		return errors.New("cannot create " + e.SockDir)
	}
	st, err := os.Lstat(e.SockDir)
	if err != nil || !st.IsDir() || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return errors.New(e.SockDir + " is not a directory of yours: remove it")
	}
	if st.Mode().Perm() != 0o700 {
		return os.Chmod(e.SockDir, 0o700)
	}
	return nil
}

// StateFile is the state file of the agent in pane p of this session.
func (e *Env) StateFile(p mux.PaneID) string { return e.PaneFile(p, "json") }

// PaneFile is the file of kind ext weaver keeps for the agent in pane p:
// json its state, thread its thread, panel its review panel, ...
func (e *Env) PaneFile(p mux.PaneID, ext string) string {
	return e.StateDir + "/" + e.SKey + "_" + string(p) + "." + ext
}

// SessionFiles are this session's per-pane files with extension ext:
// <skey>_<digits>.<ext> only, so session "a" never claims session "a_b"'s.
func (e *Env) SessionFiles(ext string) []string {
	ents, err := os.ReadDir(e.StateDir)
	if err != nil {
		return nil
	}
	pre, suf := e.SKey+"_", "."+ext
	var out []string
	for _, d := range ents {
		n := d.Name()
		if !strings.HasPrefix(n, pre) || !strings.HasSuffix(n, suf) {
			continue
		}
		p := n[len(pre) : len(n)-len(suf)]
		if p == "" || !digits(p) {
			continue
		}
		out = append(out, e.StateDir+"/"+n)
	}
	return out
}

// WeaverSession: was this session started from weaver's layout?
func (e *Env) WeaverSession() bool {
	l, _ := files.FirstLine(e.StateDir + "/" + e.SKey + ".layout")
	return l == "weaver"
}

func digits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// atoi is s as a number when it is digits.
func atoi(s string) (int, bool) {
	if !digits(s) {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}
