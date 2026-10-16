package wvr

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	Old               AgentState
	Same              bool
	OTurnRoots, Paths string
	// The lists after the event, written in the foreground only.
	Files     []string   `json:"-"`
	Subagents []Subagent `json:"-"`
	Tasks     []Task     `json:"-"`

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
	var ev hookInput
	if decodeRecord(firstJSON(in), &ev) != nil {
		return 0
	}
	if mode == "async" && !asyncGate(ev, pk.state) {
		return 0
	}
	lk := lock(h.sf)
	st, _ := os.ReadFile(h.sf)
	if !h.parse(ev, st) {
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

// parse reads the event and the stored state: false when there is no
// event to fold in.
func (h *hk) parse(in hookInput, st []byte) bool {
	h.Sid = ctl(in.SessionID)
	h.Aid = ctl(in.AgentID)
	now := time.Now()
	h.Now = now.Unix()
	h.NowMs = now.UnixMilli()
	h.Event = ctl(in.Event)
	h.Cwd = ctl(in.Cwd)
	h.File = ctl(editFile(in))
	if in.Event == "UserPromptSubmit" && yours(in) {
		h.Prompt = ctl(one(in.Prompt, 120))
		h.PromptLong = ctl(one(in.Prompt, 2000))
	}
	h.Tool = ctl(in.ToolName)
	h.Ntype = ctl(in.Ntype)
	w := ""
	switch in.Event {
	case "PermissionRequest", "PreToolUse":
		w = summary(in)
	case "Notification":
		w = in.Message
	case "StopFailure":
		w = "error: " + string(in.Error)
	}
	h.WaitingNew = ctl(one(w, 100))
	if in.ToolName != "" {
		ti := "null"
		if len(in.ToolInput) > 0 {
			ti = canon(in.ToolInput)
		}
		h.Key = ctl(in.ToolName + ":" + runeSlice(ti, 300))
	}
	h.LastNew = ctl(one(in.LastMessage, 400))
	h.Atype = ctl(one(in.AgentType, 40))
	if in.Event == "Stop" {
		bg := background(in)
		h.BgNew = strconv.Itoa(len(bg))
		var ds []string
		for _, d := range bg {
			ds = append(ds, one(d, 80))
		}
		h.BgdescNew = ctl(one(strings.Join(ds, "; "), 240))
	}
	h.Source = ctl(in.Source)
	if in.Interrupt {
		h.Interrupt = "1"
	}
	h.TitleNew = ctl(one(in.Title, 80))
	h.PromptID = ctl(in.PromptID)
	h.Transcript = ctl(in.Transcript)
	h.NewCwd = ctl(in.NewCwd)
	if rejected(in) {
		h.Rejected = "1"
	}
	h.Err = ctl(one(string(in.Error), 100))
	h.ToolUseID = ctl(one(in.ToolUseID, 80))
	if in.ToolName == "Bash" {
		h.Cmd = ctl(one(in.input().Command, 200))
	}
	if in.Event == "SessionEnd" {
		h.EndNew = ctl(one(in.Reason, 40))
	}
	// The stored state, typed; whether it is this session's.
	known := decodeRecord(firstJSON(st), &h.Old) == nil
	if !known {
		h.Old = AgentState{}
	}
	h.Same = known && h.Old.SessionID == in.SessionID
	h.Files = filesNew(h.sess().Files, h.File)
	h.Subagents = subsNew(h.sess().Subagents, in, h.Aid, h.Now)
	h.Tasks = tasksNew(h.sess().Tasks, in)
	var tr []string
	for _, r := range h.sess().TurnRoots {
		tr = append(tr, line(r))
	}
	h.OTurnRoots = strings.Join(tr, rs)
	h.Paths = commandPaths(in)
	return h.Event != ""
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
	h.Turn, h.TurnEnded = h.sess().Turn, h.sess().TurnEnded
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

func (h *hk) oPid() string { return string(h.Old.Pid) }

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
		TurnRoots: []string{}}
	st.Files, st.Subagents, st.Tasks = orEmpty(h.Files), orEmpty(h.Subagents), orEmpty(h.Tasks)
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
