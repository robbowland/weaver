package wvr

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/jsonx"
)

// Claude Code's hook input (and what `wvr event` makes of another agent's
// events, event.go), read once into the members weaver uses.

// hookInput is one hook event's input.
type hookInput struct {
	Event      string `json:"hook_event_name"`
	SessionID  string `json:"session_id"`
	Transcript string `json:"transcript_path"`
	Cwd        string `json:"cwd"`
	NewCwd     string `json:"new_cwd"` // CwdChanged
	// A subagent's events: which, and what kind.
	AgentID   string `json:"agent_id"`
	AgentType string `json:"agent_type"`
	// SessionStart's startup, resume, clear or compact; "user" or another
	// for a prompt (`wvr event`).
	Source   string `json:"source"`
	Prompt   string `json:"prompt"` // UserPromptSubmit
	PromptID string `json:"prompt_id"`
	Title    string `json:"session_title"`
	// A tool call's.
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	ToolUseID string          `json:"tool_use_id"`
	Interrupt bool            `json:"is_interrupt"` // PostToolUseFailure
	// PostToolBatch's calls.
	ToolCalls []struct {
		Response jsonx.Text `json:"tool_response"`
	} `json:"tool_calls"`
	// Notification's.
	Ntype   string `json:"notification_type"`
	Message string `json:"message"`
	// Stop's reply, and the background work still running; StopFailure's
	// error; SessionEnd's reason.
	LastMessage string `json:"last_assistant_message"`
	Background  []struct {
		Type        string `json:"type"`
		Description string `json:"description"`
	} `json:"background_tasks"`
	Error  jsonx.Text `json:"error"`
	Reason string     `json:"reason"`
	// TaskCreated's, TaskCompleted's.
	TaskID      jsonx.Text `json:"task_id"`
	TaskSubject string     `json:"task_subject"`
}

// toolInput is what weaver reads of a tool call's input.
type toolInput struct {
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
	Command      string `json:"command"` // Bash
	Questions    []struct {
		Question string `json:"question"`
	} `json:"questions"` // AskUserQuestion
}

func (in hookInput) input() toolInput {
	var t toolInput
	bestEffort(jsonx.Decode(in.ToolInput, &t))
	return t
}

// path is the file the tool call names, if any.
func (t toolInput) path() string {
	if t.FilePath != "" {
		return t.FilePath
	}
	return t.NotebookPath
}

// asyncGate: the async hook runs after every tool call; most of them change
// nothing.
func asyncGate(in hookInput, state agent.State) bool {
	switch in.Event {
	case "PostToolUse":
		t := in.ToolName
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
		return state != agent.StateWorking
	case "PostToolBatch":
		return state == agent.StateNeedsYou
	}
	return true
}

var (
	reCmdPath  = regexp.MustCompile("(?:^|[\\s=;&|(\"':])(/[^\\s\"';&|()<>`$*?:]+)")
	reYours    = regexp.MustCompile(`^\s*(<agent-message|<task-notification|<teammate-message|<channel|Another Claude session sent a message)`)
	reRejected = regexp.MustCompile(`doesn.t want to proceed with this tool use`)
)

// editFile is the file an edit tool wrote (absolute), else "".
func editFile(in hookInput) string {
	switch in.ToolName {
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
	default:
		return ""
	}
	if in.Event != "PostToolUse" {
		return ""
	}
	s := line(in.input().path())
	if s == "" || strings.HasPrefix(s, "/") {
		return s
	}
	return in.Cwd + "/" + s
}

