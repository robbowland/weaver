package engine

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/tmux"
	"golang.org/x/sys/unix"
)

// Codex runs its hooks in its app server, a daemon every Codex session on
// the machine shares, started by whichever session came first: the hook's
// environment ($TMUX, TMUX_PANE, the session) is that session's pane, on a
// server that may have gone since, not this one's. A Codex hook not run
// from inside the pane its TMUX_PANE names finds its pane by its session
// instead: the pane bound to it on this server ($STATE_DIR/codex/<session>:
// the pane and its server's pid), else the one Codex pane whose directory
// is the session's (then bound to it). Its agent is that pane's process, and
// its tmux session, server and terminal are that pane's, not the daemon's.
// One it cannot place is no pane's: never the daemon's own pane's.

// paneInfo is a pane of this tmux server as a Codex hook needs it: which it
// is, its process, what it runs and where, its session (name, and which life
// of it) and its terminal.
type paneInfo struct {
	id       tmux.PaneID
	pid      int
	cmd, cwd string
	session  string
	where    agent.Where
	tty      string
}

// codexPlace is where a Codex hook's event goes: the pane, and whether the
// hook runs in that pane's own process (its environment is the pane's) or
// in the daemon (the pane's facts stand in for its environment's).
type codexPlace struct {
	pane   paneInfo
	inPane bool
}

// codexPane is where Codex session sid's hook goes, for a hook whose
// environment names pane env, the session working in cwd; ok false when it
// cannot tell.
func codexPane(e *session.Env, env tmux.PaneID, sid, cwd string) (codexPlace, bool) {
	panes := serverPanes(e)
	if p, ok := panes[env]; ok && ancestor(p.pid) {
		return codexPlace{p, true}, true
	}
	codex := func(id tmux.PaneID) (paneInfo, bool) {
		p, ok := panes[id]
		return p, ok && p.cmd == "codex"
	}
	dir := filepath.Join(e.StateDir, "codex")
	if p, ok := codex(boundPane(dir, sid, panes)); ok {
		return codexPlace{p, false}, true
	}
	at := realPath(cwd)
	var in []paneInfo
	for _, p := range panes {
		if p.cmd == "codex" && at != "" && realPath(p.cwd) == at {
			in = append(in, p)
		}
	}
	// Two Codex panes in one directory: the one no other session holds.
	if len(in) > 1 {
		held := map[tmux.PaneID]bool{}
		ms, _ := filepath.Glob(filepath.Join(dir, "*"))
		for _, m := range ms {
			if s := filepath.Base(m); s != sid {
				held[boundPane(dir, s, panes)] = true
			}
		}
		var free []paneInfo
		for _, p := range in {
			if !held[p.id] {
				free = append(free, p)
			}
		}
		in = free
	}
	if len(in) != 1 {
		return codexPlace{}, false
	}
	if validSid(sid) && os.MkdirAll(dir, 0o755) == nil {
		b := string(in[0].id) + " " + strconv.Itoa(in[0].where.Server) + "\n"
		bestEffort(os.WriteFile(filepath.Join(dir, sid), []byte(b), 0o644))
	}
	return codexPlace{in[0], false}, true
}

// boundPane is the pane session sid is bound to, when its binding is this
// server's ("" otherwise: a server started since numbers its panes again).
func boundPane(dir, sid string, panes map[tmux.PaneID]paneInfo) tmux.PaneID {
	if !validSid(sid) {
		return ""
	}
	l, _ := files.FirstLine(filepath.Join(dir, sid))
	id, srv, _ := strings.Cut(l, " ")
	p, ok := panes[tmux.PaneID(id)]
	if !ok || srv == "" || srv != strconv.Itoa(p.where.Server) {
		return ""
	}
	return p.id
}

func validSid(sid string) bool { return sid != "" && !strings.ContainsAny(sid, "/.") }

// forgetCodexSession drops session sid's binding to its pane (it ended).
func forgetCodexSession(e *session.Env, sid string) {
	if validSid(sid) {
		os.Remove(filepath.Join(e.StateDir, "codex", sid))
	}
}

// serverPanes are the panes of this tmux server, by id.
func serverPanes(e *session.Env) map[tmux.PaneID]paneInfo {
	out, err := e.Tmux("list-panes", "-a", "-F",
		"#{pane_id}\t#{pane_pid}\t#{pane_current_command}\t#{pane_current_path}\t#{pid}\t#{session_id}\t#{pane_tty}\t#{session_name}")
	m := map[tmux.PaneID]paneInfo{}
	if err != nil {
		return m
	}
	for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		f := strings.SplitN(l, "\t", 8)
		if len(f) != 8 {
			continue
		}
		id, ok := tmux.ParsePane(f[0])
		pid, err := strconv.Atoi(f[1])
		srv, err2 := strconv.Atoi(f[4])
		if ok && err == nil && err2 == nil {
			m[id] = paneInfo{id: id, pid: pid, cmd: f[2], cwd: f[3], session: f[7],
				where: agent.Where{Server: srv, Session: f[5]}, tty: f[6]}
		}
	}
	return m
}

// ancestor: is pid this process's parent, or its parent's, and so on?
func ancestor(pid int) bool {
	p := os.Getppid()
	for i := 0; i < 16 && p > 1; i++ {
		if p == pid {
			return true
		}
		k, err := unix.SysctlKinfoProc("kern.proc.pid", p)
		if err != nil {
			return false
		}
		p = int(k.Eproc.Ppid)
	}
	return false
}

func realPath(p string) string {
	if p == "" {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}
