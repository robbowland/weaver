package wvr

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// The hook: one agent event (Claude Code's hook input on stdin, or another
// agent's, made one by Event) folded into its state file. What it needs besides the state (nvim, tmux, Worktrunk,
// snapshots, the status line) runs after it in a detached `wvr hook --bg`.

const rs, us = "\x1e", "\x1f"

// bellJSON rings the bell: Claude Code writes terminalSequence to the agent's
// terminal.
const bellJSON = `{"terminalSequence":"\u0007"}`

// hk is one hook run: the event's fields, the stored state's (o*), and what
// it makes of them. Exported fields go to the background run.
type hk struct {
	Pane, AgentKind, SKey string
	Mode                  string `json:"-"`
	sf                    string

	Event, Cwd, Sid, File, Prompt, PromptLong, Tool, Ntype string
	Now, NowMs                                             int64
	WaitingNew, Key, LastNew, Aid, Atype, BgNew, BgdescNew string
	Source, Interrupt, TitleNew, PromptID, Transcript      string
	NewCwd, Rejected, Err, Cmd, EndNew                     string

	// Old is the stored state, of whichever session; Same says it is this
	// session's (sess).
	Old                                               AgentState
	Same                                              bool
	FilesJSON, SubsJSON, TasksJSON, OTurnRoots, Paths string

	State                                                                 State
	Root, EvFile, Start, Proj, Since, Waiting, Pending, Last, Seen        string
	Marked, Marker, MarkDir, Bg, Bgdesc, Pid, ProcStart, Title, EndReason string
	Turn, TurnEnded                                                       int
	TurnRoots                                                             string
	Moved                                                                 bool
	SnapPrev, SnapStart, SnapEnd, FirstTouch, PrevPrompt, TurnPrompt      string
	NV                                                                    string
	// What it is running now (a Bash command, from the async PreToolUse
	// until its PostToolUse), that one's tool_use_id, and the ids of the
	// last tools that finished (an async PreToolUse may land after them).
	ToolUseID, Doing, DoingID, DoneIDs, ODoing, ODoingID, ODoneIDs string
	rc                                                             int
}

// Hook is `wvr hook [--async]` (and, from Event, another agent's event):
// returns the bell to ring (10 needs you, 11 done; 0 none).
func (e *Env) Hook(in []byte, mode, pane, agent, me string) int {
	h := &hk{Pane: pane, AgentKind: agent, Mode: mode, SKey: e.SKey}
	if h.AgentKind == "" {
		h.AgentKind = "claude"
	}
	if os.MkdirAll(e.StateDir, 0o755) != nil {
		return 0
	}
	h.sf = e.StateFile(pane)
	pk := peek(h.sf)
	// A session nested in this pane's agent (claude -p, an SDK script)
	// inherits its pane: while the pane's own agent runs, its events are not
	// ours.
	if me != "" && pk.pid != "" && me != pk.pid {
		if agentAlive(pk.pid, pk.start, nil) {
			if pk.state != StateEnded {
				return 0
			}
		} else {
			// The pane's last agent died with a session that is gone, and
			// this one gave its pane to a new agent: keep the old one's
			// thread as lost before this state replaces it.
			if e.lostCheck(h.sf) {
				e.lose(h.sf, pk.pid)
			}
			pk = peek(h.sf)
		}
	}
	var ev obj
	var raw map[string]json.RawMessage
	{
		d := json.NewDecoder(bytes.NewReader(in))
		d.UseNumber()
		var v any
		if d.Decode(&v) != nil {
			return 0
		}
		m, ok := v.(map[string]any)
		if !ok {
			return 0
		}
		ev = obj(m)
		json.Unmarshal(in, &raw)
	}
	if mode == "async" && !asyncGate(ev, pk.state) {
		return 0
	}
	lk := lock(h.sf)
	st, _ := os.ReadFile(h.sf)
	if !h.parse(ev, raw, st) {
		os.Remove(lk)
		return 0
	}
	h.run(e, me)
	e.writeState(h, lk)
	e.afterWrite(h)
	return h.rc
}

type peeked struct {
	state      State
	pid, start string
}

var (
	rePkState = regexp.MustCompile(`"state":"([a-z-]+)"`)
	rePkPid   = regexp.MustCompile(`"pid":([0-9]+)`)
	rePkStart = regexp.MustCompile(`"proc_start":"([^"]*)"`)
)

// peek is the stored state's state, pid and start, read from its first
// line without parsing it.
func peek(sf string) peeked {
	l, _ := firstLine(sf)
	var p peeked
	if m := rePkState.FindStringSubmatch(l); m != nil {
		p.state = State(m[1])
	}
	if m := rePkPid.FindStringSubmatch(l); m != nil {
		p.pid = m[1]
	}
	if m := rePkStart.FindStringSubmatch(l); m != nil {
		p.start = m[1]
	}
	return p
}

