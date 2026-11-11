package session

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/process"
	"github.com/robbowland/weaver/internal/store"
)

var (
	reGcPid   = regexp.MustCompile(`"pid":([0-9]+)`)
	reGcStart = regexp.MustCompile(`"proc_start":"([^"]*)"`)
	reGcState = regexp.MustCompile(`"state":"([a-z-]+)"`)
	reGcTs    = regexp.MustCompile(`"ts":([0-9]+)`)
)

func sub1(re *regexp.Regexp, l string) string {
	if m := re.FindStringSubmatch(l); m != nil {
		return m[1]
	}
	return ""
}

// GC forgets agents that are gone: a dead agent is retired (its thread
// kept as lost when its session went), a state without a pid is retired an
// hour after its session went, or at once when its session was made since
// (an earlier server's, whose pane number a new pane takes), an ended one
// forgotten an hour after it ended; then old files, the nvims a server that
// went left running, and dead sockets.
// States of other keys (tmux's and zellij's, from before weaver ran on
// Rex only) go the same way as their agents do.
func (e *Env) GC() {
	e.gcAgents()
	e.gcFiles()
}

// gcAgents is GC's agents, their nvims' bindings and their threads: of
// every session, each multiplexer asked once a run (lives).
func (e *Env) gcAgents() {
	now := time.Now().Unix()
	l := e.newLives()
	for _, f := range paneFiles(e.StateDir, "json") {
		line, _ := files.FirstLine(f)
		pid, start, st := sub1(reGcPid, line), sub1(reGcStart, line), agent.State(sub1(reGcState, line))
		ts, _ := strconv.ParseInt(sub1(reGcTs, line), 10, 64)
		skey := KeyOfFile(f)
		switch {
		case pid != "":
			if !process.StillRuns(pid, start, nil) {
				if rec, err := store.ReadState(f); err == nil && l.kept(skey, rec) {
					continue
				}
				l.retire(f, pid)
				continue
			}
		case l.madeAfter(skey, ts), !l.running(skey, ts) && now-ts > 3600:
			l.retire(f, "")
			continue
		}
		if st == agent.StateEnded && now-ts > 3600 {
			e.Forget(f, pid)
		}
	}
	for _, f := range append(paneFiles(e.StateDir, "panel"), paneFiles(e.StateDir, "nvim")...) {
		if files.Exists(strings.TrimSuffix(f, filepath.Ext(f)) + ".json") {
			continue
		}
		b, _ := os.ReadFile(f)
		ls := strings.SplitN(string(b), "\n", 3)
		alive := false
		if len(ls) > 1 {
			if n, ok := atoi(ls[1]); ok {
				alive = process.Alive(n)
			}
		}
		if !alive {
			os.Remove(f)
		}
	}
	// A thread whose agent never reported (no state) is lost with its
	// session, or once its session is one made since it was written (an
	// earlier server's, whose pane number a new pane would take it for):
	// kept, to open again, never dropped.
	for _, f := range paneFiles(e.StateDir, "thread") {
		if files.Exists(strings.TrimSuffix(f, ".thread") + ".json") {
			continue
		}
		skey := KeyOfFile(f)
		fi, err := os.Stat(f)
		if err == nil && (!l.running(skey, fi.ModTime().Unix()) || l.madeAfter(skey, fi.ModTime().Unix())) {
			e.LoseThread(f)
		}
	}
}

// gcFiles is GC's old files, the nvims a server that went left running,
// and dead sockets.
func (e *Env) gcFiles() {
	old := func(dir, pat string, age time.Duration, all bool) {
		ms, _ := filepath.Glob(filepath.Join(dir, pat))
		for _, m := range ms {
			st, err := os.Lstat(m)
			if err != nil || time.Since(st.ModTime()) <= age {
				continue
			}
			if all {
				os.RemoveAll(m)
			} else if st.Mode().IsRegular() {
				os.Remove(m)
			}
		}
	}
	day, month := 24*time.Hour, 31*24*time.Hour
	// Tabs that never finished opening leave their meeting place behind.
	old(e.StateDir+"/new", "*", day, true)
	old(e.StateDir, "form.*", day, false)
	old(e.StateDir+"/fetch", "*", day, false)
	// Codex sessions' panes (engine.codexPane), of sessions long gone.
	old(e.StateDir+"/codex", "*", month, false)
	if st, err := os.Stat(e.StateDir + "/close.log"); err == nil && st.Size() > 100000 {
		os.Remove(e.StateDir + "/close.log")
	}
	old(e.LogDir, "*.jsonl", month, false)
	old(e.LostDir, "*.json", month, false)
	old(e.StateDir+"/turns", "*", month, false)
	// A removed worktree's ignored files (wvr keepIgnored).
	old(e.StateDir+"/trash", "*", month, true)
	e.sweepOrphans()
	e.sweepSockets(day)
}

// sweepOrphans ends weaver's nvims that their server left behind: one
// listening in the socket dir whose parent has gone (launchd, or a
// subreaper, has it now). A thread's nvim is its pane's process, and its
// --embed server that nvim's child; a server that crashes, or is killed,
// can leave either running, a language server or two under it.
func (e *Env) sweepOrphans() {
	out, err := exec.Command("ps", "-Aww", "-o", "pid=,ppid=,command=").Output()
	if err != nil {
		return
	}
	type proc struct{ ppid, argv0 string }
	ps := map[string]proc{}
	var lines [][]string
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) < 3 {
			continue
		}
		ps[f[0]] = proc{f[1], filepath.Base(f[2])}
		if filepath.Base(f[2]) == "nvim" && strings.Contains(l, " --listen "+e.SockDir+"/") {
			lines = append(lines, f)
		}
	}
	var pids []int
	for _, f := range lines {
		parent, ok := ps[f[1]]
		if f[1] != "1" && ok && parent.argv0 != "systemd" {
			continue
		}
		if pid, err := strconv.Atoi(f[0]); err == nil {
			pids = append(pids, pid)
		}
	}
	// An --embed server whose UI went may not hear TERM: what is left after
	// two seconds is killed (its swap file stays, to recover from).
	process.End(pids)
}

// sweepSockets removes sockets nothing listens on any more (a board, a
// view or an nvim that was killed leaves its own) once they are older
// than age.
func (e *Env) sweepSockets(age time.Duration) {
	ms, _ := filepath.Glob(e.SockDir + "/*.sock")
	for _, m := range ms {
		st, err := os.Lstat(m)
		if err != nil || st.Mode()&os.ModeSocket == 0 || time.Since(st.ModTime()) <= age {
			continue
		}
		c, err := net.DialTimeout("unix", m, 200*time.Millisecond)
		if err != nil {
			os.Remove(m)
			continue
		}
		_ = c.Close()
	}
}

// paneFiles are dir's per-pane files with extension ext, of any session:
// <key>_<digits>.<ext>.
func paneFiles(dir, ext string) []string {
	ms, _ := filepath.Glob(dir + "/*_*." + ext)
	var out []string
	for _, m := range ms {
		b := strings.TrimSuffix(filepath.Base(m), "."+ext)
		if i := strings.LastIndexByte(b, '_'); i > 0 && digits(b[i+1:]) {
			out = append(out, m)
		}
	}
	return out
}
