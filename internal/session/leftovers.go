package session

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Leftovers are what an agent started in a weaver pane and left running
// after the pane went: a dev server or a watcher put in the background
// (nohup, &), which launchd adopts and nothing ever ends. A thread closed,
// restarted elsewhere or lost with its server leaves them holding memory
// and ports for good.
//
// One is the tree under a process launchd adopted (parent 1) that was
// started in a pane of a weaver session (WEAVER_TMUX_SESSION, TMUX and
// TMUX_PANE in the environment of a process in it: macOS hides that of its
// own programs, /bin/sh's, so the root may not say) whose pane has gone (or
// whose tmux server has: another pid, or none): of it, each process that
// runs from the worktree it was started in (its program, argv[0], or the
// script argv[1], under that checkout), with all under it (condemned). A
// daemon an agent's tool started on the way (Codex's app server,
// gpg-agent) runs from elsewhere and stays, even when a command of a gone
// thread's runs under it.

// proc is a process as the sweep sees it.
type proc struct {
	pid  int
	argv []string
	env  map[string]string
}

// Leftover is one: the topmost process that runs from the checkout, and
// all under it (of the tree launchd adopted: never a process above it that
// does not, such as a shared daemon one of whose commands it is).
type Leftover struct {
	Pid       int
	Procs     []int
	Pane, Dir string
	Cmd       string
}

// Leftovers are the leftovers of weaver panes that have gone, now.
func (e *Env) Leftovers() []Leftover {
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,uid=").Output()
	if err != nil {
		return nil
	}
	me := os.Getuid()
	kids := map[int][]int{}
	var roots []int
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) != 3 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		uid, err3 := strconv.Atoi(f[2])
		if err1 != nil || err2 != nil || err3 != nil || uid != me {
			continue
		}
		kids[ppid] = append(kids[ppid], pid)
		if ppid == 1 {
			roots = append(roots, pid)
		}
	}
	panes := map[string]map[string]bool{} // a server's TMUX value: its live panes (nil: none)
	var ls []Leftover
	for _, pid := range roots {
		root, ok := readProc(pid)
		if !ok {
			continue
		}
		switch filepath.Base(firstOf(root.argv)) {
		case "tmux", "wvr", "weaver", "nvim":
			continue
		}
		tree := []proc{root}
		for i := 0; i < len(tree); i++ {
			for _, k := range kids[tree[i].pid] {
				if p, ok := readProc(k); ok {
					tree = append(tree, p)
				}
			}
		}
		// Where it was started: the first environment in it that says.
		var env map[string]string
		for _, p := range tree {
			if p.env["WEAVER_TMUX_SESSION"] != "" && p.env["TMUX"] != "" && p.env["TMUX_PANE"] != "" {
				env = p.env
				break
			}
		}
		if env == nil {
			continue
		}
		t := env["TMUX"]
		if _, asked := panes[t]; !asked {
			panes[t] = livePanes(t)
		}
		if panes[t][env["TMUX_PANE"]] {
			continue
		}
		ps := condemned(tree, kids, checkoutOf(env["PWD"]))
		if len(ps) == 0 {
			continue
		}
		l := Leftover{Pid: ps[0].pid, Pane: env["TMUX_PANE"], Dir: env["PWD"], Cmd: strings.Join(ps[0].argv, " ")}
		for _, p := range ps {
			l.Procs = append(l.Procs, p.pid)
		}
		ls = append(ls, l)
	}
	return ls
}

