package agent

import (
	"strings"
	"testing"
)

// A review holds whatever command line runs the agent to reading only:
// what would loosen it goes, its own read-only mode is added once.
func TestCommand(t *testing.T) {
	for _, c := range []struct {
		base string
		m    Mode
		want string
	}{
		{"claude", ModeWork, "claude"},
		{"claude --model opus", ModeWork, "claude --model opus"},
		{"claude", ModeReview, "claude --permission-mode plan"},
		// A record from before: its command already has the flag.
		{"claude --permission-mode plan", ModeReview, "claude --permission-mode plan"},
		{"claude --model opus --dangerously-skip-permissions --permission-mode acceptEdits", ModeReview,
			"claude --model opus --permission-mode plan"},
		{"claude --permission-mode=bypassPermissions", ModeReview, "claude --permission-mode plan"},
	} {
		if got := strings.Join(Command(c.base, c.m), " "); got != c.want {
			t.Errorf("Command(%q, %q) = %q, want %q", c.base, c.m, got, c.want)
		}
	}
	if got := Command("", ModeReview); len(got) != 0 {
		t.Errorf("empty base: %q", got)
	}
}
