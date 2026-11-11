package hooks

import (
	"testing"

	"github.com/robbowland/weaver/internal/jsonx"
)

// Normalized events as hook input.
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

}
