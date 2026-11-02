package wvr

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/engine"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/nvim"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/setup"
	"github.com/robbowland/weaver/internal/store"
	"github.com/robbowland/weaver/internal/tmux"
)

// The agent's nvim: `wvr edit` (Alt v, e on the board) opens it beside the
// agent, or goes to it; `wvr nvim` is what runs in its pane.

// openLeftOf opens a pane running argv left of pane target, in its window;
// the new pane.
func (e *Env) openLeftOf(target mux.PaneID, cwd, name string, argv ...string) (mux.PaneID, error) {
	p, _, err := e.Mux().SplitLeft(target, mux.NewSplit{Spawn: mux.Spawn{Argv: argv, Dir: cwd}, Title: name})
	return p, err
}

// ThreadWidth is `wvr _width AGENT` (settleWidths' passes): threadWidth,
// of a pane as given, unless you moved its split since (a multiplexer that
// can tell: Rex, whose app evens a split out, and weaver sets it back):
// then it is yours.
func (e *Env) ThreadWidth(agent string) {
	p, ok := tmux.ParsePane(agent)
	if !ok {
		return
	}
	if m, ok := e.Mux().(interface{ Moved(mux.PaneID) bool }); ok && m.Moved(p) {
		return
	}
	e.threadWidth(p)
}

// threadWidth: back to a thread's split once its nvim reopens beside the
// agent: the agent at $WEAVER_AGENT_WIDTH on the right, while nvim and the
// agent are the window's only panes.
func (e *Env) threadWidth(agent mux.PaneID) {
	ps, _ := e.Mux().Panes()
	ap, ok := session.PaneIn(ps, agent)
	if !ok || len(windowPanes(ps, ap.Window)) != 2 {
		return
	}
	total, ok := e.windowWidth(ap.Window)
	if !ok {
		return
	}
	e.Mux().SetWidth(agent, agentCols(total))
}

// settleWidths puts thread agent's window back to its widths (ThreadWidth)
// once the Rex app has evened out its panes: it does that, within a second
// or so and with nothing to say when, to the window it shows whenever its
// panes change (a pane split in, or put in another's place). Detached, so
// what asked goes on at once; twice, for an app slow to it. Nothing on
// tmux, which keeps a pane's width.
func (e *Env) settleWidths(agent mux.PaneID) {
	if !e.OnRex() {
		return
	}
	c := exec.Command("/bin/sh", "-c", `sleep 1; "$0" _width "$1"; sleep 1.5; exec "$0" _width "$1"`, e.Self, string(agent))
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	bestEffort(c.Start())
}

// editLock is one `wvr edit` per agent at a time, so a second Alt v finds
// the nvim the first is opening. A lock older than 5s was left by a killed
// process.
func (e *Env) editLock(agent mux.PaneID) func() {
	d := e.PaneFile(agent, "opening")
	os.MkdirAll(e.StateDir, 0o755)
	for i := 0; i < 60; i++ {
		if os.Mkdir(d, 0o755) == nil {
			break
		}
		now := time.Now().Unix()
		t := now
		if l, err := files.FirstLine(d + "/at"); err == nil {
			if n, ok := atoi(l); ok {
				t = int64(n)
			}
		}
		if now-t > 5 {
			os.RemoveAll(d)
			continue
		}
		time.Sleep(100 * time.Millisecond)
	}
	os.WriteFile(d+"/at", []byte(strconv.FormatInt(time.Now().Unix(), 10)+"\n"), 0o644)
	return func() { os.RemoveAll(d) }
}

// focusedPane is the pane you are on (a popup or run-shell is not a pane:
// the session's active one).
func (e *Env) focusedPane() mux.PaneID {
	p, _ := e.Mux().Active()
	if p == e.MyPane {
		return ""
	}
	return p
}

