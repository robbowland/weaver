package engine

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/cli"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/git"
	"github.com/robbowland/weaver/internal/hooks"
	"github.com/robbowland/weaver/internal/jsonx"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/oneline"
	"github.com/robbowland/weaver/internal/process"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
)

// The hook: one agent event (Claude Code's hook input on stdin, or another
// agent's, made one by Event) folded into its state file. What it needs besides the state (Rex, Worktrunk,
// snapshots, the status) runs after it in a detached `wvr hook --bg`.

// bellJSON rings the bell: Claude Code writes terminalSequence to the agent's
// terminal.
const bellJSON = `{"terminalSequence":"\u0007"}`

// hk is one hook run: the event's fields, the stored state's (o*), and what
// it makes of them. Exported fields go to the background run.
type hk struct {
	Pane            mux.PaneID
	SKey string
	// The session it acts on (its name, its home's Rex id and the one it
	// ran in), which the background run takes rather than its
	// environment's.
	Session, RexID, RexHere string
	Mode                    string `json:"-"`
	sf                      string

	Event, Cwd, Sid, File, Prompt, Tool, Ntype             string
	Now, NowMs                                             int64
	WaitingNew, Key, LastNew, Aid, Atype, BgNew, BgdescNew string
	Source, Interrupt, TitleNew, PromptID, Transcript      string
	NewCwd, Rejected, Err, Cmd, EndNew                     string

	// Old is the stored state, of whichever session; Same says it is this
	// session's (sess).
	Old  agent.Record
	Same bool
	// Paths are where a command or MCP tool may have changed files.
	Paths []string
	// The lists after the event, written in the foreground only.
	Files     []string         `json:"-"`
	Subagents []agent.Subagent `json:"-"`
	Tasks     []agent.Task     `json:"-"`

	State                                                                 agent.State
	Root, EvFile, Start, Proj, Since, Waiting, Pending, Last, Seen        string
	Marked, Marker, MarkDir, Bg, Bgdesc, Pid, ProcStart, Title, EndReason string
	Turn                                                                  int
	Moved                                                                 bool
	// What it is running now (a Bash command, from the async PreToolUse
	// until its PostToolUse), that one's tool_use_id, and the ids of the
	// last tools that finished (an async PreToolUse may land after them).
	ToolUseID, Doing, DoingID, DoneIDs, ODoing, ODoingID, ODoneIDs string
	// FollowBash: the background run follows a Bash call's edits (a sync
	// hook's, which must not wait on git).
	FollowBash string
	// The files the call says it changed, and whether it says: handed on
	// to the background run, which follows them (FollowBash).
	Changed      []string
	ChangedKnown bool
	rc           int
}

// Hook is `wvr hook [--async]`: returns the bell to ring (10 needs you,
// 11 done; 0 none).
func Hook(e *session.Env, in []byte, mode string, pane mux.PaneID, me string) int {
	var ev hooks.Input
	if jsonx.Decode(jsonx.First(in), &ev) != nil {
		return 0
	}
	return hook(e, ev, mode, pane, me)
}

// hook folds event ev into the state of the agent in pane.
func hook(e *session.Env, ev hooks.Input, mode string, pane mux.PaneID, me string) int {
	h := &hk{Pane: pane, Mode: mode, SKey: e.SKey, Session: e.Session, RexID: e.RexID, RexHere: e.RexHere}
	me = knownPid(me)
	if os.MkdirAll(e.StateDir, 0o755) != nil {
		return 0
	}
	h.sf = e.StateFile(pane)
	pk := session.Peek(h.sf)
	// A session nested in this pane's agent (claude -p, an SDK script)
	// inherits its pane: while the pane's own agent runs, its events are not
	// ours.
	if me != "" && pk.Pid != "" && me != pk.Pid {
		if process.StillRuns(pk.Pid, pk.Start, nil) {
			if pk.State != agent.StateEnded {
				return 0
			}
		} else {
			// The pane's last agent died with a session that is gone, and
			// this one gave its pane to a new agent: keep the old one's
			// thread as lost before this state replaces it.
			if e.LostCheck(h.sf) {
				e.Lose(h.sf, pk.Pid)
			}
			pk = session.Peek(h.sf)
		}
	}
	// The pane's first event: a thread record an earlier server left under
	// its number is not this agent's.
	if !files.Exists(h.sf) {
		e.LoseStaleThread(pane)
	}
	if mode == "async" && !hooks.AsyncGate(ev, pk.State) {
		return 0
	}
	release := store.Lock(h.sf)
	st, _ := os.ReadFile(h.sf)
	if !h.parse(ev, st) {
		release()
		return 0
	}
	h.run(e, me)
	writeState(e, h)
	release()
	afterWrite(e, h)
	return h.rc
}

