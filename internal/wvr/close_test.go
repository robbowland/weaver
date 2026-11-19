package wvr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/cli"
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

// closeCase sets up a thread to close (closing's) whose worktree weaver
// made, clean and merged into main (so nothing in it would be lost), on
// the sweep's list; its agent idle and with no pid, so the closing does not
// wait for it. Its worktree.
func closeCase(t *testing.T) (e *Env, p mux.PaneID, wt string) {
	t.Helper()
	ownWorktrunk(t)
	e, p, _, _ = closing(t)
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	repo, wt := dir+"/repo", dir+"/repo.feat"
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", repo},
		{"-C", repo, "-c", "user.email=t@e", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"},
		{"-C", repo, "worktree", "add", "-q", "-b", "feat", wt},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	th, _ := store.ReadThread(e.ThreadFile(p))
	th.Worktree, th.Repo, th.Branch, th.WorktreeCreated = wt, repo, "feat", true
	if err := store.Write(e.ThreadFile(p), th); err != nil {
		t.Fatal(err)
	}
	store.UpdateState(e.StateFile(p), func(r *agent.Record) { r.State, r.Pid = agent.StateIdle, "" })
	e.noteWorktree(wt, repo)
	return e, p, wt
}

// closeInTerminal runs `wvr close ARGS` on e in a terminal of its own
// (script's), as the board's x runs it in a popup, presses key once its
// card's buttons show (none: it asks nothing), and returns what Close
// returned ("<nil>" for nothing).
func closeInTerminal(t *testing.T, e *Env, key string, args ...string) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir() + "/returned"
	spec, _ := json.Marshal(closeProgram{Env: *e.Env, Args: args, Out: out})
	c := exec.Command("/usr/bin/script", "-q", "/dev/null", self, "-test.run=^TestCloseProgram$")
	c.Env = append(os.Environ(), "WVR_TEST_CLOSE="+string(spec), "FZF_COLUMNS=")
	in, err := c.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var screen bytes.Buffer
	c.Stdout = writerFunc(func(b []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return screen.Write(b)
	})
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close(); _ = c.Process.Kill(); _ = c.Wait() }()
	shown := func() string {
		mu.Lock()
		defer mu.Unlock()
		return reSGR.ReplaceAllString(screen.String(), "")
	}
	waitFor := func(what string, done func() bool) {
		t.Helper()
		for i := 0; !done(); i++ {
			if i == 100 {
				t.Fatalf("%s; the terminal shows:\n%s", what, shown())
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	if key != "" {
		// The buttons ask where the cursor is, as a terminal answers.
		waitFor("no card's buttons", func() bool { return strings.Contains(shown(), "\033[6n") })
		if _, err := io.WriteString(in, "\033[9;1R"); err != nil {
			t.Fatal(err)
		}
		waitFor("no card's buttons", func() bool { return strings.Contains(shown(), " n cancel ") })
		if _, err := io.WriteString(in, key); err != nil {
			t.Fatal(err)
		}
	}
	waitFor("close did not return after "+strconv.Quote(key), func() bool { return files.Exists(out) })
	b, _ := os.ReadFile(out)
	return string(b)
}

type closeProgram struct {
	Env  session.Env
	Args []string
	Out  string
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(b []byte) (int, error) { return f(b) }

// TestCloseProgram is the `wvr close` closeInTerminal runs.
func TestCloseProgram(t *testing.T) {
	spec := os.Getenv("WVR_TEST_CLOSE")
	if spec == "" {
		t.Skip("closeInTerminal runs it")
	}
	var c closeProgram
	if err := json.Unmarshal([]byte(spec), &c); err != nil {
		t.Fatal(err)
	}
	err := (&Env{&c.Env}).Close(c.Args)
	if err := os.WriteFile(c.Out, []byte(fmt.Sprint(err)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Closing a thread whose worktree may go is one card: y closes it and
// removes the worktree, k closes it and keeps the worktree for good (off
// the sweep's list), and n, esc or enter close nothing and leave the
// worktree to the sweep. --yes asks nothing and leaves the worktree to the
// sweep, unless --remove-worktree.
func TestCloseCard(t *testing.T) {
	cancelled := cli.ErrCancelled.Error()
	for _, c := range []struct {
		name, key string
		args      []string
		returned  string
		closed    bool
		removed   bool
		listed    bool
	}{
		{"enter cancels", "\r", nil, cancelled, false, false, true},
		{"esc cancels", "\x1b", nil, cancelled, false, false, true},
		{"n cancels", "n", nil, cancelled, false, false, true},
		{"k keeps the worktree", "k", nil, "<nil>", true, false, false},
		{"y removes it", "y", nil, "<nil>", true, true, false},
		{"--yes leaves it to the sweep", "", []string{"--yes"}, "<nil>", true, false, true},
		{"--yes --remove-worktree removes it", "", []string{"--yes", "--remove-worktree"}, "<nil>", true, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, p, wt := closeCase(t)
			got := closeInTerminal(t, e, c.key, append(c.args, string(p))...)
			closed, removed, listed := !files.Exists(e.ThreadFile(p)), !files.Exists(wt), hasDir(e.worktreesMade(), wt)
			if got != c.returned || closed != c.closed || removed != c.removed || listed != c.listed {
				t.Errorf("returned %q, closed %v, worktree removed %v, on the sweep's list %v; want %q, %v, %v, %v",
					got, closed, removed, listed, c.returned, c.closed, c.removed, c.listed)
			}
		})
	}
}
