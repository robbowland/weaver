package session

import (
	"cmp"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/jsonx"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/oneline"
	"github.com/robbowland/weaver/internal/store"
	"golang.org/x/sys/unix"
)

// Panes lists the session's panes, retried twice: an empty answer is no
// answer.
func (e *Env) Panes() ([]mux.Pane, bool) {
	for i := 0; i < 3; i++ {
		if ps, err := e.Mux().Panes(); err == nil && len(ps) > 0 {
			return ps, true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, false
}

// Row is an agent of the session as the board, the status line and the
// navigation show it: its text with control characters made spaces.
type Row struct {
	State                                                             agent.State
	Pane                                                              mux.PaneID
	Tab                                                               mux.WindowID
	Root, Prompt, Waiting, Title, Pid, Pstart, Sfile, Last, File, Sid string
	Tabname, Agent, Bgtasks, Ttask, Tname                             string
	Since                                                             int64 // epoch s
	Seen                                                              bool
	Bg, Nsub, Turn                                                    int  // background tasks, subagents
	Tdone, Ttotal                                                     int  // its tasks
	Tnum                                                              int  // its thread's number; 0 for none
	Review                                                            bool // its thread is read-only: a review, an investigation
	Exited                                                            bool // its agent has exited and left its pane (kept: R starts it again, x closes it)
	Troots                                                            []string
	Doing                                                             string // the command it runs now
}

// AgentRows are the session's agents: from their
// state files, refined by Claude Code's own session files, and the threads
// whose agent has not reported yet. Agents gone (their pane closed, or their
// process dead without a SessionEnd) are retired on the way. Its second
// value is the time the rows were made (epoch s).
func (e *Env) AgentRows() ([]Row, int64) {
	now := time.Now().Unix()
	jsons := e.SessionFiles("json")
	threads := e.SessionFiles("thread")
	if len(jsons)+len(threads) == 0 {
		return nil, now
	}
	panes, ok := e.Panes()
	if !ok {
		return nil, now
	}
	byID := map[mux.PaneID]*mux.Pane{}
	trusted := e.MyPane == ""
	for i := range panes {
		id := panes[i].ID
		byID[id] = &panes[i]
		if id == e.MyPane {
			trusted = true
		}
	}
	live := e.claudeLive()
	have := map[string]bool{}
	for _, f := range jsons {
		have[f] = true
	}
	var rows []Row
	type gone struct {
		f, pid, start string
		st            agent.State
	}
	var gones []gone
	var pids []int
	for _, f := range jsons {
		s, err := store.ReadState(f)
		if err != nil {
			continue // gone, or corrupt
		}
		t, terr := store.ReadThread(strings.TrimSuffix(f, ".json") + ".thread")
		p := byID[paneOfNum(s.Pane)]
		if p == nil {
			g := gone{f, oneline.Line(string(s.Pid)), oneline.Line(s.ProcStart), s.State}
			gones = append(gones, g)
			if n, err := strconv.Atoi(g.pid); err == nil {
				pids = append(pids, n)
			}
			continue
		}
		// An agent that ended in a pane that runs on (an adopted shell's) is
		// gone from the board; one whose pane it left is shown, exited.
		if s.State == agent.StateEnded && !p.Exited {
			continue
		}
		// Claude Code's own session file says more than the hooks can:
		// a long tool you approved is work, an unreported dialog needs
		// you, quiet after Esc is interrupted.
		var c *claudeSession
		if c0, ok := live[string(s.Pid)]; ok && c0.SessionID == s.SessionID && !p.Exited {
			c = &c0
		}
		cu, cst, cwait := 0.0, "", ""
		if c != nil {
			cu, cst, cwait = math.Floor(c.StatusUpdatedAt/1000), c.Status, c.WaitingFor
		}
		st0 := s.State
		if st0 == "" {
			st0 = agent.StateIdle
		}
		st := st0
		changed := false
		since := jsonx.NumberOr(s.Since, 0)
		switch {
		case c == nil:
		case st0 == agent.StateNeedsYou && cst == "busy" && cu > since:
			st, changed = agent.StateWorking, true
		case st0 != agent.StateNeedsYou && cst == "waiting" && !(float64(s.TS) > cu):
			st, changed = agent.StateNeedsYou, st0 != agent.StateNeedsYou
		case st0 == agent.StateWorking && cst == "idle" && s.Background == 0 && cu > float64(s.TS) && float64(now)-cu > 3:
			st, changed = agent.StateInterrupted, true
		}
		r := Row{Pane: paneOfNum(s.Pane), Tab: p.Tab, State: agent.State(oneline.Line(string(st)))}
		r.Root = oneline.Line(s.Root)
		if r.Root == "" {
			r.Root = oneline.Line(s.Cwd)
		}
		switch {
		case changed:
			r.Since = int64(cu)
		case s.Since != "":
			r.Since = int64(jsonx.NumberOr(s.Since, 0))
		default:
			r.Since = s.TS
		}
		r.Seen = s.Seen
		r.Prompt = oneline.Line(s.Prompt)
		if st == agent.StateNeedsYou && st0 != agent.StateNeedsYou {
			r.Waiting = "input"
			if cwait != "" {
				r.Waiting = oneline.Line(cwait)
			}
		} else {
			r.Waiting = oneline.Line(s.Waiting)
		}
		r.Bg = s.Background
		// The name you gave the thread, else its agent's title.
		r.Title = oneline.Line(cmp.Or(t.Title, s.Title))
		r.Review = t.Mode == agent.ModeReview
		r.Nsub = len(s.Subagents)
		r.Pid = oneline.Line(string(s.Pid))
		r.Pstart = oneline.Line(s.ProcStart)
		r.Sfile = f
		r.Turn = s.Turn
		r.Last = oneline.Line(s.LastMessage)
		d := 0
		for _, x := range s.Tasks {
			if x.Done {
				d++
			}
		}
		r.Tdone, r.Ttotal = d, len(s.Tasks)
		r.File = oneline.Line(filepath.Base(s.File))
		if s.File == "" {
			r.File = ""
		}
		r.Sid = oneline.Line(s.SessionID)
		r.Tabname = oneline.Line(p.TabName)
		r.Agent = oneline.Line(s.Agent)
		r.Bgtasks = oneline.Line(s.BackgroundTasks)
		r.Doing = oneline.Line(s.Doing)
		if terr == nil {
			r.Ttask, r.Tname, r.Tnum = oneline.Line(t.About()), oneline.Line(t.Name), t.Num
		}
		for _, x := range s.TurnRoots {
			r.Troots = append(r.Troots, oneline.Line(x))
		}
		r.Exited = p.Exited
		if n, err := strconv.Atoi(r.Pid); err == nil && !r.Exited {
			pids = append(pids, n)
		}
		rows = append(rows, r)
	}
	// A thread whose agent has not reported yet (or never will: it exited
	// at once, its pane kept).
	for _, f := range threads {
		if have[strings.TrimSuffix(f, ".thread")+".json"] {
			continue
		}
		t, err := store.ReadThread(f)
		if err != nil || t.Pane == nil {
			continue
		}
		pane := paneOfNum(*t.Pane)
		p := byID[pane]
		if p == nil {
			continue
		}
		since := t.Created
		if since == 0 {
			since = now
		}
		rows = append(rows, Row{Pane: pane, Tab: p.Tab, State: agent.StateStarting,
			Root: oneline.Line(t.Worktree), Since: since, Prompt: oneline.Line(t.Task), Sfile: f,
			Tabname: oneline.Line(p.TabName), Agent: oneline.Line(string(t.Agent)), Ttask: oneline.Line(t.About()),
			Tname: oneline.Line(t.Name), Tnum: t.Num, Title: oneline.Line(t.Title), Review: t.Mode == agent.ModeReview,
			Exited: p.Exited})
	}
	starts := ProcStarts(pids)
	for _, g := range gones {
		if !trusted {
			continue
		}
		if AgentAlive(g.pid, g.start, starts) && g.pid != "" && g.st != agent.StateEnded {
			continue
		}
		e.Retire(g.f, g.pid)
	}
	out := rows[:0]
	for _, r := range rows {
		if !r.Exited && r.Pid != "" && !AgentAlive(r.Pid, r.Pstart, starts) {
			// Killed without a SessionEnd: it is not coming back (a thread
			// lost with its session is kept, to resume).
			e.Retire(r.Sfile, r.Pid)
			continue
		}
		out = append(out, r)
	}
	return out, now
}

// claudeSession is Claude Code's own record of a session it runs
// (~/.claude/sessions/<pid>.json): its status, which the hooks cannot
// report.
type claudeSession struct {
	Pid             jsonx.Text `json:"pid"`
	SessionID       string     `json:"sessionId"`
	Status          string     `json:"status"`          // busy, waiting, idle
	StatusUpdatedAt float64    `json:"statusUpdatedAt"` // epoch ms
	WaitingFor      string     `json:"waitingFor"`
	ProcStart       string     `json:"procStart"`
}

// ClaudeRunning are the conversations Claude Code runs on this machine,
// anywhere: the pid of the process in each, by session id.
func (e *Env) ClaudeRunning() map[string]string {
	m := map[string]string{}
	for pid, c := range e.claudeLive() {
		if c.SessionID != "" && AgentAlive(pid, c.ProcStart, nil) {
			m[c.SessionID] = pid
		}
	}
	return m
}

// claudeLive are the Claude Code sessions running on this machine, from
// their ~/.claude/sessions/<pid>.json files (each one line), by pid.
func (e *Env) claudeLive() map[string]claudeSession {
	m := map[string]claudeSession{}
	ents, err := os.ReadDir(e.ClaudeDir + "/sessions")
	if err != nil {
		return m
	}
	for _, d := range ents {
		if !strings.HasSuffix(d.Name(), ".json") {
			continue
		}
		l, err := files.FirstLine(e.ClaudeDir + "/sessions/" + d.Name())
		if err != nil {
			continue
		}
		var c claudeSession
		if jsonx.Decode([]byte(l), &c) != nil || c.Pid == "" {
			continue
		}
		m[string(c.Pid)] = c
	}
	return m
}

// ProcStarts are processes' start times as ps prints them (TZ=UTC, LC_ALL=C:
// "Thu Oct 15 15:38:35 2026"), by pid, from the kernel without ps.
func ProcStarts(pids []int) map[int]string {
	m := map[int]string{}
	for _, p := range pids {
		k, err := unix.SysctlKinfoProc("kern.proc.pid", p)
		if err != nil || k.Proc.P_pid != int32(p) {
			continue
		}
		t := time.Unix(int64(k.Proc.P_starttime.Sec), 0).UTC()
		m[p] = t.Format("Mon Jan _2 15:04:05 2006")
	}
	return m
}

// BootTime is when the machine started (epoch s), from the kernel; 0 when
// it does not say.
func BootTime() int64 {
	tv, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return 0
	}
	return tv.Sec
}

// AgentAlive: is process pid (started at start, as ps prints it, if known)
// still running? kill -0 alone would trust a reused pid.
func AgentAlive(pid, start string, starts map[int]string) bool {
	n, err := strconv.Atoi(pid)
	if err != nil || !digits(pid) {
		return true
	}
	if syscall.Kill(n, 0) != nil {
		return false
	}
	if start == "" {
		return true
	}
	now, ok := starts[n]
	if !ok {
		if s := ProcStarts([]int{n}); s[n] != "" {
			now = s[n]
		} else {
			return true
		}
	}
	return strings.Join(strings.Fields(now), " ") == strings.Join(strings.Fields(start), " ")
}

// LostRow is a thread lost with its session.
type LostRow struct {
	File, Sid, Name, Num, Dir, Lost, Skey, Task, Title string
	lost                                               float64
}

// LostRows are the lost threads, newest first.
func (e *Env) LostRows() []LostRow {
	ents, err := os.ReadDir(e.LostDir)
	if err != nil {
		return nil
	}
	var rs []LostRow
	for _, d := range ents {
		if !strings.HasSuffix(d.Name(), ".json") {
			continue
		}
		f := e.LostDir + "/" + d.Name()
		lo, err := store.ReadLost(f)
		if err != nil {
			continue
		}
		st := lo.State
		num := ""
		if lo.Thread.Num > 0 {
			num = strconv.Itoa(lo.Thread.Num)
		}
		r := LostRow{File: f, Sid: oneline.Line(st.SessionID), Name: oneline.Line(lo.Thread.Name), Num: num,
			Dir: oneline.Line(lo.Dir()), Lost: strconv.Itoa(int(lo.Lost)), Skey: oneline.Line(lo.Key()),
			// Named as you renamed it, else as asked.
			Task: oneline.Line(cmp.Or(lo.Thread.Title, lo.Task())), Title: oneline.Line(cmp.Or(lo.Thread.Title, st.Title))}
		r.lost, _ = strconv.ParseFloat(r.Lost, 64)
		rs = append(rs, r)
	}
	// sort -t US -k6,6nr: newest first; equal times by the whole line.
	line := func(r LostRow) string {
		return strings.Join([]string{r.File, r.Sid, r.Name, r.Num, r.Dir, r.Lost, r.Skey, r.Task, r.Title}, "\x1f")
	}
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].lost != rs[j].lost {
			return rs[i].lost > rs[j].lost
		}
		return line(rs[i]) < line(rs[j])
	})
	return rs
}

