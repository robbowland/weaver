package session

import (
	"fmt"
	"os"
	"os/exec"
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

// A thread whose state weaver 0.12 wrote (its session named tmux_session),
// its server gone since, is lost, not forgotten: read as no session, it
// was forgotten, and the sweep took its worktree. Upgrade from 0.12 only.
func TestLostFromWeaver012(t *testing.T) {
	rextest.NoRex(t)
	dir := t.TempDir()
	gone := exec.Command("true")
	if err := gone.Run(); err != nil {
		t.Fatal(err)
	}
	b := dir + "/rex-weaver_1614"
	for f, s := range map[string]string{
		b + ".json":   fmt.Sprintf(`{"pane":1614,"agent":"claude","state":"done","ts":%d,"session_id":"conv-1","server":%d,"tmux_session":"session:old"}`, time.Now().Unix(), gone.Process.Pid),
		b + ".thread": `{"num":1,"name":"demo/greet","worktree":"/w/repo.demo-greet"}`,
	} {
		if err := os.WriteFile(f, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if lost, known := (&Env{StateDir: dir}).newLives().lostCheck(b + ".json"); !lost || !known {
		t.Errorf("lost %v (known %v), want lost", lost, known)
	}
}
