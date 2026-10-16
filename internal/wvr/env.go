// Package wv is weaver: what it knows of its tmux session, its state files,
// the hook engine, the dashboards and threads.
package wvr

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/robbowland/weaver/internal/tmux"
)

// Env is where weaver runs: its tmux session, the pane it runs in, and
// where its files live.
type Env struct {
	Session   string      // the tmux session's name; empty outside one
	SKey      string      // the session's key in file names: tmux-<name> for tmux
	MyPane    tmux.PaneID // the pane weaver runs in; empty in a popup
	InTmux    bool        // inside a tmux client's pane ($TMUX)
	StateDir  string
	LogDir    string
	LostDir   string
	SockDir   string
	ClaudeDir string
	TSock     []string // weaver's own tmux server: -L name, or -S path
	Self      string   // this binary, as it was run (never resolved past a link)
}

// Load reads the environment: the tmux session, pane and server, and where
// weaver keeps its files.
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
	sock := os.Getenv("WEAVER_TMUX_SOCKET")
	switch {
	case strings.Contains(sock, "/"):
		e.TSock = []string{"-S", sock}
	case sock != "":
		e.TSock = []string{"-L", sock}
	default:
		e.TSock = []string{"-L", "weaver"}
	}
	e.Self = self()
	e.MyPane, _ = tmux.ParsePane(os.Getenv("TMUX_PANE"))
	e.InTmux = os.Getenv("TMUX") != ""
	if e.InTmux {
		// weaver's tmux sessions set it for their panes; anywhere else, ask.
		e.Session = os.Getenv("WEAVER_TMUX_SESSION")
		if e.Session == "" {
			args := []string{"display-message", "-p"}
			if p := os.Getenv("TMUX_PANE"); p != "" {
				args = append(args, "-t", p)
			}
			out, _ := exec.Command("tmux", append(args, "#S")...).Output()
			e.Session = strings.TrimRight(string(out), "\n")
		}
	}
	if e.Session != "" {
		e.SKey = "tmux-" + strings.ReplaceAll(e.Session, "/", "_")
	}
	return e
}

// self is the path this binary was run by: a link stays a link (Homebrew's
// bin/weaver outlives an upgrade; the Cellar path it points to does not).
func self() string {
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

// Tmux runs tmux on the server weaver is in, else weaver's own; its output.
func (e *Env) Tmux(args ...string) ([]byte, error) {
	if !e.InTmux {
		args = append(append([]string{}, e.TSock...), args...)
	}
	return exec.Command("tmux", args...).Output()
}

// runTmux runs a tmux client with these arguments as they are.
func runTmux(args ...string) ([]byte, error) { return exec.Command("tmux", args...).Output() }

// StateFile is the state file of the agent in pane p of this session.
func (e *Env) StateFile(p tmux.PaneID) string { return e.paneFile(p, "json") }

// nvimSock is the socket of the nvim of the agent in pane p.
func (e *Env) nvimSock(p tmux.PaneID) string {
	return e.SockDir + "/" + e.SKey + "_" + string(p) + ".sock"
}

// paneFile is the file of kind ext weaver keeps for the agent in pane p:
// json its state, thread its thread, nvim its nvim, ...
func (e *Env) paneFile(p tmux.PaneID, ext string) string {
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
	l, _ := firstLine(e.StateDir + "/" + e.SKey + ".layout")
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

// firstLine is a file's first line (as `read -r`: without its newline).
func firstLine(f string) (string, error) {
	b, err := os.ReadFile(f)
	if err != nil {
		return "", err
	}
	if i := strings.IndexByte(string(b), '\n'); i >= 0 {
		b = b[:i]
	}
	return string(b), nil
}

// readTrim is a file's content without its trailing newlines, as $(<file).
func readTrim(f string) (string, error) {
	b, err := os.ReadFile(f)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(b), "\n"), nil
}

// putFile leaves FILE holding TEXT and a newline, for readers that may read
// it meanwhile (fzf's keys, its transforms): alone when it holds that
// already, else replaced whole (a rename); a new file is just written.
func putFile(f, text string) {
	text = strings.TrimRight(text, "\n")
	if cur, err := readTrim(f); err == nil {
		if cur == text {
			return
		}
		tmp := f + "." + strconv.Itoa(os.Getpid())
		if os.WriteFile(tmp, []byte(text+"\n"), 0o644) == nil {
			os.Rename(tmp, f)
		}
		return
	}
	os.WriteFile(f, []byte(text+"\n"), 0o644)
}

func exists(f string) bool {
	_, err := os.Lstat(f)
	return err == nil
}