// asyncGate: the async hook runs after every tool call; most of them change
// nothing.
func asyncGate(ev obj, state State) bool {
	switch ev["hook_event_name"] {
	case "PostToolUse":
		t := tostring(ev.get("tool_name", ""))
		switch t {
		case "Bash", "Edit", "Write", "MultiEdit", "NotebookEdit", "EnterWorktree", "ExitWorktree":
			return true
		}
		if strings.HasPrefix(t, "mcp__") {
			// *[Ww]rite*, *[Ee]dit*, ...: the word, its first letter either case.
			for _, w := range []string{"Write", "Edit", "Replace", "Insert", "Create", "Patch", "Apply", "Rename", "Move", "Delete"} {
				if strings.Contains(t, w) || strings.Contains(t, strings.ToLower(w[:1])+w[1:]) {
					return true
				}
			}
		}
		return state != StateWorking
	case "PostToolBatch":
		return state == StateNeedsYou
	}
	return true
}

var (
	reCmdPath  = regexp.MustCompile("(?:^|[\\s=;&|(\"':])(/[^\\s\"';&|()<>`$*?:]+)")
	reYours    = regexp.MustCompile(`^\s*(<agent-message|<task-notification|<teammate-message|<channel|Another Claude session sent a message)`)
	reRejected = regexp.MustCompile(`doesn.t want to proceed with this tool use`)
)

// editFile is the file an edit tool wrote (absolute), else "".
func editFile(ev obj) string {
	t := ev.get("tool_name", "")
	if ev["hook_event_name"] != "PostToolUse" || t != "Edit" && t != "Write" && t != "MultiEdit" && t != "NotebookEdit" {
		return ""
	}
	var f any = ""
	if ti, ok := ev["tool_input"].(map[string]any); ok {
		f = firstOf(obj(ti).get("file_path", nil), obj(ti).get("notebook_path", nil), "")
	}
	s := line(f)
	if s == "" || strings.HasPrefix(s, "/") {
		return s
	}
	return tostring(ev.get("cwd", "")) + "/" + s
}

// commandPaths are the absolute paths a Bash command names (cd targets,
// VAR=/path, arguments), or an MCP tool's input holds: where to look for
// what it changed. At most 8, sorted, RS-separated.
func commandPaths(ev obj) string {
	t := tostring(ev.get("tool_name", ""))
	var ps []string
	switch {
	case strings.HasPrefix(t, "mcp__"):
		var walk func(v any)
		walk = func(v any) {
			switch x := v.(type) {
			case string:
				if strings.HasPrefix(x, "/") {
					ps = append(ps, line(x))
				}
			case []any:
				for _, y := range x {
					walk(y)
				}
			case map[string]any:
				for _, y := range x {
					walk(y)
				}
			}
		}
		walk(ev["tool_input"])
	case ev["tool_name"] == "Bash":
		c := ""
		if ti, ok := ev["tool_input"].(map[string]any); ok {
			c = tostring(obj(ti).get("command", ""))
		}
		for _, m := range reCmdPath.FindAllStringSubmatch(c, -1) {
			ps = append(ps, line(m[1]))
		}
	default:
		return ""
	}
	sort.Strings(ps)
	var u []string
	for i, p := range ps {
		if i == 0 || p != ps[i-1] {
			u = append(u, p)
		}
	}
	if len(u) > 8 {
		u = u[:8]
	}
	return strings.Join(u, rs)
}

// summary is what a permission dialog asks about.
func summary(ev obj) string {
	i := obj{}
	if ti, ok := ev["tool_input"].(map[string]any); ok {
		i = obj(ti)
	}
	fp := firstOf(i.get("file_path", nil), i.get("notebook_path", nil))
	tn := tostring(ev.get("tool_name", ""))
	switch {
	case ev["tool_name"] == "Bash":
		return "Bash: " + tostring(i.get("command", ""))
	case ev["tool_name"] == "AskUserQuestion":
		q := ""
		if qs, ok := i.get("questions", []any{}).([]any); ok && len(qs) > 0 {
			if q0, ok := qs[0].(map[string]any); ok {
				q = tostring(obj(q0).get("question", ""))
			}
		}
		return "question: " + q
	case fp != nil:
		p := strings.Split(tostring(fp), "/")
		return tn + ": " + p[len(p)-1]
	}
	return tn
}

func yours(ev obj) bool {
	if src := ev.get("source", "user"); src != "user" {
		return false
	}
	return !reYours.MatchString(tostring(ev.get("prompt", "")))
}