// parse reads the event and the stored state: false when there is no
// event to fold in.
func (h *hk) parse(in hooks.Input, st []byte) bool {
	h.Sid = oneline.Ctl(in.SessionID)
	h.Aid = oneline.Ctl(in.AgentID)
	now := time.Now()
	h.Now = now.Unix()
	h.NowMs = now.UnixMilli()
	h.Event = oneline.Ctl(in.Event)
	h.Cwd = oneline.Ctl(in.Cwd)
	h.File = oneline.Ctl(in.EditFile())
	if in.Event == "UserPromptSubmit" && in.Yours() {
		h.Prompt = oneline.Ctl(oneline.Squash(in.Prompt, 120))
	}
	h.Tool = oneline.Ctl(in.ToolName)
	h.Ntype = oneline.Ctl(in.Ntype)
	w := ""
	switch in.Event {
	case "PermissionRequest", "PreToolUse":
		w = in.Summary()
	case "Notification":
		w = in.Message
	case "StopFailure":
		w = "error: " + string(in.Error)
	}
	h.WaitingNew = oneline.Ctl(oneline.Squash(w, 100))
	if in.ToolName != "" {
		ti := "null"
		if len(in.ToolInput) > 0 {
			ti = jsonx.Canon(in.ToolInput)
		}
		h.Key = oneline.Ctl(in.ToolName + ":" + oneline.Cut(ti, 300))
	}
	h.LastNew = oneline.Ctl(oneline.Squash(in.LastMessage, 400))
	h.Atype = oneline.Ctl(oneline.Squash(in.AgentType, 40))
	if in.Event == "Stop" {
		bg := in.BackgroundWork()
		h.BgNew = strconv.Itoa(len(bg))
		var ds []string
		for _, d := range bg {
			ds = append(ds, oneline.Squash(d, 80))
		}
		h.BgdescNew = oneline.Ctl(oneline.Squash(strings.Join(ds, "; "), 240))
	}
	h.Source = oneline.Ctl(in.Source)
	if in.Interrupt {
		h.Interrupt = "1"
	}
	h.TitleNew = oneline.Ctl(oneline.Squash(in.Title, 80))
	h.PromptID = oneline.Ctl(in.PromptID)
	h.Transcript = oneline.Ctl(in.Transcript)
	h.NewCwd = oneline.Ctl(in.NewCwd)
	if in.Rejected() {
		h.Rejected = "1"
	}
	h.Err = oneline.Ctl(oneline.Squash(string(in.Error), 100))
	h.ToolUseID = oneline.Ctl(oneline.Squash(in.ToolUseID, 80))
	if in.ToolName == "Bash" {
		h.Cmd = oneline.Ctl(oneline.Squash(in.Tool().Command, 200))
	}
	if in.Event == "SessionEnd" {
		h.EndNew = oneline.Ctl(oneline.Squash(in.Reason, 40))
	}
	// The stored state, typed; whether it is this session's.
	known := jsonx.Decode(jsonx.First(st), &h.Old) == nil
	if !known {
		h.Old = agent.Record{}
	}
	h.Same = known && h.Old.SessionID == in.SessionID
	h.Files = hooks.Files(h.sess().Files, h.File)
	h.Subagents = hooks.Subagents(h.sess().Subagents, in, h.Aid, h.Now)
	h.Tasks = hooks.Tasks(h.sess().Tasks, in)
	h.Paths = in.CommandPaths()
	h.Changed, h.ChangedKnown = in.Changes()
	return h.Event != ""
}

// knownPid is pid as an agent's process weaver can follow; "" for none,
// and for 1 or less, which is no agent (launchd, the parent of an orphaned
// hook; 0, no process): recorded, it would read as dead, or as alive.
func knownPid(pid string) string {
	if n, ok := atoi(pid); ok && n > 1 {
		return pid
	}
	return ""
}

