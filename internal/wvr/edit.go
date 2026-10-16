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
	"github.com/robbowland/weaver/internal/setup"
	"github.com/robbowland/weaver/internal/tmux"
)

// The agent's nvim: `wvr edit` (Alt v, e on the board) opens it beside the
// agent, or goes to it; `wvr nvim` is what runs in its pane.

// openLeftOf opens a pane running argv left of pane target, in its window;
// the new pane.
func (e *Env) openLeftOf(target tmux.PaneID, cwd, name string, argv ...string) (tmux.PaneID, error) {
	out, err := e.Tmux(append([]string{"split-window", "-h", "-b", "-l", "80%", "-P", "-F", "#{pane_id}", "-t", target.Target(), "-c", cwd}, argv...)...)
	if err != nil {
		return "", fmt.Errorf("tmux split-window failed")
	}
	p, _ := tmux.ParsePane(string(out))
	e.renamePane(p, name)
	return p, nil
}

// threadWidth: back to a thread's split once its nvim reopens beside the
// agent: the agent at $WEAVER_AGENT_WIDTH on the right, while nvim and the
// agent are the window's only panes.
func (e *Env) threadWidth(agent tmux.PaneID) {
	out, err := e.Tmux("list-panes", "-t", agent.Target())
	if err != nil || strings.Count(strings.TrimRight(string(out), "\n"), "\n") != 1 {
		return
	}
	t, err := e.Tmux("display-message", "-p", "-t", agent.Target(), "#{window_width}")
	total, ok := atoi(strings.TrimSpace(string(t)))
	if err != nil || !ok {
		return
	}
	e.Tmux("resize-pane", "-t", agent.Target(), "-x", strconv.Itoa(agentCols(total)))
}

// editLock is one `wvr edit` per agent at a time, so a second Alt v finds
// the nvim the first is opening. A lock older than 5s was left by a killed
// process.
func (e *Env) editLock(agent tmux.PaneID) func() {
	d := e.paneFile(agent, "opening")
	os.MkdirAll(e.StateDir, 0o755)
	for i := 0; i < 60; i++ {
		if os.Mkdir(d, 0o755) == nil {
			break
		}
		now := time.Now().Unix()
		t := now
		if l, err := firstLine(d + "/at"); err == nil {
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
func (e *Env) focusedPane() tmux.PaneID {
	out, err := e.Tmux("display-message", "-p", "-t", "="+e.Session+":", "#{pane_id}")
	if err != nil {
		return ""
	}
	p, _ := tmux.ParsePane(string(out))
	if p == e.MyPane {
		return ""
	}
	return p
}

// Edit is `wvr edit [--full] [PANE]`: open or go to the nvim of the
// focused agent (or PANE's); in a pane that is no agent's, an nvim beside it.
func (e *Env) Edit(args []string) error {
	full, target := false, tmux.PaneID("")
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
		return errors.New("tmux would not list the panes")
	}
	if target == "" {
		if target = e.focusedPane(); target == "" {
			return errors.New("cannot find the focused pane")
		}
	}
	paneOf := func(ps []tmux.Pane, p tmux.PaneID) *tmux.Pane {
		for i := range ps {
			if ps[i].ID == p {
				return &ps[i]
			}
		}
		return nil
	}
	agentPane := e.agentOf(target)
	var newPane, nv tmux.PaneID
	if agentPane == "" {
		cwd := ""
		if p := paneOf(panes, target); p != nil {
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
		h := &hk{Pane: agentPane, AgentKind: "claude"}
		root, file := "", ""
		if s, err := readState(e.StateFile(agentPane)); err == nil {
			root = s.Root
			if root == "" {
				root = s.Cwd
			}
			file = s.File
			h.Sid, h.State, h.Turn = s.SessionID, s.State, s.Turn
			h.AgentKind = s.Agent
			if h.AgentKind == "" {
				h.AgentKind = string(agent.Claude)
			}
		} else if t, err := readThread(e.threadFile(agentPane)); err == nil {
			root = t.Worktree
			h.AgentKind = string(t.Agent)
			if h.AgentKind == "" {
				h.AgentKind = "claude"
			}
		}
		if st, err := os.Stat(root); root == "" || err != nil || !st.IsDir() {
			root, _ = os.Getwd()
		}
		// git gives the root as a real path; the agent may have used a
		// symlinked one (/tmp vs /private/tmp), so compare real paths.
		if file != "" && exists(file) {
			if d, err := filepath.EvalSymlinks(filepath.Dir(file)); err == nil {
				file = d + "/" + filepath.Base(file)
			}
		}
		if !strings.HasPrefix(file, root+"/") || !exists(file) {
			file = ""
		}
		unlock := e.editLock(agentPane)
		// Fresh, now that no other press is opening an nvim for this agent.
		panes, _ = e.Panes()
		nv = readNvimPane(e, agentPane)
		// Reuse the agent's nvim while its pane is alive, even when nvim does
		// not answer: never a second one.
		if p := paneOf(panes, nv); nv != "" && p != nil && !p.Exited {
			unlock()
			now := time.Now()
			h.Now, h.NowMs = now.Unix(), now.UnixMilli()
			e.nvimEditEvent(h, "focus", root, file, "")
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
			if exists(e.paneFile(agentPane, "thread")) {
				e.threadWidth(agentPane)
			}
			// The new nvim records itself (pane and pid) as it starts; give it
			// a moment, still holding the lock, so a second press finds it.
			// Only if it is slow, record its pane ourselves, without the pid
			// (never over its own record).
			for i := 0; i < 15; i++ {
				if readNvimPane(e, agentPane) == newPane {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if readNvimPane(e, agentPane) != newPane {
				nf := e.paneFile(agentPane, "nvim")
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
			if v := readNvimPane(e, agentPane); v != "" {
				p = v
			} else if newPane != "" {
				p = newPane
			}
		} else if newPane != "" {
			p = newPane
		}
		e.Tmux("resize-pane", "-Z", "-t", p.Target())
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
	if err := os.MkdirAll(e.SockDir, 0o700); err != nil {
		return errors.New("cannot create " + e.SockDir)
	}
	if st, err := os.Stat(e.SockDir); err != nil || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return errors.New(e.SockDir + " is not owned by you")
	}
	os.Chmod(e.SockDir, 0o700)
	sock := e.nvimSock(agentPane)
	// Never take the socket of an nvim still serving this agent (answering,
	// or alive but busy): go to it instead, and let this pane close.
	if st, err := os.Stat(sock); !fresh && err == nil && st.Mode()&os.ModeSocket != 0 {
		if rc := nvimEval(sock, "1", 2*time.Second); rc == 0 || rc >= 128 {
			if nv := readNvimPane(e, agentPane); nv != "" && nv != e.MyPane {
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
		nf := e.paneFile(agentPane, "nvim")
		tmp := nf + "." + strconv.Itoa(os.Getpid())
		// exec keeps this pid for nvim.
		if os.WriteFile(tmp, []byte(string(e.MyPane)+"\n"+strconv.Itoa(os.Getpid())+"\n"), 0o644) == nil {
			os.Rename(tmp, nf)
		}
		if exists(sf) && digits(string(e.MyPane)) {
			updateState(sf, func(st *agent.Record) {
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