// Retire: the agent of state file f (pid) is gone: its thread lost with its
// session is kept, anything else forgotten.
func (e *Env) Retire(f, pid string) {
	if e.LostCheck(f) && e.Lose(f, pid) {
		return
	}
	e.Forget(f, pid)
}

// LostCheck: is the dead agent of state file f a thread lost with its
// session? A thread not ended on purpose whose session has gone since its
// last event (whether or not its conversation is known: a thread lasts
// until you close it).
func (e *Env) LostCheck(f string) bool {
	b := strings.TrimSuffix(f, ".json")
	if !files.Exists(b+".thread") || !files.Exists(f) {
		return false
	}
	s, err := store.ReadState(f)
	if err != nil {
		return false
	}
	switch s.EndReason {
	case "prompt_input_exit", "logout", "clear":
		return false
	}
	return e.serverLost(KeyOfFile(f), s.TS, s.Where)
}

// serverLost: did session key skey's session go since ts (epoch s), its
// agent having run in life w of it (when its state says)? A tmux session:
// when the machine has started since ts (no server outlives that, and one
// started since may have the old one's pid), or no server has that life of
// it any more: no session of its name, or one on another server, or one
// made again on the same server (another id; older states, without one: a
// session made after ts). Any other key (zellij's, from before weaver ran
// on tmux) never is.
func (e *Env) serverLost(skey string, ts int64, w agent.Where) bool {
	if !strings.HasPrefix(skey, "tmux-") {
		return false
	}
	if b := BootTime(); b > 0 && ts < b {
		return true
	}
	ss, _ := e.tmuxSessions()
	for _, s := range ss {
		if s.key != skey {
			continue
		}
		switch {
		case w.Server == 0:
			if s.created <= ts {
				return false
			}
		case s.where.Server != w.Server:
		case w.Session != "":
			if s.where.Session == w.Session {
				return false
			}
		case s.created <= ts:
			return false
		}
	}
	return true
}

