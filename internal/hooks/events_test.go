package hooks

import (
	"testing"

	"github.com/robbowland/weaver/internal/jsonx"
)

// Normalized events as hook input; Codex's as normalized events.
func TestEventMapping(t *testing.T) {
	var n Normalized
	_ = jsonx.Decode([]byte(`{"agent":"pi","event":"tool_done","pid":123,"pane":"%4","command":"make","paths":["/a","/b"]}`), &n)
	if n.Pid != "123" || n.Pane != "%4" {
		t.Errorf("pid %q pane %q", n.Pid, n.Pane)
	}
	h := n.Hook()
	if h.Event != "PostToolUse" || h.ToolName != "Bash" || h.Tool().Command != "make /a /b" {
		t.Errorf("tool_done: %+v %s", h, h.ToolInput)
	}
	_ = jsonx.Decode([]byte(`{"agent":"pi","event":"prompt","prompt":"hi","user":false}`), &n)
	if h := n.Hook(); h.Event != "UserPromptSubmit" || h.Yours() {
		t.Errorf("a machine's prompt is not yours: %+v", h)
	}
	if h := (Normalized{Event: "nonsense"}).Hook(); h.Event != "" {
		t.Errorf("an unknown event: %q", h.Event)
	}
	for _, c := range []struct{ json, event, file, waiting string }{
		{`{"hook_event_name":"PostToolUse","cwd":"/r","tool_name":"apply_patch","tool_input":{"input":"*** Begin Patch\n*** Update File: x/y.go\n"}}`, "tool_done", "/r/x/y.go", ""},
		{`{"hook_event_name":"PermissionRequest","tool_name":"shell","tool_input":{"command":["git","push"]}}`, "needs_you", "", "shell: git push"},
		{`{"hook_event_name":"Notification"}`, "", "", ""},
	} {
		var in CodexInput
		_ = jsonx.Decode([]byte(c.json), &in)
		n := in.Normalized()
		if n.Event != c.event || string(n.File) != c.file || string(n.Waiting) != c.waiting || n.Agent != "codex" {
			t.Errorf("%s: %+v", c.json, n)
		}
	}
}
