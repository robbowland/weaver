package session

import (
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/process"
	"github.com/robbowland/weaver/internal/rex/rextest"
)

// An agent that last reported before the machine started lost its session,
// even when a server started since has its old server's pid and a session
// of its name; one of a key of another multiplexer's (tmux's, from before
// weaver ran on Rex only) never is, Rex answering or not.
func TestLostAcrossABoot(t *testing.T) {
	rextest.NoRex(t)
	e := &Env{StateDir: t.TempDir()}
	w := agent.Where{Session: "session:s"}
	if lost, known := e.newLives().lost("rex-s", process.BootTime()-60, w); !lost || !known {
		t.Errorf("it reported before the machine started: lost %v (known %v), want lost", lost, known)
	}
	if lost, known := e.newLives().lost("rex-s", time.Now().Unix(), w); lost || known {
		t.Errorf("Rex does not answer: lost %v (known %v), want not known", lost, known)
	}
	if lost, _ := e.newLives().lost("tmux-s", process.BootTime()-60, w); lost {
		t.Error("a tmux session's key: lost")
	}
}
