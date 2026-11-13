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
	"github.com/robbowland/weaver/internal/process"
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
	stop := rextest.Server(t, rex.Bin())
	dir := t.TempDir()
	e := &Env{StateDir: dir + "/state", LostDir: dir + "/lost"}
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
		if _, p, _, err := s.OpenWindow(mux.NewWindow{Spawn: mux.Spawn{Argv: sleep}, Label: "t", At: 1 + i, Background: true}); err != nil {
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
		if err := store.Write(f, store.Thread{Num: n, Name: "t" + string(p), SessionID: sid}); err != nil {
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
	t.Setenv("REX_BIN_DIR", dir+"/bin")
	// Counted as the rex command's calls (over the control connection
	// there is no process to count; the looks are the same).
	t.Setenv("WEAVER_REX_WIRE", "0")

	// Running: agents that died in its panes are left; one whose pane has
	// gone is forgotten, not lost; a thread that has not reported, or
	// reported no pid, stays.
	for _, p := range shown {
		thread(p, dead, now)
	}
	thread("9001", dead, now)
	thread("9002", "-", now)
	thread("9003", "", now)
	if lost, known := e.newLives().lost(now, life); lost || !known {
		t.Errorf("running: lost %v, known %v", lost, known)
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
	if n := strings.Count(string(b), " session.list "); n != 1 {
		t.Errorf("running: Rex asked for its sessions %d times in one run, want once\n%s", n, b)
	}

	// Gone: each thread is kept as lost.
	if err := s.Quit(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []mux.PaneID{"9001", "9003"} {
		thread(p, "", now-7200)
	}
	if lost, known := e.newLives().lost(now, life); !lost || !known {
		t.Errorf("gone: lost %v, known %v", lost, known)
	}
	os.Remove(log)
	e.gcAgents()
	if b, _ := os.ReadFile(log); strings.Count(string(b), " session.list ") != 1 {
		t.Errorf("gone: Rex asked for its sessions %d times in one run, want once\n%s", strings.Count(string(b), " session.list "), b)
	}
	for _, p := range append(shown, "9001", "9002", "9003") {
		check("gone", p, false, true)
	}

	// Rex not answering. A life whose server has stopped is gone, asked or
	// not; one whose server runs on cannot be told, and nothing of it is
	// lost or retired, but what is from before the machine started, which
	// no session outlives.
	stop()
	if err := os.RemoveAll(e.LostDir); err != nil {
		t.Fatal(err)
	}
	if lost, known := e.newLives().lost(now, life); !lost || !known {
		t.Errorf("its server stopped: lost %v, known %v", lost, known)
	}
	where = agent.Where{Server: os.Getpid(), Session: s.ID}
	if lost, known := e.newLives().lost(now, where); lost || known {
		t.Errorf("no answer: lost %v, known %v", lost, known)
	}
	for _, p := range shown {
		thread(p, dead, now)
	}
	thread("9002", "-", now)
	thread("9003", "", now-7200)
	before := process.BootTime() - 7200
	thread("9101", dead, before)
	thread("9102", "", before)
	thread("9103", "-", before)
	e.gcAgents()
	e.Retire(e.StateFile(shown[0]), dead)
	for _, p := range append(shown, "9002", "9003") {
		check("no answer", p, true, false)
	}
	for _, p := range []mux.PaneID{"9101", "9102", "9103"} {
		check("no answer, from before the machine started", p, false, true)
	}
}

// A thread's window is labelled from its row, once: a label already
// right is left (no call), one that is not is put right, from the one look
// at the panes the rows took.
func TestThreadTabLabels(t *testing.T) {
	rextest.Server(t, rex.Bin())
	dir := t.TempDir()
	e := &Env{StateDir: dir + "/state", LostDir: dir + "/lost", SockDir: dir + "/sock"}
	if err := os.MkdirAll(e.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sleep := []string{"/bin/sleep", "600"}
	s, err := rex.Start(e.RexDir(), "demo", "/", sleep)
	if err != nil {
		t.Fatal(err)
	}
	e.Session, e.SKey, e.RexID = "demo", rex.Key("demo"), s.ID
	f := e.Mux()
	_, p, _, err := f.OpenSession("demo/t", mux.NewWindow{Spawn: mux.Spawn{Argv: sleep, Dir: "/"}, Label: "main", Background: true})
	if err != nil {
		t.Fatal(err)
	}
	n, _ := strconv.Atoi(string(p))
	pane := jsonx.Int(n)
	if err := store.Write(e.PaneFile(p, "thread"), store.Thread{Num: 3, Task: "say hello", Worktree: dir + "/repo", Pane: &pane, Created: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}
	label := func() string {
		ps, _ := f.Panes()
		q, _ := PaneIn(ps, p)
		return q.WindowName
	}
	want := ThreadLabel(3, "say hello", dir+"/repo")
	e.PushStatusAndLabels()
	if l := label(); l != want {
		t.Fatalf("label %q, want %q", l, want)
	}
	// Right already: left as it is, so a label set behind weaver's back
	// to the same text is not set again (nothing to tell apart but that
	// it costs no call, which the rows' one look allows).
	rows, _ := e.AgentRows()
	if len(rows) != 1 || rows[0].WindowName != want {
		t.Fatalf("rows: %+v", rows)
	}
	calls := countRex(t)
	e.PushStatusAndLabels()
	if n := calls(); n != 1 {
		t.Errorf("a push with every label right took %d calls to rex, want 1 (the one look)", n)
	}
	f.Label(rows[0].Window, "renamed by hand")
	e.PushStatusAndLabels()
	if l := label(); l != want {
		t.Errorf("label %q once renamed, want %q again", l, want)
	}
}

// countRex has rex run through a stand-in that counts its runs: what the
// returned func says, from then on.
func countRex(t *testing.T) func() int {
	t.Helper()
	d := t.TempDir()
	log := filepath.Join(d, "calls")
	sh := "#!/bin/sh\necho >>'" + log + "'\nexec '" + rex.Bin() + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(d, "rex"), []byte(sh), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REX_BIN_DIR", d)
	// Counted as the rex command's calls (over the control connection
	// there is no process to count; the looks are the same).
	t.Setenv("WEAVER_REX_WIRE", "0")
	return func() int {
		b, _ := os.ReadFile(log)
		n := strings.Count(string(b), "\n")
		_ = os.Remove(log)
		return n
	}
}

// A thread's agent killed in a pane that runs on is the thread closed, its
// session ended (retire), but only while its state still names that
// agent: one started in its pane since (wvr restart's, whose state names
// no pid till its pane takes the resume, or its first hook gives its own)
// keeps the thread, its session and its files.
func TestRetireRechecksItsAgent(t *testing.T) {
	rextest.Server(t, rex.Bin())
	dir := t.TempDir()
	e := &Env{StateDir: dir + "/state", LostDir: dir + "/lost"}
	if err := os.MkdirAll(e.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sleep := []string{"/bin/sleep", "600"}
	s, err := rex.Start(e.RexDir(), "demo", "/", sleep)
	if err != nil {
		t.Fatal(err)
	}
	e.Session, e.SKey, e.RexID = "demo", rex.Key("demo"), s.ID
	f := e.Mux()
	_, p, _, err := f.OpenSession("demo/t", mux.NewWindow{Spawn: mux.Spawn{Argv: sleep, Dir: "/"}, Label: "main", Background: true})
	if err != nil {
		t.Fatal(err)
	}
	ps, err := f.Panes()
	if err != nil {
		t.Fatal(err)
	}
	q, _ := PaneIn(ps, p)
	life := agent.Where{Server: s.Life().Server, Session: q.Session}
	n, _ := strconv.Atoi(string(p))
	pane := jsonx.Int(n)
	sf := e.StateFile(p)
	if err := store.Write(e.PaneFile(p, "thread"), store.Thread{Num: 1, Name: "t", Pane: &pane, SessionID: "conv"}); err != nil {
		t.Fatal(err)
	}
	dead := strconv.Itoa(deadPid(t))
	state := func(pid string) {
		t.Helper()
		if err := store.Write(sf, agent.Record{Pane: pane, Agent: agent.Claude, State: agent.StateIdle, TS: time.Now().Unix(),
			SessionID: "conv", Pid: json.Number(pid), Where: life}); err != nil {
			t.Fatal(err)
		}
	}
	running := func() bool {
		ps, err := f.Panes()
		_, ok := PaneIn(ps, p)
		return err == nil && ok
	}
	// Judged on the dead agent's pid, retired after a new agent took the
	// pane (its state names no pid): kept.
	state("")
	e.Retire(sf, dead)
	if !running() || !files.Exists(sf) || !files.Exists(e.PaneFile(p, "thread")) {
		t.Fatalf("a thread retired for an agent its pane no longer runs: session %v, state %v, record %v",
			running(), files.Exists(sf), files.Exists(e.PaneFile(p, "thread")))
	}
	// Its state still naming the dead agent: the thread closed.
	state(dead)
	e.Retire(sf, dead)
	if running() || files.Exists(sf) || files.Exists(e.PaneFile(p, "thread")) {
		t.Errorf("a thread whose agent was killed in its pane: session %v, state %v, record %v",
			running(), files.Exists(sf), files.Exists(e.PaneFile(p, "thread")))
	}
}

// Home closed on its own, its threads' sessions running on: their threads
// stay what they are, neither forgotten (an agent that exited in a pane
// kept, its session running) nor lost (one whose agent never reported),
// for a home made again to take them over.
func TestGCWithHomeClosedAlone(t *testing.T) {
	rextest.Server(t, rex.Bin())
	dir := t.TempDir()
	e := &Env{StateDir: dir + "/state", LostDir: dir + "/lost", SockDir: dir + "/sock"}
	for _, d := range []string{e.StateDir, e.LostDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sleep := []string{"/bin/sleep", "600"}
	home, err := rex.Start(e.RexDir(), "demo", "/", sleep)
	if err != nil {
		t.Fatal(err)
	}
	e.Session, e.SKey, e.RexID = "demo", rex.Key("demo"), home.ID
	f := e.Mux()
	var ps []mux.PaneID
	for range 2 {
		_, p, _, err := f.OpenSession("demo/t", mux.NewWindow{Spawn: mux.Spawn{Argv: sleep, Dir: "/"}, Label: "main", Background: true})
		if err != nil {
			t.Fatal(err)
		}
		ps = append(ps, p)
	}
	panes, err := f.Panes()
	if err != nil {
		t.Fatal(err)
	}
	q, _ := PaneIn(panes, ps[0])
	life := agent.Where{Server: home.Life().Server, Session: q.Session}
	for i, p := range ps {
		n, _ := strconv.Atoi(string(p))
		pane := jsonx.Int(n)
		if err := store.Write(e.PaneFile(p, "thread"), store.Thread{Num: i + 1, Name: "t", Pane: &pane, SessionID: "conv-" + string(p)}); err != nil {
			t.Fatal(err)
		}
	}
	// The first's agent has exited (dead), its pane kept; the second's
	// never reported.
	n, _ := strconv.Atoi(string(ps[0]))
	if err := store.Write(e.StateFile(ps[0]), agent.Record{Pane: jsonx.Int(n), Agent: agent.Claude, State: agent.StateIdle,
		TS: time.Now().Unix(), SessionID: "conv-" + string(ps[0]), Pid: json.Number(strconv.Itoa(deadPid(t))), Where: life}); err != nil {
		t.Fatal(err)
	}
	if err := home.Quit(); err != nil {
		t.Fatal(err)
	}
	e.GC()
	for _, p := range ps {
		if !files.Exists(e.PaneFile(p, "thread")) {
			t.Errorf("thread of pane %s: forgotten or lost, its session running", p)
		}
	}
	if ls, _ := os.ReadDir(e.LostDir); len(ls) != 0 {
		t.Errorf("%d threads lost, their sessions running", len(ls))
	}
	// A home made again takes them over (rex.Start): made after their
	// files, but their sessions were not.
	time.Sleep(1100 * time.Millisecond)
	again, err := rex.Start(e.RexDir(), "demo", "/", sleep)
	if err != nil {
		t.Fatal(err)
	}
	e.RexID = again.ID
	e.GC()
	for _, p := range ps {
		if !files.Exists(e.PaneFile(p, "thread")) {
			t.Errorf("thread of pane %s, taken over by a home made again: forgotten or lost", p)
		}
	}
	if ls, _ := os.ReadDir(e.LostDir); len(ls) != 0 {
		t.Errorf("%d threads lost once taken over, their sessions running", len(ls))
	}
}