// background are the background subagents and workflows still running when
// the agent stops: it wakes when they report back.
func background(ev obj) []obj {
	var out []obj
	bs, _ := ev.get("background_tasks", []any{}).([]any)
	for _, b := range bs {
		if m, ok := b.(map[string]any); ok && (m["type"] == "subagent" || m["type"] == "workflow") {
			out = append(out, obj(m))
		}
	}
	return out
}

func rejected(ev obj) bool {
	if ev["hook_event_name"] != "PostToolBatch" {
		return false
	}
	cs, _ := ev.get("tool_calls", []any{}).([]any)
	for _, c := range cs {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		r := tostring(obj(m).get("tool_response", ""))
		if reRejected.MatchString(r) && !strings.Contains(r, "the user said:") {
			return true
		}
	}
	return false
}

// rawOr is a stored state's member as jq's tostring of `.k // d`.
func rawOr(s obj, k, d string) string {
	v := s.get(k, nil)
	if v == nil {
		return d
	}
	return tostring(v)
}

// parse reads the event and the stored state: false
// when there is no event to fold in.
func (h *hk) parse(ev obj, raw map[string]json.RawMessage, st []byte) bool {
	var s obj
	if o, ok := readObj(st); ok {
		s = o
	} else {
		s = obj{}
	}
	h.Sid = ctl(tostring(ev.get("session_id", "")))
	o := obj{}
	if v, ok := s.get("session_id", "").(string); ok && v == tostring(ev.get("session_id", "")) {
		o = s
	}
	h.Aid = ctl(tostring(ev.get("agent_id", "")))
	now := time.Now()
	h.Now = now.Unix()
	h.NowMs = now.UnixMilli()
	evn := ev.get("hook_event_name", "")
	h.Event = ctl(tostring(evn))
	h.Cwd = ctl(tostring(ev.get("cwd", "")))
	h.File = ctl(editFile(ev))
	if evn == "UserPromptSubmit" && yours(ev) {
		h.Prompt = ctl(one(ev.get("prompt", ""), 120))
		h.PromptLong = ctl(one(ev.get("prompt", ""), 2000))
	}
	h.Tool = ctl(tostring(ev.get("tool_name", "")))
	h.Ntype = ctl(tostring(ev.get("notification_type", "")))
	w := ""
	switch evn {
	case "PermissionRequest", "PreToolUse":
		w = summary(ev)
	case "Notification":
		w = tostring(ev.get("message", ""))
	case "StopFailure":
		w = "error: " + tostring(ev.get("error", ""))
	}
	h.WaitingNew = ctl(one(w, 100))
	if tn := ev.get("tool_name", nil); tn != nil {
		ti := "null"
		if r, ok := raw["tool_input"]; ok {
			ti = canon(r)
		}
		h.Key = ctl(tostring(tn) + ":" + runeSlice(ti, 300))
	}
	h.LastNew = ctl(one(ev.get("last_assistant_message", ""), 400))
	h.Atype = ctl(one(ev.get("agent_type", ""), 40))
	if evn == "Stop" {
		bg := background(ev)
		h.BgNew = strconv.Itoa(len(bg))
		var ds []string
		for _, b := range bg {
			ds = append(ds, one(b.get("description", ""), 80))
		}
		h.BgdescNew = ctl(one(strings.Join(ds, "; "), 240))
	}
	h.Source = ctl(tostring(ev.get("source", "")))
	if ev["is_interrupt"] == true {
		h.Interrupt = "1"
	}
	h.TitleNew = ctl(one(ev.get("session_title", ""), 80))
	h.PromptID = ctl(tostring(ev.get("prompt_id", "")))
	h.Transcript = ctl(tostring(ev.get("transcript_path", "")))
	h.NewCwd = ctl(tostring(ev.get("new_cwd", "")))
	if rejected(ev) {
		h.Rejected = "1"
	}
	h.Err = ctl(one(tostring(ev.get("error", "")), 100))
	h.ToolUseID = ctl(one(ev.get("tool_use_id", ""), 80))
	if ev["tool_name"] == "Bash" {
		c := any("")
		if ti, ok := ev["tool_input"].(map[string]any); ok {
			c = obj(ti).get("command", "")
		}
		h.Cmd = ctl(one(c, 200))
	}
	if evn == "SessionEnd" {
		h.EndNew = ctl(one(ev.get("reason", ""), 40))
	}
	// The stored state, typed; whether it is this session's.
	if decodeRecord(firstJSON(st), &h.Old) != nil {
		h.Old = AgentState{}
	}
	h.Same = len(o) > 0
	h.FilesJSON = filesNew(st, o, h.File)
	h.SubsJSON = subsNew(st, o, ev, h.Aid, h.Now)
	h.TasksJSON = tasksNew(st, o, ev)
	var tr []string
	if a, ok := o.get("turn_roots", []any{}).([]any); ok {
		for _, x := range a {
			tr = append(tr, line(x))
		}
	}
	h.OTurnRoots = strings.Join(tr, rs)
	h.Paths = commandPaths(ev)
	return h.Event != ""
}

