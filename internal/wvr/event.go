package wvr

import (
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// `wvr event` takes one event of any agent as normalized JSON (docs/
// design.md, Events) and turns it into the Claude Code hook input the state
// machine understands; `wvr adapt codex` translates Codex's own hook
// input into such an event first.

// normEvent is one event of any agent, normalized (docs/design.md, Events
// from other agents). Its text members may come as any JSON: a pid or a
// pane as a number.
type normEvent struct {
	Agent      jsonText `json:"agent"`
	Event      string   `json:"event"`
	Session    jsonText `json:"session"`
	Pid        jsonText `json:"pid"`
	Pane       jsonText `json:"pane"`
	Cwd        jsonText `json:"cwd"`
	Transcript jsonText `json:"transcript"`
	PromptID   jsonText `json:"prompt_id"`
	Title      jsonText `json:"title"`
	Source     jsonText `json:"source"` // session_start
	// prompt: the text, and false for a machine-injected one.
	Prompt  jsonText `json:"prompt"`
	User    *bool    `json:"user"`
	Waiting jsonText `json:"waiting"` // needs_you
	// tool_done: an edit's file, else its command and the paths it touched.
	Tool    jsonText   `json:"tool"`
	File    jsonText   `json:"file"`
	Command jsonText   `json:"command"`
	Paths   []jsonText `json:"paths"`
	Error   jsonText   `json:"error"`   // tool_failed, error
	Message jsonText   `json:"message"` // stop
	// subagent_start, subagent_stop.
	Subagent     jsonText `json:"subagent"`
	SubagentType jsonText `json:"subagent_type"`
}

// hookOf is the Claude Code hook input event n stands for; its Event is ""
// for none.
func hookOf(n normEvent) hookInput {
	h := hookInput{SessionID: string(n.Session), Transcript: string(n.Transcript), Cwd: string(n.Cwd),
		PromptID: string(n.PromptID), Title: string(n.Title)}
	tool := func(name string, input map[string]string) {
		h.ToolName = name
		h.ToolInput, _ = json.Marshal(input)
	}
	switch n.Event {
	case "session_start":
		h.Event, h.Source = "SessionStart", string(n.Source)
		if h.Source == "" {
			h.Source = "startup"
		}
	case "compact":
		h.Event, h.Source = "SessionStart", "compact"
	case "prompt":
		h.Event, h.Prompt, h.Source = "UserPromptSubmit", string(n.Prompt), "user"
		if n.User != nil && !*n.User {
			h.Source = "system"
		}
	case "needs_you":
		h.Event, h.Ntype, h.Message = "Notification", "permission_prompt", string(n.Waiting)
	case "answered":
		h.Event, h.Ntype = "Notification", "elicitation_response"
	case "tool_done":
		h.Event = "PostToolUse"
		if n.File != "" {
			tool("Edit", map[string]string{"file_path": string(n.File)})
			break
		}
		c := n.Command
		if c == "" {
			c = n.Tool
		}
		ps := make([]string, len(n.Paths))
		for i, p := range n.Paths {
			ps[i] = string(p)
		}
		tool("Bash", map[string]string{"command": string(c) + " " + strings.Join(ps, " ")})
	case "tool_failed":
		h.Event, h.Error = "PostToolUseFailure", n.Error
		tool(string(n.Tool), map[string]string{})
	case "interrupt":
		h.Event, h.Interrupt = "PostToolUseFailure", true
		tool(string(n.Tool), map[string]string{})
	case "stop":
		h.Event, h.LastMessage = "Stop", string(n.Message)
	case "error":
		h.Event, h.Error = "StopFailure", n.Error
	case "session_end":
		h.Event = "SessionEnd"
	case "subagent_start":
		h.Event, h.AgentID, h.AgentType = "SubagentStart", string(n.Subagent), string(n.SubagentType)
	case "subagent_stop":
		h.Event, h.AgentID, h.AgentType = "SubagentStop", string(n.Subagent), string(n.SubagentType)
	}
	return h
}

var rePatchFile = regexp.MustCompile(`\*\*\* (?:Update|Add) File: ([^\n]+)`)

// codexInput is Codex's hook input: Claude Code's, with a turn_id, its own
// tools, and no Notification.
type codexInput struct {
	Event       string          `json:"hook_event_name"`
	SessionID   jsonText        `json:"session_id"`
	TurnID      jsonText        `json:"turn_id"`
	Cwd         jsonText        `json:"cwd"`
	Transcript  jsonText        `json:"transcript_path"`
	Source      jsonText        `json:"source"`
	Prompt      jsonText        `json:"prompt"`
	ToolName    jsonText        `json:"tool_name"`
	ToolInput   json.RawMessage `json:"tool_input"`
	LastMessage jsonText        `json:"last_assistant_message"`
	AgentID     jsonText        `json:"agent_id"`
	AgentType   jsonText        `json:"agent_type"`
}

// codexTool is what weaver reads of a Codex tool call's input.
type codexTool struct {
	Command  jsonArgs `json:"command"`
	Cmd      jsonArgs `json:"cmd"`
	FilePath jsonText `json:"file_path"`
	Path     jsonText `json:"path"`
}

// jsonArgs is a command, as text or as its argv (joined by spaces).
type jsonArgs string

func (a *jsonArgs) UnmarshalJSON(b []byte) error {
	var argv []jsonText
	if json.Unmarshal(b, &argv) == nil {
		ps := make([]string, len(argv))
		for i, x := range argv {
			ps[i] = string(x)
		}
		*a = jsonArgs(strings.Join(ps, " "))
		return nil
	}
	var t jsonText
	bestEffort(t.UnmarshalJSON(b))
	*a = jsonArgs(t)
	return nil
}

// codex is the normalized event Codex's hook input stands for. Its tool
// names differ from Claude Code's, so any tool may have changed files.
func codex(c codexInput) normEvent {
	n := normEvent{Agent: "codex", Session: c.SessionID, Cwd: c.Cwd, Transcript: c.Transcript, PromptID: c.TurnID}
	var t codexTool
	bestEffort(decodeRecord(c.ToolInput, &t))
	cmd := t.Command
	if cmd == "" {
		cmd = t.Cmd
	}
	switch c.Event {
	case "SessionStart":
		n.Event, n.Source = "session_start", c.Source
	case "UserPromptSubmit":
		n.Event, n.Prompt = "prompt", c.Prompt
	case "PermissionRequest":
		w := string(cmd)
		for _, x := range []jsonText{t.FilePath, t.Path} {
			if w == "" {
				w = string(x)
			}
		}
		n.Event, n.Waiting = "needs_you", c.ToolName+": "+jsonText(w)
	case "PostToolUse":
		f := string(t.FilePath)
		if f == "" {
			f = string(t.Path)
		}
		if f == "" {
			// apply_patch: the first file its patch names.
			var walk func(v any)
			walk = func(v any) {
				switch x := v.(type) {
				case string:
					if f == "" {
						if m := rePatchFile.FindStringSubmatch(x); m != nil {
							f = m[1]
						}
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
			bestEffort(json.Unmarshal(c.ToolInput, &v))
			walk(v)
		}
		file := ""
		switch {
		case strings.HasPrefix(f, "/"):
			file = f
		case f != "":
			file = string(c.Cwd) + "/" + f
		}
		n.Event, n.Tool, n.File, n.Command = "tool_done", c.ToolName, jsonText(file), jsonText(cmd)
	case "Interrupt":
		n.Event = "interrupt"
	case "Stop":
		n.Event, n.Message = "stop", c.LastMessage
	case "SessionEnd":
		n.Event = "session_end"
	case "SubagentStart":
		n.Event, n.Subagent, n.SubagentType = "subagent_start", c.AgentID, c.AgentType
	case "SubagentStop":
		n.Event, n.Subagent, n.SubagentType = "subagent_stop", c.AgentID, c.AgentType
	case "PreCompact", "PostCompact":
		n.Event = "compact"
	}
	return n
}

// Event is `wvr event [--bell]` and `wvr adapt codex [--bell]`.
func (e *Env) Event(adapter string, bell bool) int {
	if isTTY(0) {
		os.Stderr.WriteString("weaver: usage: wvr event [--bell] < event.json\n")
		return 1
	}
	in, _ := io.ReadAll(os.Stdin)
	var n normEvent
	if adapter == "codex" {
		var c codexInput
		if decodeRecord(firstJSON(in), &c) != nil {
			return 0
		}
		n = codex(c)
	} else if decodeRecord(firstJSON(in), &n) != nil {
		return 0
	}
	strip := func(t jsonText) string {
		return strings.Map(func(r rune) rune {
			if r < 0x20 {
				return -1
			}
			return r
		}, string(t))
	}
	agent, pid, pane := strip(n.Agent), strip(n.Pid), strip(n.Pane)
	// An agent's name goes into the state file and nvim's events as it is.
	if agent == "" || agent[0] < 'a' || agent[0] > 'z' || strings.Trim(agent, "abcdefghijklmnopqrstuvwxyz0123456789_-") != "" {
		return 0
	}
	// A pane is digits: tmux's %3 and zellij's terminal_3 are pane 3.
	pane = strings.TrimPrefix(strings.TrimPrefix(pane, "terminal_"), "%")
	if !digits(pane) {
		pane = ""
	}
	if !digits(pid) {
		pid = ""
	}
	if pane == "" {
		pane = e.MyPane
	}
	if pane == "" || e.Session == "" {
		return 0
	}
	h := hookOf(n)
	if h.Event == "" {
		return 0
	}
	mode := "sync"
	if h.Event == "PostToolUse" || strings.HasPrefix(h.Event, "Subagent") {
		mode = "async"
	}
	if pid == "" {
		pid = os.Getenv("WEAVER_AGENT_PID")
	}
	// The agent process, once per session: the nearest ancestor that is
	// not a shell (an agent may run its hooks through one).
	if pid == "" && adapter != "" && h.Event == "SessionStart" {
		pid = agentAncestor()
	}
	rc := e.hook(h, mode, pane, agent, pid)
	if bell {
		bl := os.Getenv("WEAVER_BELL")
		if bl == "" {
			bl = "1"
		}
		if bl == "1" && (rc == 10 || rc == 11) || bl == "needs-you" && rc == 10 {
			if t, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
				t.WriteString("\a")
				t.Close()
			}
		}
	}
	return 0
}

// agentAncestor is the nearest ancestor of this process that is not a shell.
func agentAncestor() string {
	p := os.Getppid()
	for i := 0; i < 4; i++ {
		k, err := unix.SysctlKinfoProc("kern.proc.pid", p)
		if err != nil {
			return ""
		}
		comm := unix.ByteSliceToString(k.Proc.P_comm[:])
		switch comm {
		case "sh", "bash", "zsh", "dash", "fish", "-sh", "-bash", "-zsh", "-fish", "env":
			p = int(k.Eproc.Ppid)
		default:
			return strconv.Itoa(p)
		}
	}
	return ""
}
