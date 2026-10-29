package session

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/jsonx"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/rex"
	"github.com/robbowland/weaver/internal/rex/rextest"
	"github.com/robbowland/weaver/internal/store"
)

// What gc and the lost check decide of a Rex session's threads: while the
// session runs they are neither lost nor retired (an agent that died in a
// pane still there is the board's to show), and its panes are looked at
// once a run however many agents ask; once it has gone they are kept as
// lost; while Rex does not answer, nothing is lost or retired.
func TestRexLostAndGC(t *testing.T) {
	stop := rextest.Server(t)
	dir := t.TempDir()
	e := &Env{StateDir: dir + "/state", LostDir: dir + "/lost", TSock: []string{"-S", dir + "/no-tmux.sock"}}
	for _, d := range []string{e.StateDir, e.LostDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sleep := []string{"/bin/sleep", "600"}
	s, err := rex.Start(e.RexDir(), "demo", "/", sleep)
	if err != nil {
		t.Fatal(err)
	}
	var shown []mux.PaneID
	for i := 0; i < 2; i++ {
		if _, p, _, err := s.OpenWindow(mux.NewWindow{Spawn: mux.Spawn{Argv: sleep}, Label: "t", At: -1, Background: true}); err != nil {
			t.Fatal(err)
		} else {
			shown = append(shown, p)
		}
	}
	ps, err := s.Panes()
	if err != nil || len(ps) != 3 {
		t.Fatalf("panes: %+v %v", ps, err)
	}
	shown = append(shown, ps[0].ID)
	life := s.Life()
	if life.Server == 0 || life.Session != s.ID {
		t.Fatalf("life: %+v", life)
	}
	skey := rex.Key("demo")
	e.Session, e.SKey, e.RexID = "demo", skey, s.ID
	now := time.Now().Unix()
	dead := strconv.Itoa(deadPid(t))

	// A thread in pane p as of ts: its record, and (pid: its agent's, ""
	// for none reported; "-" for no state at all) its agent's state, in
	// life where.
	where := life
	thread := func(p mux.PaneID, pid string, ts int64) {
		t.Helper()
		n, _ := strconv.Atoi(string(p))
		sid := "conv-" + string(p)
		f := e.PaneFile(p, "thread")
		if err := store.Write(f, store.Thread{Num: n, Name: "t" + string(p), Agent: agent.Claude, SessionID: sid}); err != nil {
			t.Fatal(err)
		}
		at := time.Unix(ts, 0)
		if err := os.Chtimes(f, at, at); err != nil {
			t.Fatal(err)
		}
		if pid == "-" {
			return
		}
		st := agent.Record{Pane: jsonx.Int(n), Agent: "claude", State: agent.StateIdle, TS: ts, SessionID: sid, Where: where}
		if pid != "" {
			st.Pid = json.Number(pid)
		}
		if err := store.Write(e.StateFile(p), st); err != nil {
			t.Fatal(err)
		}
	}
	kept := func(p mux.PaneID) bool { return files.Exists(e.PaneFile(p, "thread")) }
	lost := func(p mux.PaneID) bool {
		return files.Exists(filepath.Join(e.LostDir, skey+"_conv-"+string(p)+".json"))
	}
	check := func(when string, p mux.PaneID, wantKept, wantLost bool) {
		t.Helper()
		if k, l := kept(p), lost(p); k != wantKept || l != wantLost {
			t.Errorf("%s: pane %s kept %v, lost %v; want kept %v, lost %v", when, p, k, l, wantKept, wantLost)
		}
	}
	// rex as weaver runs it, each call logged.
	bin, err := exec.LookPath(rex.Bin())
	if err != nil {
		t.Fatal(err)
	}
	log := dir + "/rex.log"
	if err := os.MkdirAll(dir+"/bin", 0o755); err != nil {
		t.Fatal(err)
	}
	wrap := "#!/bin/sh\necho \"$*\" >> '" + log + "'\nexec '" + bin + "' \"$@\"\n"
	if err := os.WriteFile(dir+"/bin/rex", []byte(wrap), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+"/bin:"+os.Getenv("PATH"))

	// Running: agents that died in its panes are left; one whose pane has
	// gone is forgotten, not lost; a thread that has not reported, or
	// reported no pid, stays.
	for _, p := range shown {
		thread(p, dead, now)
	}
	thread("9001", dead, now)
	thread("9002", "-", now)
	thread("9003", "", now)
	if lost, known := e.serverLost(skey, now, life); lost || !known {
		t.Errorf("running: serverLost %v, known %v", lost, known)
	}
	os.Remove(log)
	e.gcAgents()
	for _, p := range shown {
		check("running", p, true, false)
	}
	check("running, its pane gone", "9001", false, false)
	check("running", "9002", true, false)
	check("running", "9003", true, false)
	b, _ := os.ReadFile(log)
	if n := strings.Count(string(b), "--autostart=false do "); n != 1 {
		t.Errorf("running: the session looked at %d times, want once\n%s", n, b)
	}

	// Gone: each thread is kept as lost.
	if err := s.Quit(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []mux.PaneID{"9001", "9003"} {
		thread(p, "", now-7200)
	}
	if lost, known := e.serverLost(skey, now, life); !lost || !known {
		t.Errorf("gone: serverLost %v, known %v", lost, known)
	}
	e.gcAgents()
	for _, p := range append(shown, "9001", "9002", "9003") {
		check("gone", p, false, true)
	}

	// Rex not answering. A life whose server has stopped is gone, asked or
	// not; one whose server runs on cannot be told, and nothing of it is
	// lost or retired.
	stop()
	if err := os.RemoveAll(e.LostDir); err != nil {
		t.Fatal(err)
	}
	if lost, known := e.serverLost(skey, now, life); !lost || !known {
		t.Errorf("its server stopped: serverLost %v, known %v", lost, known)
	}
	where = agent.Where{Server: os.Getpid(), Session: s.ID}
	if lost, known := e.serverLost(skey, now, where); lost || known {
		t.Errorf("no answer: serverLost %v, known %v", lost, known)
	}
	for _, p := range shown {
		thread(p, dead, now)
	}
	thread("9002", "-", now)
	thread("9003", "", now-7200)
	e.gcAgents()
	e.Retire(e.StateFile(shown[0]), dead)
	for _, p := range append(shown, "9002", "9003") {
		check("no answer", p, true, false)
	}
}