// stored is the raw array member k of the stored state when it belongs to
// this session (o is the state, or empty).
func stored(st []byte, o obj, k string) []json.RawMessage {
	if len(o) == 0 {
		return nil
	}
	so, ok := parseOobj(st)
	if !ok {
		return nil
	}
	r, ok := so.get(k)
	if !ok {
		return nil
	}
	a, _ := rawArray(r)
	return a
}

func filesNew(st []byte, o obj, file string) string {
	fs := stored(st, o, "files")
	if file == "" {
		return joinRaw(fs)
	}
	var out []json.RawMessage
	for _, f := range fs {
		var s string
		if json.Unmarshal(f, &s) == nil && s == file {
			continue
		}
		out = append(out, f)
	}
	out = append(out, json.RawMessage(jqStr(file)))
	if len(out) > 200 {
		out = out[len(out)-200:]
	}
	return joinRaw(out)
}

// idOf is an array element's id member, decoded (nil without one).
func idOf(e json.RawMessage) any {
	var m map[string]any
	d := json.NewDecoder(bytes.NewReader(e))
	d.UseNumber()
	if d.Decode(&m) != nil {
		return nil
	}
	return m["id"]
}

func subsNew(st []byte, o obj, ev obj, aid string, now int64) string {
	subs := stored(st, o, "subagents")
	without := func() []json.RawMessage {
		var out []json.RawMessage
		for _, s := range subs {
			if id, ok := idOf(s).(string); ok && id == aid {
				continue
			}
			out = append(out, s)
		}
		return out
	}
	switch ev["hook_event_name"] {
	case "SubagentStart":
		if aid != "" {
			n := `{"id":` + jqStr(aid) + `,"type":` + jqStr(one(ev.get("agent_type", ""), 40)) + `,"since":` + strconv.FormatInt(now, 10) + "}"
			return joinRaw(append(without(), json.RawMessage(n)))
		}
	case "SubagentStop":
		return joinRaw(without())
	case "Stop":
		if len(background(ev)) == 0 {
			return "[]"
		}
	case "SessionStart":
		if ev["source"] != "compact" {
			return "[]"
		}
	}
	return joinRaw(subs)
}

func tasksNew(st []byte, o obj, ev obj) string {
	tasks := stored(st, o, "tasks")
	tid := tostring(ev.get("task_id", ""))
	tsub := one(ev.get("task_subject", ""), 80)
	last30 := func(a []json.RawMessage) []json.RawMessage {
		if len(a) > 30 {
			return a[len(a)-30:]
		}
		return a
	}
	is := func(e json.RawMessage) bool { id, ok := idOf(e).(string); return ok && id == tid }
	switch ev["hook_event_name"] {
	case "TaskCreated":
		var out []json.RawMessage
		for _, t := range tasks {
			if !is(t) {
				out = append(out, t)
			}
		}
		out = append(out, json.RawMessage(`{"id":`+jqStr(tid)+`,"subject":`+jqStr(tsub)+`,"done":false}`))
		return joinRaw(last30(out))
	case "TaskCompleted":
		found := false
		var out []json.RawMessage
		for _, t := range tasks {
			if is(t) {
				found = true
				if oo, ok := parseOobj(t); ok {
					t = json.RawMessage(oo.set("done", "true").String())
				}
			}
			out = append(out, t)
		}
		if found {
			return joinRaw(out)
		}
		out = append(out, json.RawMessage(`{"id":`+jqStr(tid)+`,"subject":`+jqStr(tsub)+`,"done":true}`))
		return joinRaw(last30(out))
	case "SessionStart":
		if ev["source"] != "compact" && ev["source"] != "resume" {
			return "[]"
		}
	}
	return joinRaw(tasks)
}