// agentWhere is the agent in pane p as weaver knows it (its state, else
// its kind from its thread), the worktree it is in (its state's root, else
// its directory, else its thread's worktree) and the file under it it was
// last at, if any; false when weaver knows of no worktree it is in that is
// there. Its nvim's reviews follow that worktree, so it is never a guess.
func (e *Env) agentWhere(p mux.PaneID) (rec agent.Record, root, file string, known bool) {
	if s, err := store.ReadState(e.StateFile(p)); err == nil {
		rec, root, file = s, s.Root, s.File
		if root == "" {
			root = s.Cwd
		}
	} else if t, err := store.ReadThread(e.ThreadFile(p)); err == nil {
		rec.Agent, root = string(t.Agent), t.Worktree
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return rec, "", "", false
	}
	// git gives the root as a real path; the agent may have used a
	// symlinked one (/tmp vs /private/tmp), so compare real paths.
	if file != "" && files.Exists(file) {
		if d, err := filepath.EvalSymlinks(filepath.Dir(file)); err == nil {
			file = d + "/" + filepath.Base(file)
		}
	}
	if !strings.HasPrefix(file, root+"/") || !files.Exists(file) {
		file = ""
	}
	return rec, root, file, true
}

// Edit is `wvr edit [--full] [PANE]`: open or go to the nvim of the
// focused agent (or PANE's); in a pane that is no agent's, an nvim beside it.
func (e *Env) Edit(args []string) error {
	full, target := false, mux.PaneID("")
	for _, a := range args {
		switch {
		case a == "--full":
			full = true
		case strings.HasPrefix(a, "-"):
			return errors.New("usage: wvr edit [--full] [pane-id]")
		default:
			target, _ = tmux.ParsePane(a)
		}
	}
	release := e.uiLock()
	defer release()
	panes, ok := e.Panes()
	if !ok {
		return errors.New(e.MuxName() + " would not list the panes")
	}
	if target == "" {
		if target = e.focusedPane(); target == "" {
			return errors.New("cannot find the focused pane")
		}
	}
	agentPane := e.agentOf(target)
	var newPane, nv mux.PaneID
	if agentPane == "" {
		cwd := ""
		if p, ok := session.PaneIn(panes, target); ok {
			cwd = p.Cwd
		}
		if st, err := os.Stat(cwd); cwd == "" || err != nil || !st.IsDir() {
			cwd, _ = os.Getwd()
		}
		var err error
		if newPane, err = e.openLeftOf(target, cwd, "nvim: "+filepath.Base(cwd), "nvim"); err != nil {
			return err
		}
	} else {
		e.MarkSeen(agentPane)
		rec, root, file, known := e.agentWhere(agentPane)
		if !known {
			// nvim opens here, and an nvim open keeps the worktree it knew.
			root, _ = os.Getwd()
		}
		unlock := e.editLock(agentPane)
		// Fresh, now that no other press is opening an nvim for this agent.
		panes, _ = e.Panes()
		nv = e.NvimPane(agentPane)
		// Reuse the agent's nvim while its pane is alive, even when nvim does
		// not answer: never a second one.
		if p, ok := session.PaneIn(panes, nv); nv != "" && ok && !p.Exited {
			unlock()
			if known {
				engine.Focus(e.Env, agentPane, rec, root)
			}
			e.focus(nv)
		} else {
			argv := []string{e.Self, "nvim", string(agentPane)}
			if file != "" {
				argv = append(argv, file)
			}
			var err error
			if newPane, err = e.openLeftOf(agentPane, root, "nvim: "+filepath.Base(root), argv...); err != nil {
				unlock()
				return err
			}
			// A thread's window gets its split back.
			if files.Exists(e.PaneFile(agentPane, "thread")) {
				e.threadWidth(agentPane)
				e.settleWidths(agentPane)
			}
			// The new nvim records itself (pane and pid) as it starts; give it
			// a moment, still holding the lock, so a second press finds it.
			// Only if it is slow, record its pane ourselves, without the pid
			// (never over its own record).
			for i := 0; i < 15; i++ {
				if e.NvimPane(agentPane) == newPane {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if e.NvimPane(agentPane) != newPane {
				nf := e.PaneFile(agentPane, "nvim")
				if f, err := os.OpenFile(nf, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644); err == nil {
					_, err := f.WriteString(string(newPane) + "\n\n")
					bestEffort(err)
					f.Close()
				}
			}
			unlock()
		}
	}
	if full {
		p := target
		if agentPane != "" {
			if v := e.NvimPane(agentPane); v != "" {
				p = v
			} else if newPane != "" {
				p = newPane
			}
		} else if newPane != "" {
			p = newPane
		}
		e.Mux().Zoom(p)
	}
	return nil
}

// Nvim is `wvr nvim [--fresh] AGENT [FILE]`: nvim, bound to the agent,
// on the agent's socket. --fresh: the agent was just made (a thread's), so
// an nvim on its socket belongs to a session of the same name that went
// (pane ids start again with each server).
func (e *Env) Nvim(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: wvr nvim <agent-pane-id> [file]")
	}
	agentPane, _ := tmux.ParsePane(args[0])
	file, fresh := "", false
	if len(args) > 1 {
		file = args[1]
	}
	if args[0] == "--fresh" {
		if len(args) < 2 {
			return errors.New("usage: wvr nvim [--fresh] <agent-pane-id> [file]")
		}
		agentPane, _ = tmux.ParsePane(args[1])
		file, fresh = "", true
		if len(args) > 2 {
			file = args[2]
		}
	}
	if err := e.SockDirReady(); err != nil {
		return err
	}
	sock := e.NvimSock(agentPane)
	// Never take the socket of an nvim still serving this agent (answering,
	// or alive but busy): go to it instead, and let this pane close.
	if st, err := os.Stat(sock); !fresh && err == nil && st.Mode()&os.ModeSocket != 0 {
		if nvim.Eval(sock, "1", 2*time.Second) != nvim.Absent {
			if nv := e.NvimPane(agentPane); nv != "" && nv != e.MyPane {
				e.focus(nv)
			}
			return nil
		}
	}
	os.Remove(sock)
	os.Remove(sock + ".stalled")
	sf := e.StateFile(agentPane)
	if e.MyPane != "" {
		os.MkdirAll(e.StateDir, 0o755)
		nf := e.PaneFile(agentPane, "nvim")
		tmp := nf + "." + strconv.Itoa(os.Getpid())
		// exec keeps this pid for nvim.
		if os.WriteFile(tmp, []byte(string(e.MyPane)+"\n"+strconv.Itoa(os.Getpid())+"\n"), 0o644) == nil {
			os.Rename(tmp, nf)
		}
		if files.Exists(sf) && digits(string(e.MyPane)) {
			store.UpdateState(sf, func(st *agent.Record) {
				nv := json.Number(e.MyPane)
				st.NvimPane = &nv
			})
		}
	}
	dir, err := setup.Runtime()
	if err != nil {
		return err
	}
	nvim, err := exec.LookPath("nvim")
	if err != nil {
		return errors.New("nvim is not installed")
	}
	env := append(os.Environ(), "WEAVER_PANE="+string(agentPane), "WEAVER_STATE="+sf, "WEAVER_BIN="+e.Self, "WEAVER_PLUGINS="+setup.PluginsDir())
	lua := strings.ReplaceAll(dir+"/nvim/weaver.lua", " ", `\ `)
	// The ATTENTION dialog for a stale swap file would block this nvim and
	// every remote call: open the startup file read-only instead (weaver.lua
	// reports it and removes this autocmd).
	argv := []string{"nvim", "--listen", sock,
		"--cmd", "augroup weaver_start | autocmd SwapExists * let v:swapchoice = 'o' | let g:weaver_swap = expand('<afile>') | augroup END",
		"-c", "luafile " + lua, "--"}
	if file != "" {
		argv = append(argv, file)
	}
	return fmt.Errorf("nvim: %w", syscall.Exec(nvim, argv, env))
}
