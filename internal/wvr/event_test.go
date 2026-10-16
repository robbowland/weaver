package wvr

import "testing"

// Normalized events as hook input; Codex's as normalized events.
func TestEventMapping(t *testing.T) {
	var n normEvent
	bestEffort(decodeRecord([]byte(`{"agent":"pi","event":"tool_done","pid":123,"pane":"%4","command":"make","paths":["/a","/b"]}`), &n))
	if n.Pid != "123" || n.Pane != "%4" {
		t.Errorf("pid %q pane %q", n.Pid, n.Pane)
	}
	h := hookOf(n)
	if h.Event != "PostToolUse" || h.ToolName != "Bash" || h.input().Command != "make /a /b" {
		t.Errorf("tool_done: %+v %s", h, h.ToolInput)
	}
	bestEffort(decodeRecord([]byte(`{"agent":"pi","event":"prompt","prompt":"hi","user":false}`), &n))
	if h := hookOf(n); h.Event != "UserPromptSubmit" || yours(h) {
		t.Errorf("a machine's prompt is not yours: %+v", h)
	}
	if h := hookOf(normEvent{Event: "nonsense"}); h.Event != "" {
		t.Errorf("an unknown event: %q", h.Event)
	}
	for _, c := range []struct{ json, event, file, waiting string }{
		{`{"hook_event_name":"PostToolUse","cwd":"/r","tool_name":"apply_patch","tool_input":{"input":"*** Begin Patch\n*** Update File: x/y.go\n"}}`, "tool_done", "/r/x/y.go", ""},
		{`{"hook_event_name":"PermissionRequest","tool_name":"shell","tool_input":{"command":["git","push"]}}`, "needs_you", "", "shell: git push"},
		{`{"hook_event_name":"Notification"}`, "", "", ""},
	} {
		var in codexInput
		bestEffort(decodeRecord([]byte(c.json), &in))
		n := codex(in)
		if n.Event != c.event || string(n.File) != c.file || string(n.Waiting) != c.waiting || n.Agent != "codex" {
			t.Errorf("%s: %+v", c.json, n)
		}
	}
}
