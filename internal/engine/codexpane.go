package engine

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/tmux"
	"golang.org/x/sys/unix"
)

// Codex runs its hooks in its app server, a daemon every Codex session on
// the machine shares, started by whichever session came first: the hook's
// environment (TMUX_PANE) is that session's pane, long closed perhaps, not
// this one's. A Codex hook not run from inside the pane its TMUX_PANE names
// finds its pane by its session instead: the pane bound to it
// ($STATE_DIR/codex/<session>), else the one Codex pane whose directory is
// the session's (then bound to it). Its agent is that pane's process, not
// the daemon.

// codexPane is the pane and agent pid of Codex session sid working in cwd,
// for a hook whose environment names pane env ("": the pid as found); ok
// false when it cannot tell, and env's pane is all there is to go by.
func codexPane(e *session.Env, env tmux.PaneID, sid, cwd string) (tmux.PaneID, string, bool) {
	panes := codexPanes(e)
	if p, ok := panes[env]; ok && ancestor(p.pid) {
		return env, "", true
	}
	dir := filepath.Join(e.StateDir, "codex")
	bound := ""
	if sid != "" && !strings.ContainsAny(sid, "/.") {
		bound, _ = files.FirstLine(filepath.Join(dir, sid))
	}
	if p, ok := panes[tmux.PaneID(bound)]; ok {
		return p.id, strconv.Itoa(p.pid), true
	}
	at := realPath(cwd)
	var in []codexPaneInfo
	for _, p := range panes {
		if at != "" && realPath(p.cwd) == at {
			in = append(in, p)
		}
	}
	// Two Codex panes in one directory: the one no other session holds.
	if len(in) > 1 {
		held := map[tmux.PaneID]bool{}
		ms, _ := filepath.Glob(filepath.Join(dir, "*"))
		for _, m := range ms {
			if filepath.Base(m) != sid {
				if l, err := files.FirstLine(m); err == nil {
					held[tmux.PaneID(l)] = true
				}
			}
		}
		var free []codexPaneInfo
		for _, p := range in {
			if !held[p.id] {
				free = append(free, p)
			}
		}
		in = free
	}
	if len(in) != 1 {
		return "", "", false
	}
	if sid != "" && !strings.ContainsAny(sid, "/.") && os.MkdirAll(dir, 0o755) == nil {
		bestEffort(os.WriteFile(filepath.Join(dir, sid), []byte(string(in[0].id)+"\n"), 0o644))
	}
	return in[0].id, strconv.Itoa(in[0].pid), true
}

// forgetCodexSession drops session sid's binding to its pane (it ended).
func forgetCodexSession(e *session.Env, sid string) {
	if sid != "" && !strings.ContainsAny(sid, "/.") {
		os.Remove(filepath.Join(e.StateDir, "codex", sid))
	}
}

type codexPaneInfo struct {
	id  tmux.PaneID
	pid int
	cwd string
}

// codexPanes are the panes of this tmux server running Codex, by id.
func codexPanes(e *session.Env) map[tmux.PaneID]codexPaneInfo {
	out, err := e.Tmux("list-panes", "-a", "-F", "#{pane_id}\t#{pane_pid}\t#{pane_current_command}\t#{pane_current_path}")
	m := map[tmux.PaneID]codexPaneInfo{}
	if err != nil {
		return m
	}
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.SplitN(l, "\t", 4)
		if len(f) != 4 || f[2] != "codex" {
			continue
		}
		id, ok := tmux.ParsePane(f[0])
		pid, err := strconv.Atoi(f[1])
		if ok && err == nil {
			m[id] = codexPaneInfo{id, pid, f[3]}
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
