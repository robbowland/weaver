package rex_test

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/rex"
	"github.com/robbowland/weaver/internal/rex/rextest"
)

// deadPid is the pid of a process that has exited.
func deadPid(t *testing.T) int {
	t.Helper()
	c := exec.Command("/usr/bin/true")
	if err := c.Run(); err != nil {
		t.Fatal(err)
	}
	return c.Process.Pid
}

// Whether a session's life has gone, from what Rex answers, call by call:
// a rex that answers server.status with $FAKE_PID and session.list with
// $FAKE_SESSIONS, and fails either while its variable is empty. Not known
// whenever a call it takes fails, the second as much as the first; gone
// without a call when the life's server has ended.
func TestLostAnswers(t *testing.T) {
	dir := rextest.NoRex(t)
	log := dir + "/calls"
	fake := "#!/bin/sh\necho \"$4\" >> '" + log + "'\n" + `case "$4" in
server.status) [ -n "$FAKE_PID" ] || exit 1; echo "{\"pid\":$FAKE_PID}" ;;
session.list) [ -n "$FAKE_SESSIONS" ] || exit 1; echo "$FAKE_SESSIONS" ;;
*) exit 1 ;;
esac
`
	if err := os.MkdirAll(dir+"/bin", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/bin/rex", []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REX_BIN_DIR", dir+"/bin")
	server, other := os.Getpid(), os.Getppid()
	life := agent.Where{Server: server, Session: "session:a"}
	listed := `{"sessions":[{"session_id":"session:a"}]}`
	for _, c := range []struct {
		what          string
		w             agent.Where
		pid, sessions string
		lost, known   bool
		calls         string
	}{
		{"no session", agent.Where{}, "", "", false, true, ""},
		{"its server ended", agent.Where{Server: deadPid(t), Session: "session:a"}, "", "", true, true, ""},
		{"no answer", life, "", "", false, false, "server.status"},
		{"the server answers, the list fails", life, strconv.Itoa(server), "", false, false, "server.status session.list"},
		{"another server runs", life, strconv.Itoa(other), listed, true, true, "server.status"},
		{"on its server", life, strconv.Itoa(server), listed, false, true, "server.status session.list"},
		{"gone from its server", life, strconv.Itoa(server), `{"sessions":[]}`, true, true, "server.status session.list"},
	} {
		t.Setenv("FAKE_PID", c.pid)
		t.Setenv("FAKE_SESSIONS", c.sessions)
		os.Remove(log)
		lost, known := new(rex.Answer).Lost(c.w)
		b, _ := os.ReadFile(log)
		calls := strings.Join(strings.Fields(string(b)), " ")
		if lost != c.lost || known != c.known || calls != c.calls {
			t.Errorf("%s: lost %v, known %v, asked %q; want %v, %v, %q", c.what, lost, known, calls, c.lost, c.known, c.calls)
		}
	}
}

// One Answer asks each thing once, however many lives it judges: the
// server's pid, then its sessions, only when a life needs them.
func TestAnswerAsksOnce(t *testing.T) {
	dir := rextest.NoRex(t)
	log := dir + "/calls"
	fake := "#!/bin/sh\necho \"$4\" >> '" + log + "'\n" + `case "$4" in
server.status) echo "{\"pid\":$FAKE_PID}" ;;
session.list) echo '{"sessions":[{"session_id":"session:a"}]}' ;;
*) exit 1 ;;
esac
`
	if err := os.MkdirAll(dir+"/bin", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/bin/rex", []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REX_BIN_DIR", dir+"/bin")
	t.Setenv("FAKE_PID", strconv.Itoa(os.Getpid()))
	var a rex.Answer
	if lost, known := a.Lost(agent.Where{Server: os.Getppid(), Session: "session:a"}); !lost || !known {
		t.Errorf("another server's life: lost %v, known %v", lost, known)
	}
	if b, _ := os.ReadFile(log); strings.Join(strings.Fields(string(b)), " ") != "server.status" {
		t.Errorf("another server's life asked %q, want only server.status", b)
	}
	for _, s := range []string{"session:a", "session:b", "session:a"} {
		a.Lost(agent.Where{Server: os.Getpid(), Session: s})
	}
	if _, running, answered := a.Keyed(t.TempDir(), "rex-a"); !running || !answered {
		t.Errorf("Keyed rex-a: running %v, answered %v", running, answered)
	}
	b, _ := os.ReadFile(log)
	if calls := strings.Join(strings.Fields(string(b)), " "); calls != "server.status session.list" {
		t.Errorf("asked %q, want each once: server.status session.list", calls)
	}
}

// On a Rex server of the test's own: a running session's life is not lost,
// a destroyed one's is, and once the server has stopped its life is gone
// with it, while what Rex alone could say is not known.
func TestLostServer(t *testing.T) {
	stop := rextest.Server(t, rex.Bin())
	s, err := rex.Start(t.TempDir(), "demo", "/", []string{"/bin/sleep", "600"})
	if err != nil {
		t.Fatal(err)
	}
	life := s.Life()
	if life.Server == 0 || life.Session != s.ID {
		t.Fatalf("life: %+v", life)
	}
	if lost, known := new(rex.Answer).Lost(life); lost || !known {
		t.Errorf("running: lost %v, known %v", lost, known)
	}
	if err := s.Quit(); err != nil {
		t.Fatal(err)
	}
	if lost, known := new(rex.Answer).Lost(life); !lost || !known {
		t.Errorf("destroyed: lost %v, known %v", lost, known)
	}
	stop()
	if lost, known := new(rex.Answer).Lost(life); !lost || !known {
		t.Errorf("its server stopped: lost %v, known %v", lost, known)
	}
	if lost, known := new(rex.Answer).Lost(agent.Where{Server: os.Getpid(), Session: s.ID}); lost || known {
		t.Errorf("no Rex answering: lost %v, known %v", lost, known)
	}
}