// SweepLeftovers ends the leftovers of weaver panes that have gone (TERM,
// then KILL what is left after 2 s), says each in gc.log, and returns how
// many processes it ended.
func (e *Env) SweepLeftovers() int {
	ls := e.Leftovers()
	if len(ls) == 0 {
		return 0
	}
	log, _ := os.OpenFile(e.StateDir+"/gc.log", os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	var pids []int
	for _, l := range ls {
		if log != nil {
			fmt.Fprintf(log, "%s ended %d (%d processes; pane %s, %s): %s\n", time.Now().Format(time.RFC3339),
				l.Pid, len(l.Procs), l.Pane, l.Dir, l.Cmd)
		}
		for _, pid := range l.Procs {
			if syscall.Kill(pid, syscall.SIGTERM) == nil {
				pids = append(pids, pid)
			}
		}
	}
	if log != nil {
		_ = log.Close()
	}
	n := len(pids)
	for i := 0; i < 20 && len(pids) > 0; i++ {
		time.Sleep(100 * time.Millisecond)
		var left []int
		for _, pid := range pids {
			if syscall.Kill(pid, 0) == nil {
				left = append(left, pid)
			}
		}
		pids = left
	}
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	return n
}

// condemned are the processes of tree (breadth first, as Leftovers walks
// it) that run from under dir, each with all under it; never one above them
// that runs from elsewhere: a daemon a thread's command ran under stays.
func condemned(tree []proc, kids map[int][]int, dir string) []proc {
	parent := map[int]int{}
	for _, p := range tree {
		for _, k := range kids[p.pid] {
			parent[k] = p.pid
		}
	}
	marked := map[int]bool{}
	var out []proc
	for _, p := range tree {
		if marked[parent[p.pid]] || runsFrom([]proc{p}, dir) {
			marked[p.pid] = true
			out = append(out, p)
		}
	}
	return out
}

// livePanes are the panes of the tmux server a TMUX value names
// ("socket,pid,session"), if that server still runs with that pid; nil
// when it does not.
func livePanes(t string) map[string]bool {
	f := strings.Split(t, ",")
	if len(f) < 2 {
		return nil
	}
	pid, err := exec.Command("tmux", "-S", f[0], "display-message", "-p", "#{pid}").Output()
	if err != nil || strings.TrimSpace(string(pid)) != f[1] {
		return nil
	}
	out, err := exec.Command("tmux", "-S", f[0], "list-panes", "-a", "-F", "#{pane_id}").Output()
	if err != nil {
		return nil
	}
	m := map[string]bool{}
	for _, p := range strings.Fields(string(out)) {
		m[p] = true
	}
	return m
}

// checkoutOf is the checkout dir is in (git's top level), else dir itself
// (a worktree removed under it, or none).
func checkoutOf(dir string) string {
	if dir == "" {
		return ""
	}
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output(); err == nil {
		if t := strings.TrimSpace(string(out)); t != "" {
			return t
		}
	}
	return dir
}

// runsFrom: does a process of tree run a program from under dir (its
// argv[0], or the script that is its argv[1]; relative to its PWD)? The
// path as run, not where its links lead: a virtualenv's python is a link to
// an interpreter elsewhere.
func runsFrom(tree []proc, dir string) bool {
	if dir == "" || dir == "/" {
		return false
	}
	dirs := []string{dir}
	if r, err := filepath.EvalSymlinks(dir); err == nil && r != dir {
		dirs = append(dirs, r)
	}
	for _, p := range tree {
		for i, a := range p.argv {
			if i > 1 {
				break
			}
			if a == "" || strings.HasPrefix(a, "-") {
				continue
			}
			if !filepath.IsAbs(a) {
				if !strings.Contains(a, "/") || p.env["PWD"] == "" {
					continue
				}
				a = filepath.Join(p.env["PWD"], a)
			}
			a = filepath.Clean(a)
			for _, d := range dirs {
				if strings.HasPrefix(a, d+"/") {
					return true
				}
			}
		}
	}
	return false
}

// readProc is process pid's arguments and environment (kern.procargs2).
func readProc(pid int) (proc, bool) {
	b, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return proc{}, false
	}
	argv, env, ok := parseProcArgs(b)
	if !ok {
		return proc{}, false
	}
	return proc{pid: pid, argv: argv, env: env}, true
}

// parseProcArgs reads kern.procargs2's layout: argc (int32), the program's
// path, NUL padding, then argc arguments and the environment, each ended by
// a NUL.
func parseProcArgs(b []byte) ([]string, map[string]string, bool) {
	if len(b) < 4 {
		return nil, nil, false
	}
	argc := int(binary.LittleEndian.Uint32(b[:4]))
	b = b[4:]
	i := bytes.IndexByte(b, 0)
	if i < 0 {
		return nil, nil, false
	}
	b = bytes.TrimLeft(b[i:], "\x00")
	var argv []string
	env := map[string]string{}
	for _, s := range strings.Split(string(b), "\x00") {
		if len(argv) < argc {
			argv = append(argv, s)
			continue
		}
		if s == "" {
			break
		}
		if k, v, ok := strings.Cut(s, "="); ok {
			env[k] = v
		}
	}
	return argv, env, len(argv) == argc
}

// Processes is "1 process", "3 processes".
func Processes(n int) string {
	if n == 1 {
		return "1 process"
	}
	return strconv.Itoa(n) + " processes"
}

func firstOf(s []string) string {
	if len(s) > 0 {
		return s[0]
	}
	return ""
}