func atoi(s string) (int, bool) {
	if !digits(s) {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

// findTop is the work tree holding directory dir: the nearest ancestor with
// a .git, as a physical path like git's.
func findTop(dir string) (string, bool) {
	d, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", false
	}
	if !filepath.IsAbs(d) {
		if d, err = filepath.Abs(d); err != nil {
			return "", false
		}
	}
	if st, err := os.Stat(d); err != nil || !st.IsDir() {
		return "", false
	}
	if d == "/" {
		d = ""
	}
	for {
		if exists(d + "/.git") {
			if d == "" {
				return "/", true
			}
			return d, true
		}
		if d == "" {
			return "", false
		}
		d = d[:strings.LastIndexByte(d, '/')]
	}
}

// fileTop is the work tree holding a file that may not exist yet, in
// directories that may not exist yet.
func fileTop(f string) (string, bool) {
	d := f[:max(strings.LastIndexByte(f, '/'), 0)]
	for d != "" {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			break
		}
		d = d[:max(strings.LastIndexByte(d, '/'), 0)]
	}
	if d == "" {
		d = "/"
	}
	return findTop(d)
}

var reProcStart = regexp.MustCompile(`"procStart":"([^"]*)"`)

// run is the state machine, from the parse on.
func (h *hk) run(e *Env, me string) {
	// A new agent session in this pane (not a resume or compaction) starts
	// afresh; otherwise the root is never reset to the start directory.
	if h.Sid != "" && h.Sid != h.Old.SessionID {
		h.Old.Cwd, h.Old.Root, h.Old.File, h.Old.Prompt, h.Old.State, h.Old.StartCwd, h.Old.Since = "", "", "", "", "", "", ""
		h.Old.Waiting, h.Old.Pending, h.Old.LastMessage, h.Old.Seen, h.Old.ProjectRoot, h.Old.Background, h.Old.BackgroundTasks = "", "", "", false, "", 0, ""
	}
	h.Pid = me
	if h.Pid == "" {
		h.Pid = h.oPid()
	}
	h.ProcStart = h.Old.ProcStart
	if h.Pid != "" && (h.Pid != h.oPid() || h.ProcStart == "") {
		h.ProcStart = ""
		if l, err := firstLine(e.ClaudeDir + "/sessions/" + h.Pid + ".json"); err == nil {
			if m := reProcStart.FindStringSubmatch(l); m != nil {
				h.ProcStart = m[1]
			}
		}
		// Not Claude Code (or before it writes its session file): the
		// kernel's, once.
		if h.ProcStart == "" && h.Pid != h.oPid() {
			if n, ok := atoi(h.Pid); ok {
				h.ProcStart = strings.TrimRight(procStarts([]int{n})[n], " ")
			}
		}
	}
	// A subagent reports its own cwd: only the main agent's is tracked.
	cwd := h.Cwd
	if h.Aid != "" && h.Old.Cwd != "" {
		cwd = h.Old.Cwd
	}
	if h.Event == "CwdChanged" && h.NewCwd != "" {
		cwd = h.NewCwd
	}
	h.Cwd = cwd
	h.Start = h.Old.StartCwd
	if h.Start == "" {
		h.Start = cwd
	}
	h.Proj = h.Old.ProjectRoot
	if h.Proj == "" {
		d := os.Getenv("CLAUDE_PROJECT_DIR")
		if d == "" {
			d = h.Start
		}
		if t, ok := findTop(d); ok {
			h.Proj = t
		} else {
			h.Proj = "-"
		}
	}
	// Where the agent is really working: the repo of the file it (or one of
	// its subagents) just edited...
	if h.File != "" {
		h.EvFile = h.File
		if t, ok := fileTop(h.File); ok {
			h.Root = t
		}
	}
	// ...else the repo of a new cwd, if it is another repo or worktree: only
	// a real move counts (the worktree tools, CwdChanged, or a cwd in
	// another repo)...
	if h.Root == "" {
		if cwd != h.Old.Cwd {
			switch {
			case h.Event == "CwdChanged":
				h.Moved = true
			case h.Tool == "EnterWorktree" || h.Tool == "ExitWorktree":
				h.Moved, h.Start = true, cwd
			default:
				if cwd != h.Start {
					d := cwd
					if t, ok := findTop(cwd); ok {
						d = t
					}
					if d != h.Proj {
						h.Moved = true
					}
				}
			}
		}
		if h.Moved || h.Old.Root == "" {
			if t, ok := findTop(cwd); ok {
				h.Root = t
			} else {
				h.Root = cwd
			}
		} else {
			// ...else wherever it was before.
			h.Root = h.Old.Root
		}
	}
	// Turns: each prompt of yours starts one; it ends at Stop, StopFailure
	// or an interrupt. The roots it touches are snapshotted at its end.
	h.Turn, _ = atoi(h.oTurn())
	h.TurnEnded, _ = atoi(h.oTurnEnded())
	h.TurnRoots = h.OTurnRoots
	if h.Event == "UserPromptSubmit" && h.Prompt != "" {
		// Esc while the model was writing fires nothing: the previous turn
		// ends here.
		if h.Turn > h.TurnEnded {
			h.SnapPrev = strconv.Itoa(h.Turn)
		}
		h.Turn++
		h.TurnEnded = h.Turn - 1
		h.TurnRoots, h.SnapStart = h.Root, "1"
	} else if h.Turn > 0 && h.Root != "" && (h.EvFile != "" || h.Moved) {
		if !strings.Contains(rs+h.TurnRoots+rs, rs+h.Root+rs) {
			if h.TurnRoots != "" {
				h.TurnRoots += rs
			}
			h.TurnRoots += h.Root
			h.FirstTouch = h.Root
		}
	}
	switch h.Event {
	case "Stop", "StopFailure":
		if h.Turn > 0 {
			h.SnapEnd = "1"
		}
	case "PostToolUseFailure":
		if h.Interrupt != "" && h.Turn > 0 {
			h.SnapEnd = "1"
		}
	}
	if h.SnapEnd != "" {
		h.TurnEnded = h.Turn
	}

	bg, _ := atoi(h.BgNew)
	ch := transition(h.Old, h.sess(), hookEvent{Name: h.Event, Async: h.Mode == "async", Source: h.Source, Ntype: h.Ntype,
		Key: h.Key, Waiting: h.WaitingNew, Cmd: h.Cmd, ToolUseID: h.ToolUseID, Interrupt: h.Interrupt != "", Rejected: h.Rejected != "",
		Background: bg, BackgroundTasks: h.BgdescNew, Prompt: h.Prompt, File: h.EvFile, PromptID: h.PromptID,
		Title: h.TitleNew, Transcript: h.Transcript, LastMessage: h.LastNew, EndReason: h.EndNew, Now: h.Now})
	n := ch.Next
	h.State, h.Waiting, h.Pending = n.State, n.Waiting, n.Pending
	h.Bg, h.Bgdesc = strconv.Itoa(n.Background), n.BackgroundTasks
	h.Doing, h.DoingID, h.DoneIDs = n.Doing, n.DoingID, n.DoneIDs
	h.EndReason, h.rc = n.EndReason, ch.Bell
	h.Since, h.Seen = n.Since.String(), strconv.FormatBool(n.Seen)
	h.Last, h.Prompt, h.File, h.Title, h.PromptID, h.Transcript = n.LastMessage, n.Prompt, n.File, n.Title, n.PromptID, n.Transcript
	// Worktrunk branch markers, like its own Claude Code plugin sets for the
	// session's start directory, but on the worktree the agent is really in.
	h.Marker = n.Marker
	if os.Getenv("WEAVER_WT_MARKERS") != "0" && h.Marker != "" && h.Root != h.Proj && exists(h.Root+"/.git") {
		h.MarkDir = h.Root
	}
	h.Marked = h.MarkDir
	h.NV = readNvimPane(e, h.Pane)
}

