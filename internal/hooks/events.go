package hooks

import (
	"encoding/json"
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