// commandPaths are the absolute paths a Bash command names (cd targets,
// VAR=/path, arguments), or an MCP tool's input holds: where to look for
// what it changed. At most 8, sorted, RS-separated.
func commandPaths(in hookInput) string {
	var ps []string
	switch {
	case strings.HasPrefix(in.ToolName, "mcp__"):
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
		var v any
		bestEffort(json.Unmarshal(in.ToolInput, &v))
		walk(v)
	case in.ToolName == "Bash":
		for _, m := range reCmdPath.FindAllStringSubmatch(in.input().Command, -1) {
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
func summary(in hookInput) string {
	t := in.input()
	switch {
	case in.ToolName == "Bash":
		return "Bash: " + t.Command
	case in.ToolName == "AskUserQuestion":
		q := ""
		if len(t.Questions) > 0 {
			q = t.Questions[0].Question
		}
		return "question: " + q
	case t.path() != "":
		p := strings.Split(t.path(), "/")
		return in.ToolName + ": " + p[len(p)-1]
	}
	return in.ToolName
}

// yours is whether a prompt is yours, not another agent's message or a
// task's report.
func yours(in hookInput) bool {
	if in.Source != "" && in.Source != "user" {
		return false
	}
	return !reYours.MatchString(in.Prompt)
}

// background are the descriptions of the background subagents and
// workflows still running when the agent stops: it wakes when they report
// back.
func background(in hookInput) []string {
	var out []string
	for _, b := range in.Background {
		if b.Type == "subagent" || b.Type == "workflow" {
			out = append(out, b.Description)
		}
	}
	return out
}

// rejected is whether you said no to a call of the batch, without saying
// what to do instead.
func rejected(in hookInput) bool {
	if in.Event != "PostToolBatch" {
		return false
	}
	for _, c := range in.ToolCalls {
		r := string(c.Response)
		if reRejected.MatchString(r) && !strings.Contains(r, "the user said:") {
			return true
		}
	}
	return false
}

// filesNew is the files the agent edited, file last.
func filesNew(fs []string, file string) []string {
	if file == "" {
		return fs
	}
	out := []string{}
	for _, f := range fs {
		if f != file {
			out = append(out, f)
		}
	}
	out = append(out, file)
	if len(out) > 200 {
		out = out[len(out)-200:]
	}
	return out
}

// subsNew is the subagents running after the event (aid its subagent's).
func subsNew(subs []agent.Subagent, in hookInput, aid string, now int64) []agent.Subagent {
	without := func() []agent.Subagent {
		out := []agent.Subagent{}
		for _, s := range subs {
			if s.ID != aid {
				out = append(out, s)
			}
		}
		return out
	}
	switch in.Event {
	case "SubagentStart":
		if aid != "" {
			return append(without(), agent.Subagent{ID: aid, Type: one(in.AgentType, 40), Since: json.Number(strconv.FormatInt(now, 10))})
		}
	case "SubagentStop":
		return without()
	case "Stop":
		if len(background(in)) == 0 {
			return []agent.Subagent{}
		}
	case "SessionStart":
		if in.Source != "compact" {
			return []agent.Subagent{}
		}
	}
	return subs
}

// tasksNew is the agent's tasks after the event: the last 30.
func tasksNew(tasks []agent.Task, in hookInput) []agent.Task {
	id, subject := string(in.TaskID), one(in.TaskSubject, 80)
	last30 := func(a []agent.Task) []agent.Task {
		if len(a) > 30 {
			return a[len(a)-30:]
		}
		return a
	}
	switch in.Event {
	case "TaskCreated":
		out := []agent.Task{}
		for _, t := range tasks {
			if t.ID != id {
				out = append(out, t)
			}
		}
		return last30(append(out, agent.Task{ID: id, Subject: subject}))
	case "TaskCompleted":
		out := append([]agent.Task{}, tasks...)
		found := false
		for i := range out {
			if out[i].ID == id {
				out[i].Done, found = true, true
			}
		}
		if found {
			return out
		}
		return last30(append(out, agent.Task{ID: id, Subject: subject, Done: true}))
	case "SessionStart":
		if in.Source != "compact" && in.Source != "resume" {
			return []agent.Task{}
		}
	}
	return tasks
}

// orEmpty is s, or an empty list for none: [] in JSON, not null.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