// The stored state's members as the hook's text fields hold them.
func (h *hk) oSince() string     { return h.Old.Since.String() }
func (h *hk) oSeen() string      { return strconv.FormatBool(h.Old.Seen) }
func (h *hk) oBg() string        { return strconv.Itoa(h.Old.Background) }
func (h *hk) oPid() string       { return string(h.Old.Pid) }
func (h *hk) oTurn() string      { return strconv.Itoa(h.sess().Turn) }
func (h *hk) oTurnEnded() string { return strconv.Itoa(h.sess().TurnEnded) }

// sess is the stored state when it is this session's, else none.
func (h *hk) sess() AgentState {
	if h.Same {
		return h.Old
	}
	return AgentState{}
}

// stateCell is a state's glyph.
func stateCell(s State) string {
	switch s {
	case StateNeedsYou:
		return GNeed
	case StateDone:
		return GDone
	case StateWorking, StateStarting:
		return GWork
	case StateInterrupted:
		return GInt
	case StateEnded:
		return GEnd
	case StateIdle:
		return GIdle
	}
	return GIdle
}

// readNvimPane is the agent's nvim pane, when that nvim still runs.
func readNvimPane(e *Env, pane string) string {
	b, err := os.ReadFile(e.StateDir + "/" + e.SKey + "_" + pane + ".nvim")
	if err != nil {
		return ""
	}
	ls := strings.SplitN(string(b), "\n", 3)
	nv, pid := ls[0], ""
	if len(ls) > 1 {
		pid = ls[1]
	}
	if !digits(nv) && nv != "" {
		return ""
	}
	if pid != "" && !digits(pid) {
		pid = ""
	}
	if pid != "" {
		if n, _ := strconv.Atoi(pid); syscall.Kill(n, 0) != nil {
			return ""
		}
	}
	return nv
}

