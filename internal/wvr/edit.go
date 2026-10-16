package wvr

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// The agent's nvim: `wvr edit` (Alt v, e on the board) opens it beside the
// agent, or goes to it; `wvr nvim` is what runs in its pane.

// openLeftOf opens a pane running argv left of pane target, in its window;
// the new pane.
func (e *Env) openLeftOf(target, cwd, name string, argv ...string) (string, error) {
	out, err := e.Tmux(append([]string{"split-window", "-h", "-b", "-l", "80%", "-P", "-F", "#{pane_id}", "-t", "%" + target, "-c", cwd}, argv...)...)
	if err != nil {
		return "", fmt.Errorf("tmux split-window failed")
	}
	p := strings.TrimPrefix(strings.TrimSpace(string(out)), "%")
	e.renamePane(p, name)
	return p, nil
}

// threadWidth: back to a thread's split once its nvim reopens beside the
// agent: the agent at $WEAVER_AGENT_WIDTH on the right, while nvim and the
// agent are the window's only panes.
func (e *Env) threadWidth(agent string) {
	out, err := e.Tmux("list-panes", "-t", "%"+agent)
	if err != nil || strings.Count(strings.TrimRight(string(out), "\n"), "\n") != 1 {
		return
	}
	t, err := e.Tmux("display-message", "-p", "-t", "%"+agent, "#{window_width}")
	total, ok := atoi(strings.TrimSpace(string(t)))
	if err != nil || !ok {
		return
	}
	e.Tmux("resize-pane", "-t", "%"+agent, "-x", strconv.Itoa(agentCols(total)))
}

