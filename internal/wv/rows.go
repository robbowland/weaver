package wv

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
	Pane, Tab, State, Root, Since, Seen, Prompt, Waiting, Bg, Title, Nsub, Pid, Pstart, Sfile string
	Turn, Last, Tdone, Ttotal, File, Sid, Tabname, Agent, Bgtasks, Ttask, Tname, Tnum         string
	Troots                                                                                    []string
}

// obj is a JSON object read with its numbers as they were written.
type obj map[string]any

func readObj(b []byte) (obj, bool) {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var v any
	if d.Decode(&v) != nil {
		return nil, false
	}
	o, ok := v.(map[string]any)
	return obj(o), ok
}

// get is jq's .k // d: the value unless it is missing, null or false.
func (o obj) get(k string, d any) any {
	if o == nil {
		return d
	}
	v, ok := o[k]
	if !ok || v == nil || v == false {
		return d
	}
	return v
}

// str is jq's tostring with control characters made spaces, as the rows'
// fields are printed.
func str(v any) string { return clean(tostring(v)) }

func tostring(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
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

// num is a JSON value as a number (jq's arithmetic); ok false when it is not one.
func num(v any) (float64, bool) {
	switch x := v.(type) {
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case float64:
		return x, true
	case int:
		return float64(x), true
	}
	return 0, false
}

// jqLess is jq's ordering for the comparisons the rows make: numbers by
// value, and any number below any string or other value (null < false <
// true < numbers < strings < arrays < objects).
func jqGreater(a, b any) bool { return jqCmp(a, b) > 0 }

func jqCmp(a, b any) int {
	ra, rb := jqRank(a), jqRank(b)
	if ra != rb {
		if ra < rb {
			return -1
		}
		return 1
	}
	switch ra {
	case 3:
		x, _ := num(a)
		y, _ := num(b)
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
		return 0
	case 4:
		return strings.Compare(a.(string), b.(string))
	}
	return 0
}

func jqRank(v any) int {
	switch x := v.(type) {
	case nil:
		return 0
	case bool:
		if x {
			return 2
		}
		return 1
	case json.Number, float64, int:
		return 3
	case string:
		return 4
	case []any:
		return 5
	}
	return 6
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
	type gone struct{ f, pid, start, st string }
	var gones []gone
	var pids []int
	for _, f := range jsons {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		s, ok := readObj(b)
		if !ok {
			continue // corrupt
		}
		var t obj
		if tb, err := os.ReadFile(strings.TrimSuffix(f, ".json") + ".thread"); err == nil {
			t, _ = readObj(tb)
		}
		p := byID[tostring(s["pane"])]
		if p == nil {
			g := gone{f, str(s.get("pid", "")), str(s.get("proc_start", "")), str(s.get("state", ""))}
			gones = append(gones, g)
			if n, err := strconv.Atoi(g.pid); err == nil {
				pids = append(pids, n)
			}
			continue
		}
		if p.Exited || s["state"] == "ended" {
			continue
		}
		var c obj
		if c0, ok := live[tostring(s.get("pid", ""))]; ok && tostring(c0.get("sessionId", "")) == tostring(s.get("session_id", "")) {
			c = c0
		}
		cu := 0.0
		if c != nil {
			if v, ok := num(c.get("statusUpdatedAt", 0)); ok {
				cu = math.Floor(v / 1000)
			}
		}
		st0 := s.get("state", "idle")
		var st any = st0
		changed := false
		cst := ""
		if c != nil {
			cst = tostring(c.get("status", nil))
		}
		cuv := json.Number(strconv.FormatFloat(cu, 'f', -1, 64))
		switch {
		case c == nil:
		case st0 == "needs-you" && cst == "busy" && jqGreater(cuv, s.get("since", json.Number("0"))):
			st, changed = "working", true
		case st0 != "needs-you" && cst == "waiting" && !jqGreater(s.get("ts", json.Number("0")), cuv):
			st, changed = "needs-you", st0 != "needs-you"
		case st0 == "working" && cst == "idle" && jqCmp(s.get("background", json.Number("0")), json.Number("0")) == 0 &&
			jqGreater(cuv, s.get("ts", json.Number("0"))) && float64(now)-cu > 3:
			st, changed = "interrupted", true
		}
		r := Row{Pane: str(s["pane"]), Tab: strconv.Itoa(p.Tab), State: str(st)}
		r.Root = str(firstOf(s.get("root", nil), s.get("cwd", nil), ""))
		if !changed {
			r.Since = str(firstOf(s.get("since", nil), s.get("ts", nil), json.Number("0")))
		} else {
			r.Since = str(cuv)
		}
		r.Seen = str(s.get("seen", false))
		r.Prompt = str(s.get("prompt", ""))
		if st == "needs-you" && st0 != "needs-you" {
			r.Waiting = str(c.get("waitingFor", "input"))
		} else {
			r.Waiting = str(s.get("waiting", ""))
		}
		r.Bg = str(s.get("background", json.Number("0")))
		r.Title = str(s.get("title", ""))
		r.Nsub = strconv.Itoa(jqLength(s.get("subagents", []any{})))
		r.Pid = str(s.get("pid", ""))
		r.Pstart = str(s.get("proc_start", ""))
		r.Sfile = f
		r.Turn = str(s.get("turn", json.Number("0")))
		r.Last = str(s.get("last_message", ""))
		if ts, ok := s.get("tasks", []any{}).([]any); ok {
			d := 0
			for _, x := range ts {
				if o, ok := x.(map[string]any); ok && o["done"] == true {
					d++
				}
			}
			r.Tdone, r.Ttotal = strconv.Itoa(d), strconv.Itoa(len(ts))
		} else {
			continue // jq's arrays filter: no row at all
		}
		fs := strings.Split(tostring(s.get("file", "")), "/")
		r.File = clean(fs[len(fs)-1])
		r.Sid = str(s.get("session_id", ""))
		r.Tabname = clean(p.TabName)
		r.Agent = str(s.get("agent", ""))
		r.Bgtasks = str(s.get("background_tasks", ""))
		r.Ttask, r.Tname, r.Tnum = str(t.get("task", "")), str(t.get("name", "")), str(t.get("num", ""))
		if tr, ok := s.get("turn_roots", []any{}).([]any); ok {
			for _, x := range tr {
				r.Troots = append(r.Troots, clean(tostring(x)))
			}
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
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		t, ok := readObj(b)
		if !ok {
			continue
		}
		p := byID[tostring(t.get("pane", ""))]
		if p == nil || p.Exited {
			continue
		}
		rows = append(rows, Row{Pane: str(t["pane"]), Tab: strconv.Itoa(p.Tab), State: "starting",
			Root: str(t.get("worktree", "")), Since: str(t.get("created", json.Number(strconv.FormatInt(now, 10)))),
			Seen: "false", Prompt: str(t.get("task", "")), Bg: "0", Nsub: "0", Sfile: f, Turn: "0", Tdone: "0", Ttotal: "0",
			Tabname: clean(p.TabName), Agent: str(t.get("agent", "")), Ttask: str(t.get("task", "")),
			Tname: str(t.get("name", "")), Tnum: str(t.get("num", ""))})
	}
	starts := procStarts(pids)
	for _, g := range gones {
		if !trusted {
			continue
		}
		if agentAlive(g.pid, g.start, starts) && g.pid != "" && g.st != "ended" {
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

// firstOf is jq's a // b // d over values already defaulted by get.
func firstOf(vs ...any) any {
	for _, v := range vs {
		if v != nil && v != false {
			return v
		}
	}
	return nil
}

// jqLength is jq's length: of an array, object or string, or a number's
// absolute value; null's is 0.
func jqLength(v any) int {
	switch x := v.(type) {
	case []any:
		return len(x)
	case map[string]any:
		return len(x)
	case string:
		return len([]rune(x))
	case json.Number:
		f, _ := x.Float64()
		if f < 0 {
			f = -f
		}
		return int(f)
	}
	return 0
}

// claudeLive are the Claude Code sessions running on this machine, from
// their ~/.claude/sessions/<pid>.json files (each one line), by pid.
func (e *Env) claudeLive() map[string]obj {
	m := map[string]obj{}
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
		o, ok := readObj([]byte(l))
		if !ok {
			continue
		}
		m[tostring(o["pid"])] = o
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
		o, ok := readObj(b)
		if !ok {
			continue
		}
		s, _ := o.get("state", nil).(map[string]any)
		t, _ := o.get("thread", nil).(map[string]any)
		st, th := obj(s), obj(t)
		r := LostRow{File: f, Sid: str(st.get("session_id", "")), Name: str(th.get("name", "")), Num: str(th.get("num", "")),
			Dir: str(lostDir(st, th)), Lost: str(o.get("lost", json.Number("0"))), Skey: str(lostSkey(o)),
			Task: str(firstOf(th.get("task", nil), st.get("prompt", nil), "")), Title: str(st.get("title", ""))}
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

func present(v any) bool { return v != nil && v != "" }

// lostDir: where a lost thread resumes (where its agent started, else its
// thread's worktree, else where it last was), as JQ_LOST's lost_dir.
func lostDir(s, t obj) any {
	for _, v := range []any{s["start_cwd"], t["worktree"], s["root"], s["cwd"]} {
		if present(v) {
			return v
		}
	}
	return ""
}

func lostSkey(o obj) any {
	for _, v := range []any{o["skey"], o["session"]} {
		if present(v) {
			return v
		}
	}
	return ""
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
	data, err := os.ReadFile(f)
	if err != nil {
		return false
	}
	s, ok := readObj(data)
	if !ok {
		return false
	}
	sid := tostring(s.get("session_id", ""))
	if sid == "" {
		return false
	}
	switch tostring(s.get("end_reason", "")) {
	case "prompt_input_exit", "logout", "clear":
		return false
	}
	ts, _ := num(s.get("ts", json.Number("0")))
	base := filepath.Base(b)
	return e.serverLost(base[:strings.LastIndexByte(base, '_')], ts, tostring(s.get("server", "")))
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
	data, err := os.ReadFile(f)
	if err != nil {
		return false
	}
	s, _ := readObj(data)
	sid := tostring(s.get("session_id", ""))
	if sid == "" {
		return false
	}
	th, err := os.ReadFile(b + ".thread")
	if err != nil {
		return false
	}
	base := filepath.Base(b)
	skey := base[:strings.LastIndexByte(base, '_')]
	var sc, tc bytes.Buffer
	if json.Compact(&sc, firstJSON(data)) != nil || json.Compact(&tc, firstJSON(th)) != nil {
		return false
	}
	sk, _ := json.Marshal(skey)
	rec := `{"lost":` + strconv.FormatInt(time.Now().Unix(), 10) + `,"skey":` + string(sk) +
		`,"state":` + sc.String() + `,"thread":` + tc.String() + "}\n"
	os.MkdirAll(e.LostDir, 0o755)
	lf := e.LostDir + "/" + skey + "_" + sid + ".json"
	tmp := lf + "." + strconv.Itoa(os.Getpid())
	if os.WriteFile(tmp, []byte(rec), 0o644) != nil || os.Rename(tmp, lf) != nil {
		os.Remove(tmp)
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
