package wv

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// `weaver event` takes one event of any agent as normalized JSON (docs/
// design.md, Events) and turns it into the Claude Code hook input the state
// machine understands; `weaver adapt codex` translates Codex's own hook
// input into such an event first.

func s(v any) string { return tostring(v) }

// normalized turns a normalized event into Claude Code hook input (the
// script's JQ_EVENT).
func normalized(n obj) map[string]any {
	h := map[string]any{
		"session_id": s(n.get("session", "")), "transcript_path": s(n.get("transcript", "")), "cwd": s(n.get("cwd", "")),
		"prompt_id": s(n.get("prompt_id", "")), "session_title": s(n.get("title", "")),
	}
	set := func(kv ...any) {
		for i := 0; i+1 < len(kv); i += 2 {
			h[kv[i].(string)] = kv[i+1]
		}
	}
	switch n["event"] {
	case "session_start":
		set("hook_event_name", "SessionStart", "source", n.get("source", "startup"))
	case "compact":
		set("hook_event_name", "SessionStart", "source", "compact")
	case "prompt":
		src := "user"
		if n["user"] == false {
			src = "system"
		}
		set("hook_event_name", "UserPromptSubmit", "prompt", s(n.get("prompt", "")), "source", src)
	case "needs_you":
		set("hook_event_name", "Notification", "notification_type", "permission_prompt", "message", s(n.get("waiting", "")))
	case "answered":
		set("hook_event_name", "Notification", "notification_type", "elicitation_response", "message", "")
	case "tool_done":
		if f := n.get("file", ""); f != "" {
			set("hook_event_name", "PostToolUse", "tool_name", "Edit", "tool_input", map[string]any{"file_path": s(f)})
		} else {
			var ps []string
			if a, ok := n.get("paths", []any{}).([]any); ok {
				for _, p := range a {
					ps = append(ps, s(p))
				}
			}
			c := firstOf(n.get("command", nil), n.get("tool", nil), "")
			set("hook_event_name", "PostToolUse", "tool_name", "Bash", "tool_input", map[string]any{"command": s(c) + " " + strings.Join(ps, " ")})
		}
	case "tool_failed":
		set("hook_event_name", "PostToolUseFailure", "tool_name", s(n.get("tool", "")), "tool_input", map[string]any{}, "error", s(n.get("error", "")))
	case "interrupt":
		set("hook_event_name", "PostToolUseFailure", "tool_name", s(n.get("tool", "")), "tool_input", map[string]any{}, "is_interrupt", true)
	case "stop":
		set("hook_event_name", "Stop", "last_assistant_message", s(n.get("message", "")))
	case "error":
		set("hook_event_name", "StopFailure", "error", s(n.get("error", "")))
	case "session_end":
		set("hook_event_name", "SessionEnd")
	case "subagent_start":
		set("hook_event_name", "SubagentStart", "agent_id", s(n.get("subagent", "")), "agent_type", s(n.get("subagent_type", "")))
	case "subagent_stop":
		set("hook_event_name", "SubagentStop", "agent_id", s(n.get("subagent", "")), "agent_type", s(n.get("subagent_type", "")))
	}
	return h
}

var rePatchFile = regexp.MustCompile(`\*\*\* (?:Update|Add) File: ([^\n]+)`)

// codex turns Codex's hook input into a normalized event (the script's
// JQ_CODEX). Its hooks mirror Claude Code's, without Notification; tool
// names differ, so any tool may have changed files.
func codex(c obj) obj {
	n := obj{"agent": "codex", "session": s(c.get("session_id", "")), "cwd": s(c.get("cwd", "")),
		"transcript": c.get("transcript_path", ""), "prompt_id": c.get("turn_id", "")}
	ti, _ := c["tool_input"].(map[string]any)
	cmdOf := func() string {
		v := firstOf(obj(ti).get("command", nil), obj(ti).get("cmd", nil), "")
		if a, ok := v.([]any); ok {
			var p []string
			for _, x := range a {
				p = append(p, s(x))
			}
			return strings.Join(p, " ")
		}
		return s(v)
	}
	switch c["hook_event_name"] {
	case "SessionStart":
		n["event"], n["source"] = "session_start", c.get("source", "startup")
	case "UserPromptSubmit":
		n["event"], n["prompt"] = "prompt", c.get("prompt", "")
	case "PermissionRequest":
		v := firstOf(obj(ti).get("command", nil), obj(ti).get("cmd", nil), obj(ti).get("file_path", nil), obj(ti).get("path", nil), "")
		w := s(v)
		if a, ok := v.([]any); ok {
			var p []string
			for _, x := range a {
				p = append(p, s(x))
			}
			w = strings.Join(p, " ")
		}
		n["event"], n["waiting"] = "needs_you", s(c.get("tool_name", ""))+": "+w
	case "PostToolUse":
		var f any = firstOf(obj(ti).get("file_path", nil), obj(ti).get("path", nil))
		if f == nil {
			var walk func(v any)
			walk = func(v any) {
				switch x := v.(type) {
				case string:
					if f == nil {
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
			walk(c["tool_input"])
		}
		if f == nil {
			f = ""
		}
		file := ""
		switch fs := s(f); {
		case strings.HasPrefix(fs, "/"):
			file = fs
		case fs != "":
			file = s(c.get("cwd", "")) + "/" + fs
		}
		n["event"], n["tool"], n["file"], n["command"] = "tool_done", c.get("tool_name", ""), file, cmdOf()
	case "Interrupt":
		n["event"] = "interrupt"
	case "Stop":
		n["event"], n["message"] = "stop", c.get("last_assistant_message", "")
	case "SessionEnd":
		n["event"] = "session_end"
	case "SubagentStart":
		n["event"], n["subagent"], n["subagent_type"] = "subagent_start", c.get("agent_id", ""), c.get("agent_type", "")
	case "SubagentStop":
		n["event"], n["subagent"], n["subagent_type"] = "subagent_stop", c.get("agent_id", ""), c.get("agent_type", "")
	case "PreCompact", "PostCompact":
		n["event"] = "compact"
	default:
		n["event"] = "none"
	}
	return n
}

// Event is `weaver event [--bell]` and `weaver adapt codex [--bell]`.
func (e *Env) Event(adapter string, bell bool) int {
	if isTTY(0) {
		os.Stderr.WriteString("weaver: usage: weaver event [--bell] < event.json\n")
		return 1
	}
	in, _ := io.ReadAll(os.Stdin)
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
	n := obj(m)
	if adapter == "codex" {
		n = codex(n)
	}
	strip := func(x any) string {
		return strings.Map(func(r rune) rune {
			if r < 0x20 {
				return -1
			}
			return r
		}, tostring(x))
	}
	agent, pid, pane := strip(n.get("agent", "")), strip(n.get("pid", "")), strip(n.get("pane", ""))
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
	h := normalized(n)
	if _, ok := h["hook_event_name"]; !ok {
		return 0
	}
	mode := "sync"
	if ev := tostring(h["hook_event_name"]); ev == "PostToolUse" || strings.HasPrefix(ev, "Subagent") {
		mode = "async"
	}
	if pid == "" {
		pid = os.Getenv("WEAVER_AGENT_PID")
	}
	// The agent process, once per session: the nearest ancestor that is
	// not a shell (an agent may run its hooks through one).
	if pid == "" && adapter != "" && h["hook_event_name"] == "SessionStart" {
		pid = agentAncestor()
	}
	b, _ := json.Marshal(h)
	rc := e.Hook(b, mode, pane, agent, pid)
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
