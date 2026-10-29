package session

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/rex"
	"github.com/robbowland/weaver/internal/store"
	"github.com/robbowland/weaver/internal/tmux"
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

// tmuxSession is a running tmux session: its key (tmux-<name>), when it
// was made (epoch s), and the life it is (its server, its id there).
type tmuxSession struct {
	key     string
	created int64
	where   agent.Where
}

// tmuxSessions are the running tmux sessions on weaver's server and the one
// we run in; ok false when neither answers.
func (e *Env) tmuxSessions() (ss []tmuxSession, ok bool) {
	add := func(args ...string) {
		out, err := RunTmux(args...)
		if err != nil {
			return
		}
		ok = true
		for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			// The name last: it may have spaces.
			f := strings.SplitN(l, " ", 4)
			if len(f) != 4 || f[3] == "" {
				continue
			}
			made, _ := strconv.ParseInt(f[0], 10, 64)
			srv, _ := strconv.Atoi(f[1])
			ss = append(ss, tmuxSession{key: "tmux-" + strings.ReplaceAll(f[3], "/", "_"), created: made,
				where: agent.Where{Server: srv, Session: f[2]}})
		}
	}
	const format = "#{session_created} #{pid} #{session_id} #S"
	add(append(append([]string{}, e.TSock...), "list-sessions", "-F", format)...)
	if e.InTmux {
		add("list-sessions", "-F", format)
	}
	return ss, ok
}

// rexShows: is pane p one of Rex session s's? (Not known: it is.)
func rexShows(s rex.Session, p mux.PaneID) bool {
	ps, err := s.Panes()
	return err != nil || slices.ContainsFunc(ps, func(x mux.Pane) bool { return x.ID == p })
}

// paneLife is a pane of a life of a session: its key, its id, and which
// life (server, session id).
type paneLife struct {
	skey  string
	pane  mux.PaneID
	where agent.Where
}

// panesShown are the panes of the running sessions, on weaver's server and
// the one we run in.
func (e *Env) panesShown() map[paneLife]bool {
	m := map[paneLife]bool{}
	add := func(args ...string) {
		out, err := RunTmux(args...)
		if err != nil {
			return
		}
		for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			f := strings.SplitN(l, " ", 4)
			if len(f) != 4 {
				continue
			}
			srv, _ := strconv.Atoi(f[1])
			if p, ok := tmux.ParsePane(f[0]); ok {
				m[paneLife{KeyOf(f[3]), p, agent.Where{Server: srv, Session: f[2]}}] = true
			}
		}
	}
	const format = "#{pane_id} #{pid} #{session_id} #S"
	add(append(append([]string{}, e.TSock...), "list-panes", "-a", "-F", format)...)
	if e.InTmux {
		add("list-panes", "-a", "-F", format)
	}
	return m
}

// GC forgets agents that are gone: a dead agent is retired (its thread
// kept as lost when its session went), a state without a pid is retired an
// hour after its session went, or at once when its session was made since
// (an earlier server's, whose pane number a new pane takes), an ended one
// forgotten an hour after it ended; then old files, the nvims a server that
// went left running, and dead sockets.
// States of other keys (zellij's, from before weaver ran on tmux only) go
// the same way as their agents do.
func (e *Env) GC() {
	now := time.Now().Unix()
	var sessions []tmuxSession
	answered, asked := false, false
	ask := func() {
		if !asked {
			sessions, answered = e.tmuxSessions()
			asked = true
		}
	}
	// A Rex session's (rex-<name>), as Rex says, once a run.
	type rexAnswer struct {
		s                 rex.Session
		running, answered bool
	}
	rexes := map[string]rexAnswer{}
	onRex := func(skey string) (rexAnswer, bool) {
		name, ok := strings.CutPrefix(skey, "rex-")
		if !ok {
			return rexAnswer{}, false
		}
		a, seen := rexes[name]
		if !seen {
			a.s, a.running, a.answered = rex.Alive(e.RexDir(), name)
			rexes[name] = a
		}
		return a, true
	}
	running := func(skey string) bool {
		if a, ok := onRex(skey); ok {
			return !a.answered || a.running
		}
		ask()
		// Nobody answered: keep everything.
		return !answered || slices.ContainsFunc(sessions, func(s tmuxSession) bool { return s.key == skey })
	}
	// Is skey's session one made after ts: another life of it, the one a
	// file of ts knew having gone? (A name on two servers: the earlier.)
	madeAfter := func(skey string, ts int64) bool {
		if a, ok := onRex(skey); ok {
			made, known := a.s.Made()
			return a.running && known && ts > 0 && made > ts
		}
		ask()
		made := int64(0)
		for _, s := range sessions {
			if s.key == skey && (made == 0 || s.created < made) {
				made = s.created
			}
		}
		return made > 0 && ts > 0 && made > ts
	}
	var shown map[paneLife]bool
	for _, f := range paneFiles(e.StateDir, "json") {
		l, _ := files.FirstLine(f)
		pid, start, st := sub1(reGcPid, l), sub1(reGcStart, l), agent.State(sub1(reGcState, l))
		ts, _ := strconv.ParseInt(sub1(reGcTs, l), 10, 64)
		if pid != "" {
			if !AgentAlive(pid, start, nil) {
				// Its pane kept (it exited, its thread to be started again
				// or closed): the board's to show, not to retire.
				if shown == nil {
					shown = e.panesShown()
				}
				st, err := store.ReadState(f)
				if err == nil && shown[paneLife{KeyOfFile(f), paneOfNum(st.Pane), st.Where}] {
					continue
				}
				if a, ok := onRex(KeyOfFile(f)); err == nil && ok && (!a.answered || a.running && a.s.ID == st.Where.Session && rexShows(a.s, paneOfNum(st.Pane))) {
					continue
				}
				e.Retire(f, pid)
				continue
			}
		} else {
			skey := KeyOfFile(f)
			if madeAfter(skey, ts) {
				e.Retire(f, "")
				continue
			}
			if !running(skey) && now-ts > 3600 {
				e.Retire(f, "")
				continue
			}
		}
		if st == agent.StateEnded && now-ts > 3600 {
			e.Forget(f, pid)
		}
	}
	for _, f := range paneFiles(e.StateDir, "nvim") {
		if files.Exists(strings.TrimSuffix(f, ".nvim") + ".json") {
			continue
		}
		b, _ := os.ReadFile(f)
		ls := strings.SplitN(string(b), "\n", 3)
		alive := false
		if len(ls) > 1 {
			if n, ok := atoi(ls[1]); ok {
				alive = AgentAlive(ls[1], "", nil) && n > 0
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
		if err == nil && (!running(skey) || madeAfter(skey, fi.ModTime().Unix())) {
			e.LoseThread(f)
		}
	}
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
	if n := e.SweepLeftovers(); n > 0 && e.Session != "" {
		e.Mux().SayToSession(4000, "weaver: ended "+Processes(n)+" left running by threads that have gone (gc.log)")
	}
	e.sweepSockets(day)
}

// sweepOrphans ends weaver's nvims that their tmux server left behind: one
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
		if pid, err := strconv.Atoi(f[0]); err == nil && pid > 1 && syscall.Kill(pid, syscall.SIGTERM) == nil {
			pids = append(pids, pid)
		}
	}
	// An --embed server whose UI went may not hear TERM: what is left after
	// two seconds is killed (its swap file stays, to recover from).
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
