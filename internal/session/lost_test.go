package session

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/agent"
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
	srv, _ := strconv.Atoi(tm("display-message", "-p", "-t", "=s:", "#{pid}"))
	w := agent.Where{Server: srv, Session: tm("display-message", "-p", "-t", "=s:", "#{session_id}")}
	e := &Env{TSock: []string{"-S", sock}}

	if lost, _ := e.serverLost("tmux-s", time.Now().Unix(), w); lost {
		t.Error("its server runs on: lost")
	}
	if lost, _ := e.serverLost("tmux-s", BootTime()-60, w); !lost {
		t.Error("it reported before the machine started, on a server with the same pid: not lost")
	}
}

// A session quit and made again on a server that stayed (another session
// kept it running) is another life of it: what ran in the old one is lost,
// what runs in the new one is not.
func TestServerLostSessionMadeAgain(t *testing.T) {
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
	tm("new-session", "-d", "-s", "keep")
	tm("new-session", "-d", "-s", "s")
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", sock, "kill-server").Run() })
	srv, _ := strconv.Atoi(tm("display-message", "-p", "-t", "=s:", "#{pid}"))
	old := agent.Where{Server: srv, Session: tm("display-message", "-p", "-t", "=s:", "#{session_id}")}
	then := time.Now().Unix() - 5
	e := &Env{TSock: []string{"-S", sock}}
	tm("kill-session", "-t", "=s")
	time.Sleep(1100 * time.Millisecond)
	tm("new-session", "-d", "-s", "s")
	now := agent.Where{Server: srv, Session: tm("display-message", "-p", "-t", "=s:", "#{session_id}")}

	if lost, _ := e.serverLost("tmux-s", then, old); !lost {
		t.Error("its session was quit and made again: not lost")
	}
	if lost, _ := e.serverLost("tmux-s", then, agent.Where{Server: srv}); !lost {
		t.Error("an older state (no session id), from before the session was made again: not lost")
	}
	if lost, _ := e.serverLost("tmux-s", time.Now().Unix(), now); lost {
		t.Error("the session it runs in: lost")
	}
}