var rePid = []byte(`"pid":`)

// pidOf is the pid in a state file's first line ("pid":N); empty without
// one.
func pidOf(f string) string {
	l, _ := files.FirstLine(f)
	i := strings.Index(l, string(rePid))
	if i < 0 {
		return ""
	}
	j := i + len(rePid)
	k := j
	for k < len(l) && l[k] >= '0' && l[k] <= '9' {
		k++
	}
	return l[j:k]
}

// Lose keeps the thread of the agent whose state file is f as lost, then
// removes its files; only if its pid is still pid when we hold its lock.
func (e *Env) Lose(f, pid string) bool {
	if !files.Exists(f) {
		return true
	}
	defer store.Lock(f)()
	if pidOf(f) != pid {
		return true
	}
	b := strings.TrimSuffix(f, ".json")
	st, err := store.ReadState(f)
	if err != nil {
		return false
	}
	th, err := store.ReadThread(b + ".thread")
	if err != nil || !e.keepLost(KeyOfFile(f), st, th) {
		return false
	}
	removePaneFiles(b)
	return true
}

// LoseThread keeps the thread of record f, whose agent never reported (it
// has no state), as lost with its session, then removes its files.
func (e *Env) LoseThread(f string) {
	th, err := store.ReadThread(f)
	if err == nil && !e.keepLost(KeyOfFile(f), agent.Record{}, th) {
		return
	}
	removePaneFiles(strings.TrimSuffix(f, ".thread"))
}