// editLock is one `wvr edit` per agent at a time, so a second Alt v finds
// the nvim the first is opening. A lock older than 5s was left by a killed
// process.
func (e *Env) editLock(agent string) func() {
	d := e.StateDir + "/" + e.SKey + "_" + agent + ".opening"
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
func (e *Env) focusedPane() string {
	out, err := e.Tmux("display-message", "-p", "-t", "="+e.Session+":", "#{pane_id}")
	if err != nil {
		return ""
	}
	p := strings.TrimPrefix(strings.TrimSpace(string(out)), "%")
	if p == e.MyPane {
		return ""
	}
	return p
}

// Edit is `wvr edit [--full] [PANE]`: open or go to the nvim of the
// focused agent (or PANE's); in a pane that is no agent's, an nvim beside it.
func (e *Env) Edit(args []string) int {
	full, target := false, ""
	for _, a := range args {
		switch {
		case a == "--full":
			full = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintln(os.Stderr, "weaver: usage: wvr edit [--full] [pane-id]")
			return 1
		default:
			target = strings.TrimPrefix(strings.TrimPrefix(a, "terminal_"), "%")
		}
	}
	release, _ := e.uiLock(false)
	defer release()
	panes, ok := e.Panes()
	if !ok {
		fmt.Fprintln(os.Stderr, "weaver: tmux would not list the panes")
		return 1
	}
	if target == "" {
		if target = e.focusedPane(); target == "" {
			fmt.Fprintln(os.Stderr, "weaver: cannot find the focused pane")
			return 1
		}
	}
	paneOf := func(ps []Pane, p string) *Pane {
		for i := range ps {
			if strconv.Itoa(ps[i].ID) == p {
				return &ps[i]
			}
		}
		return nil
	}
	agent := e.agentOf(target)
	newPane, nv := "", ""
	if agent == "" {
		cwd := ""
		if p := paneOf(panes, target); p != nil {
			cwd = p.Cwd
		}
		if st, err := os.Stat(cwd); cwd == "" || err != nil || !st.IsDir() {
			cwd, _ = os.Getwd()
		}
		var err error
		if newPane, err = e.openLeftOf(target, cwd, "nvim: "+filepath.Base(cwd), "nvim"); err != nil {
			fmt.Fprintln(os.Stderr, "weaver: "+err.Error())
			return 1
		}
	} else {
		e.MarkSeen(agent)
		h := &hk{Pane: agent, AgentKind: "claude"}
		root, file := "", ""
		if b, err := os.ReadFile(e.StateFile(agent)); err == nil {
			o, _ := readObj(b)
			root = tostring(firstOf(o.get("root", nil), o.get("cwd", nil), ""))
			file = tostring(o.get("file", ""))
			h.Sid, h.State = tostring(o.get("session_id", "")), tostring(o.get("state", ""))
			h.Turn, _ = atoi(tostring(o.get("turn", "0")))
			h.AgentKind = tostring(o.get("agent", "claude"))
		} else if b, err := os.ReadFile(e.StateDir + "/" + e.SKey + "_" + agent + ".thread"); err == nil {
			o, _ := readObj(b)
			root = tostring(o.get("worktree", ""))
			h.AgentKind = tostring(o.get("agent", "claude"))
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
		unlock := e.editLock(agent)
		// Fresh, now that no other press is opening an nvim for this agent.
		panes, _ = e.Panes()
		nv = readNvimPane(e, agent)
		// Reuse the agent's nvim while its pane is alive, even when nvim does
		// not answer: never a second one.
		if p := paneOf(panes, nv); nv != "" && p != nil && !p.Exited {
			unlock()
			now := time.Now()
			h.Now, h.NowMs = now.Unix(), now.UnixMilli()
			e.nvimEditEvent(h, "focus", root, file, "")
			e.focus(nv)
		} else {
			argv := []string{e.Self, "nvim", agent}
			if file != "" {
				argv = append(argv, file)
			}
			var err error
			if newPane, err = e.openLeftOf(agent, root, "nvim: "+filepath.Base(root), argv...); err != nil {
				unlock()
				fmt.Fprintln(os.Stderr, "weaver: "+err.Error())
				return 1
			}
			// A thread's window gets its split back.
			if exists(e.StateDir + "/" + e.SKey + "_" + agent + ".thread") {
				e.threadWidth(agent)
			}
			// The new nvim records itself (pane and pid) as it starts; give it
			// a moment, still holding the lock, so a second press finds it.
			// Only if it is slow, record its pane ourselves, without the pid
			// (never over its own record).
			for i := 0; i < 15; i++ {
				if readNvimPane(e, agent) == newPane {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if readNvimPane(e, agent) != newPane {
				nf := e.StateDir + "/" + e.SKey + "_" + agent + ".nvim"
				if f, err := os.OpenFile(nf, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644); err == nil {
					f.WriteString(newPane + "\n\n")
					f.Close()
				}
			}
			unlock()
		}
	}
	if full {
		p := target
		if agent != "" {
			if v := readNvimPane(e, agent); v != "" {
				p = v
			} else if newPane != "" {
				p = newPane
			}
		} else if newPane != "" {
			p = newPane
		}
		e.Tmux("resize-pane", "-Z", "-t", "%"+p)
	}
	return 0
}

// Nvim is `wvr nvim [--fresh] AGENT [FILE]`: nvim, bound to the agent,
// on the agent's socket. --fresh: the agent was just made (a thread's), so
// an nvim on its socket belongs to a session of the same name that went
// (pane ids start again with each server).
func (e *Env) Nvim(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "weaver: usage: wvr nvim <agent-pane-id> [file]")
		return 1
	}
	agent, file, fresh := strings.TrimPrefix(args[0], "terminal_"), "", false
	if len(args) > 1 {
		file = args[1]
	}
	if args[0] == "--fresh" {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "weaver: usage: wvr nvim [--fresh] <agent-pane-id> [file]")
			return 1
		}
		agent, file, fresh = strings.TrimPrefix(args[1], "terminal_"), "", true
		if len(args) > 2 {
			file = args[2]
		}
	}
	if err := os.MkdirAll(e.SockDir, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "weaver: cannot create "+e.SockDir)
		return 1
	}
	if st, err := os.Stat(e.SockDir); err != nil || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		fmt.Fprintln(os.Stderr, "weaver: "+e.SockDir+" is not owned by you")
		return 1
	}
	os.Chmod(e.SockDir, 0o700)
	sock := e.SockDir + "/" + e.SKey + "_" + agent + ".sock"
	// Never take the socket of an nvim still serving this agent (answering,
	// or alive but busy): go to it instead, and let this pane close.
	if st, err := os.Stat(sock); !fresh && err == nil && st.Mode()&os.ModeSocket != 0 {
		if rc := nvimEval(sock, "1", 2*time.Second); rc == 0 || rc >= 128 {
			if nv := readNvimPane(e, agent); nv != "" && nv != e.MyPane {
				e.focus(nv)
			}
			return 0
		}
	}
	os.Remove(sock)
	os.Remove(sock + ".stalled")
	sf := e.StateFile(agent)
	if e.MyPane != "" {
		os.MkdirAll(e.StateDir, 0o755)
		nf := e.StateDir + "/" + e.SKey + "_" + agent + ".nvim"
		tmp := nf + "." + strconv.Itoa(os.Getpid())
		// exec keeps this pid for nvim.
		if os.WriteFile(tmp, []byte(e.MyPane+"\n"+strconv.Itoa(os.Getpid())+"\n"), 0o644) == nil {
			os.Rename(tmp, nf)
		}
		if exists(sf) {
			lk := lock(sf)
			b, _ := os.ReadFile(sf)
			if o, ok := parseOobj(b); ok {
				v := jqStr(e.MyPane)
				if digits(e.MyPane) {
					v = e.MyPane
				}
				if os.WriteFile(lk, []byte(o.set("nvim_pane", v).String()+"\n"), 0o644) == nil {
					os.Rename(lk, sf)
				} else {
					os.Remove(lk)
				}
			} else {
				os.Remove(lk)
			}
		}
	}
	dir, err := Runtime()
	if err != nil {
		fmt.Fprintln(os.Stderr, "weaver: "+err.Error())
		return 1
	}
	nvim, err := exec.LookPath("nvim")
	if err != nil {
		fmt.Fprintln(os.Stderr, "weaver: nvim is not installed")
		return 1
	}
	env := append(os.Environ(), "WEAVER_PANE="+agent, "WEAVER_STATE="+sf, "WEAVER_BIN="+e.Self, "WEAVER_PLUGINS="+PluginsDir())
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
	syscall.Exec(nvim, argv, env)
	return 1
}
