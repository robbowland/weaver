package engine

import (
	"os"
	"strconv"
	"testing"

	"github.com/robbowland/weaver/internal/hooks"
	"github.com/robbowland/weaver/internal/rex/rextest"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
)

// An agent's pid of 1 or less (launchd, the parent of an orphaned hook; 0,
// no process) is no agent: never recorded, where it would read as dead and
// have the board retire a running agent. A real one is.
func TestHookRecordsNoUnknownPid(t *testing.T) {
	rextest.NoRex(t)
	me := strconv.Itoa(os.Getpid())
	for _, c := range []struct{ pid, want string }{{"1", ""}, {"0", ""}, {me, me}} {
		dir := t.TempDir()
		e := &session.Env{StateDir: dir + "/state", ClaudeDir: dir + "/claude", SockDir: dir + "/sock",
			Session: "s", SKey: "tmux-s", TSock: []string{"-S", dir + "/no-tmux.sock"}, Self: "/usr/bin/true"}
		hook(e, hooks.Input{Event: "SessionStart", SessionID: "conv", Cwd: dir, Source: "startup"}, "sync", "5", "claude", c.pid)
		st, err := store.ReadState(e.StateFile("5"))
		if err != nil {
			t.Fatalf("pid %s: no state: %v", c.pid, err)
		}
		if string(st.Pid) != c.want {
			t.Errorf("pid %s: recorded %q, want %q", c.pid, st.Pid, c.want)
		}
	}
}