// writeState writes the state file from the hook's fields through its lock, which the rename releases.
func (e *Env) writeState(h *hk, lk string) {
	pane, _ := atoi(h.Pane)
	since := h.Since
	if since == "" {
		since = "0"
	}
	st := AgentState{Pane: jsonInt(pane), Agent: h.AgentKind, State: h.State, TS: h.Now, Since: json.Number(since),
		Seen: h.Seen == "true", SessionID: h.Sid, ProcStart: h.ProcStart, Title: h.Title, Turn: h.Turn, TurnEnded: h.TurnEnded,
		PromptID: h.PromptID, Transcript: h.Transcript, Cwd: h.Cwd, Root: h.Root, StartCwd: h.Start, ProjectRoot: h.Proj,
		File: h.File, Prompt: h.Prompt, Waiting: h.Waiting, Pending: h.Pending, LastMessage: h.Last, MarkedRoot: h.Marked,
		Marker: h.Marker, BackgroundTasks: h.Bgdesc, DoneIDs: h.DoneIDs, EndReason: h.EndReason,
		Subagents: []Subagent{}, Tasks: []Task{}, Files: []string{}, TurnRoots: []string{}}
	// The lists, kept as JSON text while the hook merges them.
	for _, l := range []struct {
		text string
		v    any
	}{{h.FilesJSON, &st.Files}, {h.SubsJSON, &st.Subagents}, {h.TasksJSON, &st.Tasks}} {
		if l.text != "" {
			bestEffort(json.Unmarshal([]byte(l.text), l.v))
		}
	}
	if digits(h.Pid) {
		st.Pid = json.Number(h.Pid)
	}
	if digits(h.Bg) {
		st.Background, _ = atoi(h.Bg)
	}
	if h.Doing != "" {
		st.Doing, st.DoingID = h.Doing, h.DoingID
	}
	if h.NV != "" {
		nv := json.Number(h.NV)
		st.NvimPane = &nv
	}
	// The tmux server it runs on ($TMUX is "socket,pid,session"): a server
	// started again is another, however soon (serverLost).
	if t := os.Getenv("TMUX"); strings.IndexByte(t, ',') >= 0 {
		srv := t[strings.IndexByte(t, ',')+1:]
		if j := strings.IndexByte(srv, ','); j >= 0 {
			srv = srv[:j]
		}
		if n, ok := atoi(srv); ok && n != 0 {
			st.Server = n
		}
	}
	if h.Sid != "" {
		st.Log = e.LogDir + "/" + h.Sid + ".jsonl"
	}
	for _, r := range strings.Split(h.TurnRoots, rs) {
		if r != "" {
			st.TurnRoots = append(st.TurnRoots, r)
		}
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if enc.Encode(st) != nil || os.WriteFile(lk, b.Bytes(), 0o644) != nil {
		os.Remove(lk)
		return
	}
	bestEffort(os.Rename(lk, h.sf))
}

// logLine appends one line to the agent session's activity log: members
// are the event's fields after "ev", as `,"k":v...`.
func (h *hk) logLine(e *Env, ev, members string) {
	if h.Sid == "" {
		return
	}
	f, err := os.OpenFile(e.LogDir+"/"+h.Sid+".jsonl", os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return
	}
	f.WriteString(`{"t":` + strconv.FormatInt(h.Now, 10) + `,"pane":` + jsonStr(h.Pane) + `,"turn":` + strconv.Itoa(h.Turn) +
		`,"ev":"` + ev + `"` + members + "}\n")
	f.Close()
}

// hookLog is the activity log's lines for this event (docs/design.md).
func (h *hk) hookLog(e *Env) {
	if h.Sid == "" {
		return
	}
	switch h.Event {
	case "SessionStart":
		h.logLine(e, "session", `,"source":`+jsonStr(h.Source))
	case "UserPromptSubmit":
		if h.SnapStart != "" {
			h.logLine(e, "prompt", `,"text":`+jsonStr(h.PromptLong)+`,"prompt_id":`+jsonStr(h.PromptID))
		}
	case "PostToolUse":
		if h.EvFile != "" {
			x := `,"file":` + jsonStr(h.EvFile) + `,"root":` + jsonStr(h.Root) + `,"tool":` + jsonStr(h.Tool)
			if h.Aid != "" {
				x += `,"agent_id":` + jsonStr(h.Aid)
			}
			h.logLine(e, "edit", x)
		}
	case "PostToolUseFailure":
		if h.Interrupt != "" {
			h.logLine(e, "interrupt", `,"tool":`+jsonStr(h.Tool))
		}
	case "SubagentStart", "SubagentStop":
		x := `,"agent_id":` + jsonStr(h.Aid) + `,"agent_type":` + jsonStr(h.Atype)
		if h.Event == "SubagentStart" {
			x += `,"phase":"start"`
		} else {
			x += `,"phase":"stop"`
		}
		h.logLine(e, "subagent", x)
	case "Stop":
		h.logLine(e, "stop", `,"last":`+jsonStr(h.LastNew))
	case "StopFailure":
		h.logLine(e, "error", `,"error":`+jsonStr(h.Err))
	case "SessionEnd":
		h.logLine(e, "end", "")
	}
	if h.Moved && h.Root != h.Old.Root {
		h.logLine(e, "cwd", `,"cwd":`+jsonStr(h.Cwd)+`,"root":`+jsonStr(h.Root))
	}
	if h.State != h.Old.State {
		if h.State == StateNeedsYou {
			h.logLine(e, "needs-you", `,"waiting":`+jsonStr(h.Waiting))
		}
		h.logLine(e, "state", `,"state":"`+string(h.State)+`"`)
	}
}

// afterWrite: the activity log, the turn's prompt, following a Bash or MCP
// edit (async), and the background work when there is some.
func (e *Env) afterWrite(h *hk) {
	if h.Sid != "" {
		os.MkdirAll(e.LogDir, 0o755)
	}
	pf := e.StateDir + "/" + e.SKey + "_" + h.Pane + ".prompt"
	if h.SnapPrev+h.SnapEnd != "" && exists(pf) {
		h.TurnPrompt, _ = firstLine(pf)
		h.PrevPrompt = h.TurnPrompt
	}
	if h.SnapStart != "" {
		os.WriteFile(pf, []byte(h.PromptLong+"\n"), 0o644)
	}
	h.hookLog(e)
	if h.Mode == "async" && h.Event == "PostToolUse" && (h.Tool == "Bash" || strings.HasPrefix(h.Tool, "mcp__")) {
		// Following a Bash or MCP edit needs git: this process runs in the
		// background anyway, so do it here.
		if !e.followBashEdits(h) {
			if h.Tool == "Bash" {
				h.logLine(e, "bash", `,"cmd":`+jsonStr(h.Cmd))
			}
			if h.NV != "" {
				e.nvimEvent(h, "checktime", "", "", "")
			}
		}
	}
	// The background work, only when there is some.
	if h.State != h.Old.State || h.Root != h.Old.Root || h.EvFile+h.SnapPrev+h.SnapStart+h.SnapEnd+h.FirstTouch != "" ||
		h.Title != h.sess().Title || h.Marker != h.Old.Marker || h.MarkDir != h.Old.MarkedRoot || h.Doing != h.sess().Doing ||
		h.Event == "SessionStart" || h.Event == "UserPromptSubmit" {
		e.spawnBg(h)
	}
}

// spawnBg starts `wvr hook --bg`, detached, with this run in its
// environment.
func (e *Env) spawnBg(h *hk) {
	// The stored state's lists stay behind: the background run has the new
	// ones, and its environment has room for so much.
	bg := *h
	bg.Old.Files, bg.Old.Tasks, bg.Old.Subagents, bg.Old.TurnRoots = nil, nil, nil, nil
	b, err := json.Marshal(&bg)
	if err != nil {
		return
	}
	c := exec.Command(e.Self, "hook", "--bg")
	c.Env = append(os.Environ(), "WEAVER_BG="+string(b))
	if os.Getenv("WEAVER_DEBUG") != "" {
		if f, err := os.OpenFile(e.StateDir+"/hook.log", os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644); err == nil {
			c.Stderr = f
		}
	}
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	c.Start()
}

// HookMain is `wvr hook [--async|--bg]`.
func (e *Env) HookMain(args []string) {
	mode := "sync"
	if len(args) > 0 {
		switch args[0] {
		case "--async":
			mode = "async"
		case "--bg":
			e.hookBg()
			return
		}
	}
	if e.MyPane == "" || e.Session == "" || isTTY(0) {
		return
	}
	in, _ := io.ReadAll(os.Stdin)
	me := os.Getenv("WEAVER_AGENT_PID")
	if me == "" {
		me = os.Getenv("CLAUDE_PID")
	}
	rc := e.Hook(in, mode, e.MyPane, os.Getenv("WEAVER_AGENT"), me)
	if mode == "sync" {
		bell := os.Getenv("WEAVER_BELL")
		if bell == "" {
			bell = "1"
		}
		if bell == "1" && (rc == 10 || rc == 11) || bell == "needs-you" && rc == 10 {
			os.Stdout.WriteString(bellJSON + "\n")
		}
	}
}
