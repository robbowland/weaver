package session

import (
	"cmp"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/jsonx"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/oneline"
	"github.com/robbowland/weaver/internal/process"
	"github.com/robbowland/weaver/internal/store"
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

// PaneIn is pane p among ps, panes as a multiplexer listed them (once for
// all an operation looks up: on Rex each list is a snapshot).
func PaneIn(ps []mux.Pane, p mux.PaneID) (mux.Pane, bool) {
	for _, q := range ps {
		if q.ID == p {
			return q, true
		}
	}
	return mux.Pane{}, false
}

// Row is an agent of the session as the board, the status line and the
// navigation show it: its text with control characters made spaces.
type Row struct {
	State                                                             agent.State
	Pane                                                              mux.PaneID
	Window                                                            mux.WindowID
	Session                                                           string // the Rex session its pane is in, as Rex lists it
	Root, Prompt, Waiting, Title, Pid, Pstart, Sfile, Last, File, Sid string
	WindowName, Agent, Bgtasks, Ttask, Tname                          string
	Since                                                             int64 // epoch s
	Seen                                                              bool
	Bg, Nsub, Turn                                                    int    // background tasks, subagents
	Tdone, Ttotal                                                     int    // its tasks
	Tnum                                                              int    // its thread's number; 0 for none
	Thread                                                            int    // the thread it is of: its own, or a helper's (rollUp); 0 for none
	Via                                                               *Row   // on a thread's agent: its helper that has needed you longest, nil for none (rollUp)
	Review                                                            bool   // its thread is read-only: a review, an investigation
	Exited                                                            bool   // its agent has exited and left its pane (kept: R starts it again, x closes it)
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
		r := Row{Pane: paneOfNum(s.Pane), Window: p.Window, Session: p.Session, State: agent.State(oneline.Line(string(st)))}
		// Where it is: as its agent said, else its thread's worktree (an
		// agent restarted, or brought back, before it has said).
		r.Root = oneline.Line(cmp.Or(s.Root, s.Cwd, t.Worktree))
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
		r.WindowName = oneline.Line(p.WindowName)
		r.Agent = oneline.Line(s.Agent)
		r.Bgtasks = oneline.Line(s.BackgroundTasks)
		r.Doing = oneline.Line(s.Doing)
		if terr == nil {
			r.Ttask, r.Tname, r.Tnum = oneline.Line(t.About()), oneline.Line(t.Name), t.Num
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
		rows = append(rows, Row{Pane: pane, Window: p.Window, Session: p.Session, State: agent.StateStarting,
			Root: oneline.Line(t.Worktree), Since: since, Prompt: oneline.Line(t.Task), Sfile: f,
			WindowName: oneline.Line(p.WindowName), Agent: agent.Claude, Ttask: oneline.Line(t.About()),
			Tname: oneline.Line(t.Name), Tnum: t.Num, Title: oneline.Line(t.Title), Review: t.Mode == agent.ModeReview,
			Exited: p.Exited})
	}
	starts := process.Starts(pids)
	l := e.newLives()
	for _, g := range gones {
		if !trusted {
			continue
		}
		if process.StillRuns(g.pid, g.start, starts) && g.pid != "" && g.st != agent.StateEnded {
			continue
		}
		l.retire(g.f, g.pid)
	}
	out := rows[:0]
	for _, r := range rows {
		if !r.Exited && r.Pid != "" && !process.StillRuns(r.Pid, r.Pstart, starts) {
			// Killed without a SessionEnd: it is not coming back (a thread
			// lost with its session is kept, to resume).
			l.retire(r.Sfile, r.Pid)
			continue
		}
		out = append(out, r)
	}
	rollUp(out)
	return out, now
}

// rollUp gives each helper its thread, and each thread's agent the helper
// of it that has needed you longest (Via), which Shown rolls into it. A
// helper is an agent with no thread whose pane is in a thread's session.
func rollUp(rows []Row) {
	agentOf := map[string]int{} // a thread's session: its agent's row
	for i, r := range rows {
		if r.Tnum > 0 && r.Session != "" {
			agentOf[r.Session], rows[i].Thread = i, r.Tnum
		}
	}
	for i, r := range rows {
		t, ok := agentOf[r.Session]
		if r.Tnum > 0 || !ok {
			continue
		}
		rows[i].Thread = rows[t].Tnum
		if v := rows[t].Via; r.State == agent.StateNeedsYou && !r.Exited && (v == nil || r.Since < v.Since) {
			h := rows[i]
			rows[t].Via = &h
		}
	}
}

// rolled: does r show its helper's wait (Via), not its own?
func (r Row) rolled() bool { return r.Via != nil && (r.State != agent.StateNeedsYou || r.Exited) }

// Waiter is the pane whose wait r shows (Shown): its helper's when that is
// why it needs you, else its own. A jump to r goes there.
func (r Row) Waiter() mux.PaneID {
	if r.rolled() {
		return r.Via.Pane
	}
	return r.Pane
}

// Helper: is r a thread's helper (rollUp)?
func (r Row) Helper() bool { return r.Tnum == 0 && r.Thread > 0 }

// Shown is r as its thread shows wherever threads are listed (the board,
// the views, the counts): a thread's agent with what its helpers need of
// you rolled in. One whose helper needs you (Via) needs you, saying which
// helper waits on what and since when, though its own agent works, is done
// or has exited.
func (r Row) Shown() Row {
	if !r.rolled() {
		return r
	}
	v := *r.Via
	r.State, r.Since, r.Exited = agent.StateNeedsYou, v.Since, false
	r.Waiting = cmp.Or(v.WindowName, v.Called()) + ": " + cmp.Or(v.Waiting, "input")
	return r
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
		if c.SessionID != "" && process.StillRuns(pid, c.ProcStart, nil) {
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

// LostRow is a thread lost with its session, or held: Held (epoch s) and
// Note when you put it on hold.
type LostRow struct {
	File, Sid, Name, Num, Dir, Lost, Skey, Task, Title string
	Held, Note                                         string
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
			Task: oneline.Line(cmp.Or(lo.Thread.Title, lo.Task())), Title: oneline.Line(cmp.Or(lo.Thread.Title, st.Title)),
			Note: oneline.Line(lo.Note)}
		if lo.Held > 0 {
			r.Held = strconv.Itoa(int(lo.Held))
		}
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
// session is kept, anything else forgotten. When whether its session went
// cannot be told (Rex not answering), nothing yet: a later look decides.
// A pass over many files keeps one lives for them all.
func (e *Env) Retire(f, pid string) { e.newLives().retire(f, pid) }

// LostCheck: is the dead agent of state file f a thread lost with its
// session? Not when that cannot be told (lives.lostCheck).
func (e *Env) LostCheck(f string) bool {
	lost, known := e.newLives().lostCheck(f)
	return lost && known
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
	return e.writeLost(skey, st, th, 0, "")
}

// writeLost writes the lost record of a thread (held when you put it on
// hold, with your note).
func (e *Env) writeLost(skey string, st agent.Record, th store.Thread, held int64, note string) bool {
	st.SessionID = cmp.Or(st.SessionID, th.SessionID)
	st.Agent = cmp.Or(st.Agent, agent.Claude)
	id := st.SessionID
	if id == "" {
		id = "thread-" + strconv.FormatInt(th.Created, 10) + "-" + strconv.Itoa(th.Num)
	}
	rec := store.Lost{Lost: jsonx.Int(time.Now().Unix()), SKey: skey, State: st, Thread: th, Held: jsonx.Int(held), Note: note}
	return store.Write(e.LostDir+"/"+skey+"_"+id+".json", rec) == nil
}

// Hold keeps the thread of the agent whose state file is f as held: a lost
// record you made, with note (what is next), which nothing brings back on
// its own. Its files stay, for closing it to forget. False when its agent
// is no thread, or the record could not be written.
func (e *Env) Hold(f, note string) bool {
	defer store.Lock(f)()
	b := strings.TrimSuffix(f, ".json")
	th, err := store.ReadThread(b + ".thread")
	if err != nil {
		return false
	}
	// One whose agent never reported has no state: its record says enough.
	st, _ := store.ReadState(f)
	return e.writeLost(KeyOfFile(f), st, th, time.Now().Unix(), note)
}

// Forget removes the files of the agent whose state file is f; only if
// its pid is still pid when we hold its lock.
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
// review panel binding, last Bash command and opening lock.
func removePaneFiles(b string) {
	for _, x := range []string{".json", ".panel", ".bash", ".thread", ".opening.lock"} {
		os.Remove(b + x)
	}
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