// keepLost keeps thread th, with its agent's state st as it was, as lost
// from session key skey: under its conversation's id (its state's, else its
// record's), else, one never known, its thread's own; resumed, that one
// starts afresh in its directory.
func (e *Env) keepLost(skey string, st agent.Record, th store.Thread) bool {
	st.SessionID = cmp.Or(st.SessionID, th.SessionID)
	st.Agent = cmp.Or(st.Agent, string(th.Agent))
	id := st.SessionID
	if id == "" {
		id = "thread-" + strconv.FormatInt(th.Created, 10) + "-" + strconv.Itoa(th.Num)
	}
	rec := store.Lost{Lost: jsonx.Int(time.Now().Unix()), SKey: skey, State: st, Thread: th}
	return store.Write(e.LostDir+"/"+skey+"_"+id+".json", rec) == nil
}

// Forget removes the files of the agent whose state file is f (its activity
// log stays); only if its pid is still pid when we hold its lock.
func (e *Env) Forget(f, pid string) {
	if !files.Exists(f) {
		return
	}
	defer store.Lock(f)()
	if pidOf(f) != pid {
		return
	}
	removePaneFiles(strings.TrimSuffix(f, ".json"))
}

// removePaneFiles removes the files weaver keeps for the agent in a pane,
// b their path without extension (<skey>_<pane>): its state, thread record,
// nvim binding, turn prompt, last Bash command and opening marker.
func removePaneFiles(b string) {
	for _, x := range []string{".json", ".nvim", ".bash", ".prompt", ".thread"} {
		os.Remove(b + x)
	}
	os.RemoveAll(b + ".opening")
}

// KeyOfFile is the session key of per-pane file f (<skey>_<pane>.<ext>).
func KeyOfFile(f string) string {
	b := filepath.Base(f)
	if i := strings.LastIndexByte(b, '.'); i > 0 {
		b = b[:i]
	}
	if i := strings.LastIndexByte(b, '_'); i > 0 {
		return b[:i]
	}
	return ""
}