func atoi(s string) (int, bool) {
	if !digits(s) {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

var reProcStart = regexp.MustCompile(`"procStart":"([^"]*)"`)

// run is the state machine, from the parse on.
func (h *hk) run(e *session.Env, me string) {
	// A new agent session in this pane (not a resume or compaction) starts
	// afresh; otherwise the root is never reset to the start directory.
	if h.Sid != "" && h.Sid != h.Old.SessionID {
		h.Old.Cwd, h.Old.Root, h.Old.File, h.Old.Prompt, h.Old.State, h.Old.StartCwd, h.Old.Since = "", "", "", "", "", "", ""
		h.Old.Waiting, h.Old.Pending, h.Old.LastMessage, h.Old.Seen, h.Old.ProjectRoot, h.Old.Background, h.Old.BackgroundTasks = "", "", "", false, "", 0, ""
	}
	h.Pid = me
	if h.Pid == "" {
		h.Pid = knownPid(h.oPid())
	}
	h.ProcStart = h.Old.ProcStart
	if h.Pid != "" && (h.Pid != h.oPid() || h.ProcStart == "") {
		h.ProcStart = ""
		if l, err := files.FirstLine(e.ClaudeDir + "/sessions/" + h.Pid + ".json"); err == nil {
			if m := reProcStart.FindStringSubmatch(l); m != nil {
				h.ProcStart = m[1]
			}
		}
		// Not Claude Code (or before it writes its session file): the
		// kernel's, once.
		if h.ProcStart == "" && h.Pid != h.oPid() {
			if n, ok := atoi(h.Pid); ok {
				h.ProcStart = strings.TrimRight(process.Starts([]int{n})[n], " ")
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
		if t, ok := git.WorkTree(d); ok {
			h.Proj = t
		} else {
			h.Proj = "-"
		}
	}
	// Where the agent is really working: the repo of the file it (or one of
	// its subagents) just edited...
	if h.File != "" {
		h.EvFile = h.File
		if t, ok := git.FileWorkTree(h.File); ok {
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
					if t, ok := git.WorkTree(cwd); ok {
						d = t
					}
					if d != h.Proj {
						h.Moved = true
					}
				}
			}
		}
		if h.Moved || h.Old.Root == "" {
			if t, ok := git.WorkTree(cwd); ok {
				h.Root = t
			} else {
				h.Root = cwd
			}
		} else {
			// ...else wherever it was before.
			h.Root = h.Old.Root
		}
	}
	// Turns: each prompt of yours starts one.
	h.Turn = h.sess().Turn
	if h.yourPrompt() {
		h.Turn++
	}

	bg, _ := atoi(h.BgNew)
	ch := agent.Transition(h.Old, h.sess(), agent.Event{Name: h.Event, Async: h.Mode == "async", Source: h.Source, Ntype: h.Ntype,
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
	if os.Getenv("WEAVER_WT_MARKERS") != "0" && h.Marker != "" && h.Root != h.Proj && files.Exists(h.Root+"/.git") {
		h.MarkDir = h.Root
	}
	h.Marked = h.MarkDir
}

func (h *hk) oPid() string { return string(h.Old.Pid) }

// yourPrompt: is the event a prompt of yours, which starts a turn?
func (h *hk) yourPrompt() bool { return h.Event == "UserPromptSubmit" && h.Prompt != "" }

// sess is the stored state when it is this session's, else none.
func (h *hk) sess() agent.Record {
	if h.Same {
		return h.Old
	}
	return agent.Record{}
}

// writeState writes the state file from the hook's fields (under its lock).
func writeState(e *session.Env, h *hk) {
	pane, _ := atoi(string(h.Pane))
	since := h.Since
	if since == "" {
		since = "0"
	}
	st := agent.Record{Pane: jsonx.Int(pane), Agent: agent.Claude, State: h.State, TS: h.Now, Since: json.Number(since),
		Seen: h.Seen == "true", SessionID: h.Sid, ProcStart: h.ProcStart, Title: h.Title, Turn: h.Turn,
		PromptID: h.PromptID, Transcript: h.Transcript, Cwd: h.Cwd, Root: h.Root, StartCwd: h.Start, ProjectRoot: h.Proj,
		File: h.File, Prompt: h.Prompt, Waiting: h.Waiting, Pending: h.Pending, LastMessage: h.Last, MarkedRoot: h.Marked,
		Marker: h.Marker, BackgroundTasks: h.Bgdesc, DoneIDs: h.DoneIDs, EndReason: h.EndReason,
	}
	st.Files, st.Subagents, st.Tasks = orEmpty(h.Files), orEmpty(h.Subagents), orEmpty(h.Tasks)
	if p := knownPid(h.Pid); p != "" {
		st.Pid = json.Number(p)
	}
	if digits(h.Bg) {
		st.Background, _ = atoi(h.Bg)
	}
	if h.Doing != "" {
		st.Doing, st.DoingID = h.Doing, h.DoingID
	}
	// The life of the Rex session it runs in: a server or session made
	// again is another, however soon (session's lives.lost).
	st.Where = e.Where
	bestEffort(store.Write(h.sf, st))
}

// afterWrite: what a new session teaches its thread, following a Bash or
// MCP edit (async), and the background work when there is some.
func afterWrite(e *session.Env, h *hk) {
	if h.Event == "SessionStart" {
		e.ThreadLearns(h.Pane, h.Sid)
	}
	// Following a Bash or MCP edit needs git: here when this process runs in
	// the background anyway (Claude Code's async hook), else in the
	// background run, never while the agent waits on its hook.
	async := h.Mode == "async"
	if h.Event == "PostToolUse" && (h.Tool == "Bash" || strings.HasPrefix(h.Tool, "mcp__")) {
		if async {
			followBash(e, h)
		} else {
			h.FollowBash = "1"
		}
	}
	// The background work, only when there is some.
	if h.State != h.Old.State || h.Root != h.Old.Root || h.FollowBash != "" ||
		h.Waiting != h.Old.Waiting ||
		h.Title != h.sess().Title || h.Marker != h.Old.Marker || h.MarkDir != h.Old.MarkedRoot || h.Doing != h.sess().Doing ||
		h.Event == "SessionStart" || h.Event == "UserPromptSubmit" {
		spawnBg(e, h)
	}
}

// spawnBg starts `wvr hook --bg`, detached, with this run in its
// environment.
func spawnBg(e *session.Env, h *hk) {
	b, err := bgRun(h)
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
	// Not started, Rex catches up at the next event.
	bestEffort(c.Start())
}

// bgRun is h as the background run takes it ($WEAVER_BG, hookBg). The
// stored state's lists stay behind: the background run has the new ones,
// and its environment has room for so much. The files the call changed go
// only for it to follow them (FollowBash), and only so many: more (a
// formatter over a whole tree), and it asks git, as for a call that does
// not say.
func bgRun(h *hk) ([]byte, error) {
	bg := *h
	bg.Old.Files, bg.Old.Tasks, bg.Old.Subagents = nil, nil, nil
	if bg.FollowBash == "" || len(bg.Changed) > maxChangedHandedOn {
		bg.Changed, bg.ChangedKnown = nil, false
	}
	return json.Marshal(&bg)
}

// maxChangedHandedOn is how many changed files go with a background run.
const maxChangedHandedOn = 200

// HookMain is `wvr hook [--async|--bg]`.
func HookMain(e *session.Env, args []string) {
	mode := "sync"
	if len(args) > 0 {
		switch args[0] {
		case "--async":
			mode = "async"
		case "--bg":
			hookBg(e)
			return
		}
	}
	if e.MyPane == "" || e.Session == "" || cli.IsTTY(0) {
		return
	}
	in, _ := io.ReadAll(os.Stdin)
	me := os.Getenv("WEAVER_AGENT_PID")
	if me == "" {
		me = os.Getenv("CLAUDE_PID")
	}
	rc := Hook(e, in, mode, e.MyPane, me)
	if mode == "sync" {
		bell := os.Getenv("WEAVER_BELL")
		if bell == "" {
			bell = "1"
		}
		if bell == "1" && (rc == 10 || rc == 11) || bell == "needs-you" && rc == 10 {
			_, err := os.Stdout.WriteString(bellJSON + "\n")
			bestEffort(err) // a bell missed
		}
	}
}

// orEmpty is s, or an empty list for none: [] in JSON, not null.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
