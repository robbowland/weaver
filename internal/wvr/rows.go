package wvr

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Pane is one pane of the session.
type Pane struct {
	ID, Tab  int
	TabName  string
	TabIndex int
	Exited   bool
	Cmd      string
	W, H     int
	X, Y     int
	Title    string
	Cwd      string
	Focused  bool
}

// Panes lists the session's panes (tmux list-panes -s), retried twice: an
// empty answer is no answer.
func (e *Env) Panes() ([]Pane, bool) {
	const us = "\x1f"
	f := strings.Join([]string{"#{pane_id}", "#{window_id}", "#{window_name}", "#{window_index}", "#{pane_dead}",
		"#{pane_start_command}", "#{pane_width}", "#{pane_height}", "#{pane_left}", "#{pane_top}", "#{pane_title}",
		"#{pane_current_path}", "#{pane_active}"}, us)
	for i := 0; i < 3; i++ {
		out, err := e.Tmux("list-panes", "-s", "-t", "="+e.Session, "-F", f)
		if err == nil {
			var ps []Pane
			for _, l := range strings.Split(string(out), "\n") {
				x := strings.Split(l, us)
				if len(x) != 13 {
					continue
				}
				p := Pane{TabName: x[2], Exited: x[4] == "1", Cmd: x[5], Title: x[10], Cwd: x[11], Focused: x[12] == "1"}
				n := []*int{&p.ID, &p.Tab, &p.TabIndex, &p.W, &p.H, &p.X, &p.Y}
				s := []string{strings.TrimPrefix(x[0], "%"), strings.TrimPrefix(x[1], "@"), x[3], x[6], x[7], x[8], x[9]}
				ok := true
				for j := range n {
					v, err := strconv.Atoi(s[j])
					if err != nil || !digits(s[j]) {
						ok = false
						break
					}
					*n[j] = v
				}
				if ok {
					ps = append(ps, p)
				}
			}
			if len(ps) > 0 {
				return ps, true
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, false
}

// Row is an agent of the session: each field as a string (numbers in
// decimal, control characters made spaces).
type Row struct {
	State                                                                              State
	Pane, Tab, Root, Since, Seen, Prompt, Waiting, Bg, Title, Nsub, Pid, Pstart, Sfile string
	Turn, Last, Tdone, Ttotal, File, Sid, Tabname, Agent, Bgtasks, Ttask, Tname, Tnum  string
	Troots                                                                             []string
	Doing                                                                              string // the command it runs now
}

// clean makes each run of control characters one space (jq's
// gsub("[\u0000-\u001f]+"; " ")).
func clean(s string) string {
	if !hasCtl(s) {
		return s
	}
	var b strings.Builder
	ctl := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 {
			if !ctl {
				b.WriteByte(' ')
			}
			ctl = true
			continue
		}
		ctl = false
		b.WriteByte(c)
	}
	return b.String()
}

func hasCtl(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 {
			return true
		}
	}
	return false
}

// numberOr is n as a float, or d when it is not a number.
func numberOr(n json.Number, d float64) float64 {
	if f, err := n.Float64(); err == nil {
		return f
	}
	return d
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
	byID := map[string]*Pane{}
	trusted := e.MyPane == ""
	for i := range panes {
		id := strconv.Itoa(panes[i].ID)
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
		st            State
	}
	var gones []gone
	var pids []int
	for _, f := range jsons {
		s, err := readState(f)
		if err != nil {
			continue // gone, or corrupt
		}
		t, terr := readThread(strings.TrimSuffix(f, ".json") + ".thread")
		p := byID[strconv.Itoa(int(s.Pane))]
		if p == nil {
			g := gone{f, clean(string(s.Pid)), clean(s.ProcStart), s.State}
			gones = append(gones, g)
			if n, err := strconv.Atoi(g.pid); err == nil {
				pids = append(pids, n)
			}
			continue
		}
		if p.Exited || s.State == StateEnded {
			continue
		}
		// Claude Code's own session file says more than the hooks can:
		// a long tool you approved is work, an unreported dialog needs
		// you, quiet after Esc is interrupted.
		var c *claudeSession
		if c0, ok := live[string(s.Pid)]; ok && c0.SessionID == s.SessionID {
			c = &c0
		}
		cu, cst, cwait := 0.0, "", ""
		if c != nil {
			cu, cst, cwait = math.Floor(c.StatusUpdatedAt/1000), c.Status, c.WaitingFor
		}
		st0 := s.State
		if st0 == "" {
			st0 = StateIdle
		}
		st := st0
		changed := false
		since := numberOr(s.Since, 0)
		switch {
		case c == nil:
		case st0 == StateNeedsYou && cst == "busy" && cu > since:
			st, changed = StateWorking, true
		case st0 != StateNeedsYou && cst == "waiting" && !(float64(s.TS) > cu):
			st, changed = StateNeedsYou, st0 != StateNeedsYou
		case st0 == StateWorking && cst == "idle" && s.Background == 0 && cu > float64(s.TS) && float64(now)-cu > 3:
			st, changed = StateInterrupted, true
		}
		r := Row{Pane: strconv.Itoa(int(s.Pane)), Tab: strconv.Itoa(p.Tab), State: State(clean(string(st)))}
		r.Root = clean(s.Root)
		if r.Root == "" {
			r.Root = clean(s.Cwd)
		}
		switch {
		case changed:
			r.Since = strconv.FormatFloat(cu, 'f', -1, 64)
		case s.Since != "":
			r.Since = s.Since.String()
		default:
			r.Since = strconv.FormatInt(s.TS, 10)
		}
		r.Seen = strconv.FormatBool(s.Seen)
		r.Prompt = clean(s.Prompt)
		if st == StateNeedsYou && st0 != StateNeedsYou {
			r.Waiting = "input"
			if cwait != "" {
				r.Waiting = clean(cwait)
			}
		} else {
			r.Waiting = clean(s.Waiting)
		}
		r.Bg = strconv.Itoa(s.Background)
		r.Title = clean(s.Title)
		r.Nsub = strconv.Itoa(len(s.Subagents))
		r.Pid = clean(string(s.Pid))
		r.Pstart = clean(s.ProcStart)
		r.Sfile = f
		r.Turn = strconv.Itoa(s.Turn)
		r.Last = clean(s.LastMessage)
		d := 0
		for _, x := range s.Tasks {
			if x.Done {
				d++
			}
		}
		r.Tdone, r.Ttotal = strconv.Itoa(d), strconv.Itoa(len(s.Tasks))
		r.File = clean(filepath.Base(s.File))
		if s.File == "" {
			r.File = ""
		}
		r.Sid = clean(s.SessionID)
		r.Tabname = clean(p.TabName)
		r.Agent = clean(s.Agent)
		r.Bgtasks = clean(s.BackgroundTasks)
		r.Doing = clean(s.Doing)
		if terr == nil {
			r.Ttask, r.Tname, r.Tnum = clean(t.Task), clean(t.Name), strconv.Itoa(t.Num)
		}
		for _, x := range s.TurnRoots {
			r.Troots = append(r.Troots, clean(x))
		}
		if n, err := strconv.Atoi(r.Pid); err == nil {
			pids = append(pids, n)
		}
		rows = append(rows, r)
	}
	// A thread whose agent has not reported yet (its pane still open).
	for _, f := range threads {
		if have[strings.TrimSuffix(f, ".thread")+".json"] {
			continue
		}
		t, err := readThread(f)
		if err != nil || t.Pane == nil {
			continue
		}
		pane := strconv.Itoa(int(*t.Pane))
		p := byID[pane]
		if p == nil || p.Exited {
			continue
		}
		since := t.Created
		if since == 0 {
			since = now
		}
		rows = append(rows, Row{Pane: pane, Tab: strconv.Itoa(p.Tab), State: StateStarting,
			Root: clean(t.Worktree), Since: strconv.FormatInt(since, 10),
			Seen: "false", Prompt: clean(t.Task), Bg: "0", Nsub: "0", Sfile: f, Turn: "0", Tdone: "0", Ttotal: "0",
			Tabname: clean(p.TabName), Agent: clean(string(t.Agent)), Ttask: clean(t.Task),
			Tname: clean(t.Name), Tnum: strconv.Itoa(t.Num)})
	}
	starts := procStarts(pids)
	for _, g := range gones {
		if !trusted {
			continue
		}
		if agentAlive(g.pid, g.start, starts) && g.pid != "" && g.st != StateEnded {
			continue
		}
		e.retire(g.f, g.pid)
	}
	out := rows[:0]
	for _, r := range rows {
		if r.Pid != "" && !agentAlive(r.Pid, r.Pstart, starts) {
			// Killed without a SessionEnd: it is not coming back (a thread
			// lost with its session is kept, to resume).
			e.retire(r.Sfile, r.Pid)
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
	Pid             jsonText `json:"pid"`
	SessionID       string   `json:"sessionId"`
	Status          string   `json:"status"`          // busy, waiting, idle
	StatusUpdatedAt float64  `json:"statusUpdatedAt"` // epoch ms
	WaitingFor      string   `json:"waitingFor"`
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
		l, err := firstLine(e.ClaudeDir + "/sessions/" + d.Name())
		if err != nil {
			continue
		}
		var c claudeSession
		if decodeRecord([]byte(l), &c) != nil || c.Pid == "" {
			continue
		}
		m[string(c.Pid)] = c
	}
	return m
}

// procStarts are processes' start times as ps prints them (TZ=UTC, LC_ALL=C:
// "Thu Oct 15 15:38:35 2026"), by pid, from the kernel without ps.
func procStarts(pids []int) map[int]string {
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

// agentAlive: is process pid (started at start, as ps prints it, if known)
// still running? kill -0 alone would trust a reused pid.
func agentAlive(pid, start string, starts map[int]string) bool {
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
		if s := procStarts([]int{n}); s[n] != "" {
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
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var lo Lost
		if json.Unmarshal(firstJSON(b), &lo) != nil {
			continue
		}
		st := lo.State
		num := ""
		if lo.Thread.Num > 0 {
			num = strconv.Itoa(lo.Thread.Num)
		}
		r := LostRow{File: f, Sid: clean(st.SessionID), Name: clean(lo.Thread.Name), Num: num,
			Dir: clean(lo.Dir()), Lost: strconv.Itoa(int(lo.Lost)), Skey: clean(lo.Key()),
			Task: clean(lo.Task()), Title: clean(st.Title)}
		r.lost, _ = strconv.ParseFloat(r.Lost, 64)
		rs = append(rs, r)
	}
	// sort -t US -k6,6nr: newest first; equal times by the whole line.
	line := func(r LostRow) string {
		return strings.Join([]string{r.File, r.Sid, r.Name, r.Num, r.Dir, r.Lost, r.Skey, r.Task, r.Title}, "\x1f")
	}
	sortStable(rs, func(a, b LostRow) bool {
		if a.lost != b.lost {
			return a.lost > b.lost
		}
		return line(a) < line(b)
	})
	return rs
}

// retire: the agent of state file f (pid) is gone: its thread lost with its
// session is kept, anything else forgotten.
func (e *Env) retire(f, pid string) {
	if e.lostCheck(f) && e.lose(f, pid) {
		return
	}
	e.forget(f, pid)
}

// lostCheck: is the dead agent of state file f a thread lost with its
// session? Only a thread with a session to resume, not ended on purpose,
// whose session has gone since its last event.
func (e *Env) lostCheck(f string) bool {
	b := strings.TrimSuffix(f, ".json")
	if !exists(b+".thread") || !exists(f) {
		return false
	}
	s, err := readState(f)
	if err != nil || s.SessionID == "" {
		return false
	}
	switch s.EndReason {
	case "prompt_input_exit", "logout", "clear":
		return false
	}
	srv := ""
	if s.Server != 0 {
		srv = strconv.Itoa(s.Server)
	}
	base := filepath.Base(b)
	return e.serverLost(base[:strings.LastIndexByte(base, '_')], float64(s.TS), srv)
}

// serverLost: did session key skey's session go since ts (epoch s), the
// agent having run on tmux server srv (its pid, when its state says)? A tmux
// session: when its server no longer has it, or has one of its name made
// since: another server, or (older states) a session made after ts. Any
// other key (zellij's, from before weaver ran on tmux) never is.
func (e *Env) serverLost(skey string, ts float64, srv string) bool {
	if !strings.HasPrefix(skey, "tmux-") {
		return false
	}
	out, _ := e.Tmux("list-sessions", "-F", "#S #{session_created} #{pid}")
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) < 2 || "tmux-"+strings.ReplaceAll(f[0], "/", "_") != skey {
			continue
		}
		if srv != "" && len(f) > 2 {
			return f[2] != srv
		}
		n, _ := strconv.ParseFloat(f[1], 64)
		return n > ts
	}
	return true
}

// lock serialises read-modify-write of state file sf:
// sf.lock created exclusively holding our pid; a lock whose owner is gone is
// removed; after ~2s, a private file instead. Its path.
func lock(sf string) string {
	l := sf + ".lock"
	me := strconv.Itoa(os.Getpid())
	for i := 1; ; i++ {
		fh, err := os.OpenFile(l, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			fh.WriteString(me + "\n")
			fh.Close()
			return l
		}
		owner, _ := firstLine(l)
		if n, err := strconv.Atoi(owner); err == nil && digits(owner) {
			if syscall.Kill(n, 0) != nil {
				if again, _ := firstLine(l); again == owner {
					os.Remove(l)
				}
				continue
			}
		}
		if i >= 100 {
			return sf + "." + me + ".tmp"
		}
		time.Sleep(20 * time.Millisecond)
	}
}

var rePid = []byte(`"pid":`)

// pidOf is the pid in a state file's first line ("pid":N); empty without
// one.
func pidOf(f string) string {
	l, _ := firstLine(f)
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

// lose keeps the thread of the agent whose state file is f as lost, then
// removes its files; only if its pid is still pid when we hold its lock.
func (e *Env) lose(f, pid string) bool {
	if !exists(f) {
		return true
	}
	l := lock(f)
	defer os.Remove(l)
	if pidOf(f) != pid {
		return true
	}
	b := strings.TrimSuffix(f, ".json")
	st, err := readState(f)
	sid := st.SessionID
	if err != nil || sid == "" {
		return false
	}
	th, err := readThread(b + ".thread")
	if err != nil {
		return false
	}
	base := filepath.Base(b)
	skey := base[:strings.LastIndexByte(base, '_')]
	rec := Lost{Lost: jsonInt(time.Now().Unix()), SKey: skey, State: st, Thread: th}
	if writeRecord(e.LostDir+"/"+skey+"_"+sid+".json", rec) != nil {
		return false
	}
	for _, x := range []string{f, b + ".nvim", b + ".bash", b + ".prompt", b + ".thread"} {
		os.Remove(x)
	}
	os.RemoveAll(b + ".opening")
	return true
}

// firstJSON is the first JSON value of a file (jq --slurpfile's [0]).
func firstJSON(b []byte) []byte {
	d := json.NewDecoder(bytes.NewReader(b))
	var raw json.RawMessage
	if d.Decode(&raw) != nil {
		return nil
	}
	return raw
}

// forget removes the files of the agent whose state file is f (its activity
// log stays); only if its pid is still pid when we hold its lock.
func (e *Env) forget(f, pid string) {
	if !exists(f) {
		return
	}
	l := lock(f)
	defer os.Remove(l)
	if pidOf(f) != pid {
		return
	}
	b := strings.TrimSuffix(f, ".json")
	for _, x := range []string{f, b + ".nvim", b + ".bash", b + ".prompt", b + ".thread"} {
		os.Remove(x)
	}
	os.RemoveAll(b + ".opening")
}

func lookPath(f string) (string, error) { return exec.LookPath(f) }
