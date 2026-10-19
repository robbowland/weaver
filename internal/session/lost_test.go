package session

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// An agent that last reported before the machine started lost its session,
// even when a server started since has its old server's pid and a session
// of its name.
func TestServerLostAcrossABoot(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux")
	}
	sock := t.TempDir() + "/tmux.sock"
	tm := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("tmux", append([]string{"-S", sock, "-f", "/dev/null"}, args...)...).Output()
		if err != nil {
			t.Fatalf("tmux %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	tm("new-session", "-d", "-s", "s")
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", sock, "kill-server").Run() })
	srv := tm("display-message", "-p", "-t", "=s:", "#{pid}")
	e := &Env{TSock: []string{"-S", sock}}

	if e.serverLost("tmux-s", float64(time.Now().Unix()), srv) {
		t.Error("its server runs on: lost")
	}
	if !e.serverLost("tmux-s", float64(BootTime()-60), srv) {
		t.Error("it reported before the machine started, on a server with the same pid: not lost")
	}
}
