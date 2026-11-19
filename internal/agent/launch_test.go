package agent

import (
	"strings"
	"testing"
)

// A review holds whatever command line runs the agent to auto mode: what
// would loosen it goes, auto mode is added once.
func TestCommand(t *testing.T) {
	for _, c := range []struct {
		base string
		m    Mode
		want string
	}{
		{"claude", ModeWork, "claude"},
		{"claude --model opus", ModeWork, "claude --model opus"},
		{"claude", ModeReview, "claude --permission-mode auto"},
		// A record from before Command: plan mode is in its command.
		{"claude --permission-mode plan", ModeReview, "claude --permission-mode auto"},
		{"claude --model opus --dangerously-skip-permissions --permission-mode acceptEdits", ModeReview,
			"claude --model opus --permission-mode auto"},
		{"claude --permission-mode=bypassPermissions", ModeReview, "claude --permission-mode auto"},
	} {
		if got := strings.Join(Command(c.base, c.m), " "); got != c.want {
			t.Errorf("Command(%q, %q) = %q, want %q", c.base, c.m, got, c.want)
		}
	}
	if got := Command("", ModeReview); len(got) != 0 {
		t.Errorf("empty base: %q", got)
	}
}
