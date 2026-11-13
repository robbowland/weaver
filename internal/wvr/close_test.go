package wvr

import (
	"encoding/json"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/jsonx"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/rex"
	"github.com/robbowland/weaver/internal/rex/rextest"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
)

// closing sets up a thread to close: home, the thread's session, its
// record, and its agent's state naming pid (an agent of the test's own,
// running out of the session, which the test ends as the session's end
// would end the real one).
func closing(t *testing.T) (e *Env, p mux.PaneID, sid string, agentProc *exec.Cmd) {
	t.Helper()
	rextest.Server(t, rex.Bin())
	dir := t.TempDir()
	se := &session.Env{StateDir: dir + "/state", LostDir: dir + "/lost", SockDir: dir + "/sock", Self: "/usr/bin/true"}
	for _, d := range []string{se.StateDir, se.LostDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sleep := []string{"/bin/sleep", "600"}
	home, err := rex.Start(se.RexDir(), "demo", "/", sleep)
	if err != nil {
		t.Fatal(err)
	}
	se.Session, se.SKey, se.RexID, se.RexHere = "demo", rex.Key("demo"), home.ID, home.ID
	e = &Env{se}
	_, p, _, err = e.Mux().OpenSession("demo/t", mux.NewWindow{Spawn: mux.Spawn{Argv: sleep, Dir: "/"}, Label: "main", Background: true})
	if err != nil {
		t.Fatal(err)
	}
	ps, _ := e.Mux().Panes()
	q, _ := session.PaneIn(ps, p)
	sid = q.Session
	n, _ := strconv.Atoi(string(p))
	pane := jsonx.Int(n)
	if err := store.Write(e.ThreadFile(p), store.Thread{Num: 1, Name: "t", Pane: &pane, SessionID: "conv", Created: 77}); err != nil {
		t.Fatal(err)
	}
	agentProc = exec.Command("/bin/sleep", "600")
	if err := agentProc.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = agentProc.Process.Kill(); _ = agentProc.Wait() })
	if err := store.Write(e.StateFile(p), agent.Record{Pane: pane, Agent: agent.Claude, State: agent.StateWorking, TS: time.Now().Unix(),
		SessionID: "conv", Pid: json.Number(strconv.Itoa(agentProc.Process.Pid)), Where: agent.Where{Server: home.Life().Server, Session: sid}}); err != nil {
		t.Fatal(err)
	}
	return e, p, sid, agentProc
}

// endsAgent is what the session's end does to its agent: its SessionEnd
// ("other", a hangup) recorded, and it gone.
func endsAgent(t *testing.T, e *Env, p mux.PaneID, c *exec.Cmd) {
	t.Helper()
	store.UpdateState(e.StateFile(p), func(r *agent.Record) { r.State, r.EndReason = agent.StateEnded, "other" })
	_ = c.Process.Kill()
	_ = c.Wait()
}

func lostRecords(t *testing.T, e *Env) []store.Lost {
	t.Helper()
	fs, _ := os.ReadDir(e.LostDir)
	var out []store.Lost
	for _, f := range fs {
		l, err := store.ReadLost(e.LostDir + "/" + f.Name())
		if err == nil {
			out = append(out, l)
		}
	}
	return out
}

// A thread closed before its agent reported (its card read no pid), whose
// agent then started: closed, never kept as lost to come back.
func TestCloseBeforeTheFirstHook(t *testing.T) {
	e, p, sid, c := closing(t)
	if err := e.CloseTail([]string{string(p), "", "", "", "0", "/", "/", "", sid}); err != nil {
		t.Fatal(err)
	}
	endsAgent(t, e, p, c)
	e.AgentRows()
	if ls := lostRecords(t, e); len(ls) != 0 || files.Exists(e.ThreadFile(p)) {
		t.Errorf("closed: %d lost records, record kept %v; want none", len(ls), files.Exists(e.ThreadFile(p)))
	}
}

// A board refreshing as the closing ends the thread's session (its
// agent's SessionEnd pushes it) does not keep the thread as lost; one you
// held stays held, with your note.
func TestCloseRacesARefresh(t *testing.T) {
	for _, held := range []bool{false, true} {
		e, p, sid, c := closing(t)
		if held && !e.Env.Hold(e.StateFile(p), "next: the docs") {
			t.Fatal("not held")
		}
		done := make(chan error, 1)
		go func() {
			done <- e.CloseTail([]string{string(p), "", strconv.Itoa(c.Process.Pid), "", "0", "/", "/", "", sid})
		}()
		// The session ended, the tail waiting for its agent: the hangup's
		// SessionEnd, and a board's refresh.
		for i := 0; ; i++ {
			if ps, err := e.Mux().Panes(); err == nil {
				if _, ok := session.PaneIn(ps, p); !ok {
					break
				}
			}
			if i == 50 {
				t.Fatal("the thread's session did not end")
			}
			time.Sleep(50 * time.Millisecond)
		}
		store.UpdateState(e.StateFile(p), func(r *agent.Record) { r.State, r.EndReason = agent.StateEnded, "other" })
		e.AgentRows()
		endsAgent(t, e, p, c)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		ls := lostRecords(t, e)
		switch {
		case held && (len(ls) != 1 || ls[0].Held == 0 || ls[0].Note != "next: the docs"):
			t.Errorf("held, then closed: %+v, want it held with its note", ls)
		case !held && len(ls) != 0:
			t.Errorf("closed: %d lost records, want none", len(ls))
		}
		if files.Exists(e.ThreadFile(p)) || files.Exists(e.StateFile(p)) {
			t.Errorf("closed (held %v): its files stay", held)
		}
	}
}
