package hooks

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/robbowland/weaver/internal/jsonx"
)

// Normalized is one event of any agent, normalized (docs/design.md, Events
// from other agents). Its text members may come as any JSON: a pid or a
// pane as a number.
type Normalized struct {
	Agent      jsonx.Text `json:"agent"`
	Event      string     `json:"event"`
	Session    jsonx.Text `json:"session"`
	Pid        jsonx.Text `json:"pid"`
	Pane       jsonx.Text `json:"pane"`
	Cwd        jsonx.Text `json:"cwd"`
	Transcript jsonx.Text `json:"transcript"`
	PromptID   jsonx.Text `json:"prompt_id"`
	Title      jsonx.Text `json:"title"`
	Source     jsonx.Text `json:"source"` // session_start
	Reason     jsonx.Text `json:"reason"` // session_end: why it ended
	// prompt: the text, and false for a machine-injected one.
	Prompt  jsonx.Text `json:"prompt"`
	User    *bool      `json:"user"`
	Waiting jsonx.Text `json:"waiting"` // needs_you
	// needs_you, tool_done: which tool call (any text the same for one
	// call's dialog and its end), so a dialog's call ending is told from
	// another call ending while it waits.
	ToolKey jsonx.Text `json:"tool_key"`
	// tool_done: an edit's file, else its command and the paths it touched.
	Tool    jsonx.Text   `json:"tool"`
	File    jsonx.Text   `json:"file"`
	Command jsonx.Text   `json:"command"`
	Paths   []jsonx.Text `json:"paths"`
	Error   jsonx.Text   `json:"error"`   // tool_failed, error
	Message jsonx.Text   `json:"message"` // stop
	// subagent_start, subagent_stop.
	Subagent     jsonx.Text `json:"subagent"`
	SubagentType jsonx.Text `json:"subagent_type"`
}

// Hook is the Claude Code hook input event n stands for; its Event is ""
// for none.
func (n Normalized) Hook() Input {
	h := Input{SessionID: string(n.Session), Transcript: string(n.Transcript), Cwd: string(n.Cwd),
		PromptID: string(n.PromptID), Title: string(n.Title), CallKey: string(n.ToolKey)}
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
		h.Event, h.Reason = "SessionEnd", string(n.Reason)
	case "subagent_start":
		h.Event, h.AgentID, h.AgentType = "SubagentStart", string(n.Subagent), string(n.SubagentType)
	case "subagent_stop":
		h.Event, h.AgentID, h.AgentType = "SubagentStop", string(n.Subagent), string(n.SubagentType)
	}
	return h
}

var rePatchFile = regexp.MustCompile(`\*\*\* (?:Update|Add) File: ([^\n]+)`)

// CodexInput is Codex's hook input: Claude Code's, with a turn_id, its own
// tools, and no Notification.
type CodexInput struct {
	Event       string          `json:"hook_event_name"`
	SessionID   jsonx.Text      `json:"session_id"`
	TurnID      jsonx.Text      `json:"turn_id"`
	Cwd         jsonx.Text      `json:"cwd"`
	Transcript  jsonx.Text      `json:"transcript_path"`
	Source      jsonx.Text      `json:"source"`
	Reason      jsonx.Text      `json:"reason"`
	Prompt      jsonx.Text      `json:"prompt"`
	ToolName    jsonx.Text      `json:"tool_name"`
	ToolInput   json.RawMessage `json:"tool_input"`
	LastMessage jsonx.Text      `json:"last_assistant_message"`
	AgentID     jsonx.Text      `json:"agent_id"`
	AgentType   jsonx.Text      `json:"agent_type"`
}

// codexTool is what weaver reads of a Codex tool call's input.
type codexTool struct {
	Command  jsonArgs   `json:"command"`
	Cmd      jsonArgs   `json:"cmd"`
	FilePath jsonx.Text `json:"file_path"`
	Path     jsonx.Text `json:"path"`
}

// jsonArgs is a command, as text or as its argv (joined by spaces).
type jsonArgs string

func (a *jsonArgs) UnmarshalJSON(b []byte) error {
	var argv []jsonx.Text
	if json.Unmarshal(b, &argv) == nil {
		ps := make([]string, len(argv))
		for i, x := range argv {
			ps[i] = string(x)
		}
		*a = jsonArgs(strings.Join(ps, " "))
		return nil
	}
	var t jsonx.Text
	_ = t.UnmarshalJSON(b) // Text reads any value
	*a = jsonArgs(t)
	return nil
}

// Normalized is the normalized event Codex's hook input stands for. Its tool
// names differ from Claude Code's, so any tool may have changed files.
func (c CodexInput) Normalized() Normalized {
	n := Normalized{Agent: "codex", Session: c.SessionID, Cwd: c.Cwd, Transcript: c.Transcript, PromptID: c.TurnID}
	// One call's dialog and its end name it alike: its tool and input.
	if c.ToolName != "" {
		ti := "null"
		if len(c.ToolInput) > 0 {
			ti = jsonx.Canon(c.ToolInput)
		}
		n.ToolKey = c.ToolName + ":" + jsonx.Text(ti)
	}
	var t codexTool
	_ = jsonx.Decode(c.ToolInput, &t) // none, or not an object: nothing
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
		for _, x := range []jsonx.Text{t.FilePath, t.Path} {
			if w == "" {
				w = string(x)
			}
		}
		n.Event, n.Waiting = "needs_you", c.ToolName+": "+jsonx.Text(w)
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
			_ = json.Unmarshal(c.ToolInput, &v) // none: nothing to walk
			walk(v)
		}
		file := ""
		switch {
		case strings.HasPrefix(f, "/"):
			file = f
		case f != "":
			file = string(c.Cwd) + "/" + f
		}
		n.Event, n.Tool, n.File, n.Command = "tool_done", c.ToolName, jsonx.Text(file), jsonx.Text(cmd)
	case "Interrupt":
		n.Event = "interrupt"
	case "Stop":
		n.Event, n.Message = "stop", c.LastMessage
	case "SessionEnd":
		n.Event, n.Reason = "session_end", c.Reason
	case "SubagentStart":
		n.Event, n.Subagent, n.SubagentType = "subagent_start", c.AgentID, c.AgentType
	case "SubagentStop":
		n.Event, n.Subagent, n.SubagentType = "subagent_stop", c.AgentID, c.AgentType
	case "PreCompact", "PostCompact":
		n.Event = "compact"
	}
	return n
}
