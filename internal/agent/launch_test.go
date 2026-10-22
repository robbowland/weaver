package agent

import (
	"strings"
	"testing"
)

// A review holds whatever command line runs the agent to reading only:
// what would loosen it goes, its own read-only mode is added once.
func TestCommand(t *testing.T) {
	for _, c := range []struct {
		k    Kind
		base string
		m    Mode
		want string
	}{
		{Claude, "claude", ModeWork, "claude"},
		{Claude, "claude --model opus", ModeWork, "claude --model opus"},
		{Claude, "claude", ModeReview, "claude --permission-mode plan"},
		// A record from before: its command already has the flag.
		{Claude, "claude --permission-mode plan", ModeReview, "claude --permission-mode plan"},
		{Claude, "claude --model opus --dangerously-skip-permissions --permission-mode acceptEdits", ModeReview,
			"claude --model opus --permission-mode plan"},
		{Claude, "claude --permission-mode=bypassPermissions", ModeReview, "claude --permission-mode plan"},
		{Codex, "codex", ModeReview, "codex --sandbox read-only"},
		{Codex, "codex --sandbox read-only", ModeReview, "codex --sandbox read-only"},
		{Codex, "codex --full-auto -s workspace-write -m o3", ModeReview, "codex -m o3 --sandbox read-only"},
		{Codex, "codex --dangerously-bypass-approvals-and-sandbox --sandbox=danger-full-access", ModeReview, "codex --sandbox read-only"},
		{Codex, "codex --full-auto", ModeWork, "codex --full-auto"},
	} {
		if got := strings.Join(Command(c.k, c.base, c.m), " "); got != c.want {
			t.Errorf("Command(%s, %q, %q) = %q, want %q", c.k, c.base, c.m, got, c.want)
		}
	}
	if got := Command(Claude, "", ModeReview); len(got) != 0 {
		t.Errorf("empty base: %q", got)
	}
}
