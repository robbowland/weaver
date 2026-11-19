package wvr

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/rex"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
)

// Home is the window weaver placed at 0 that shows no view, whatever its
// label says: renamed, it is still home, and another window labelled home
// is not.
func TestIsHome(t *testing.T) {
	for w, want := range map[mux.Window]bool{
		{Name: "home", Index: 0}:                 true,
		{Name: "renamed", Index: 0}:              true,
		{Name: "home", Index: -1}:                false,
		{Name: "home", Index: 900, View: "home"}: false,
	} {
		if got := isHome(w); got != want {
			t.Errorf("isHome(%+v) = %v, want %v", w, got, want)
		}
	}
}

// A UI lock left by a press killed before it wrote when it took it (its
// directory alone) goes stale as one that says when does: never holding
// alt+1 off for good.
func TestUILockLeftWithoutItsTime(t *testing.T) {
	dir := t.TempDir()
	e := &Env{&session.Env{StateDir: dir, SKey: "rex-w"}}
	d := dir + "/rex-w.ui"
	if err := os.Mkdir(d, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(d, old, old); err != nil {
		t.Fatal(err)
	}
	release, ok := e.tryUILock()
	if !ok {
		t.Fatal("a lock left a minute ago without its time holds the UI")
	}
	release()
	// One just taken (its time not written yet) holds.
	if err := os.Mkdir(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.tryUILock(); ok {
		t.Error("a lock just taken did not hold")
	}
}

// _pos puts a board's cursor on a thread's row among the rows the board
// shows (those it last loaded), leaving its keys and what its refresh
// compares with as they are: new rows of its own would put the cursor by
// rows not on screen, and have the refresh find nothing changed.
func TestPosInTheRowsShown(t *testing.T) {
	d := t.TempDir()
	t.Setenv("FZF_SOCK", d+"/b.sock")
	shown := "h0\t\t\t\t2\tNEEDS YOU\n\t\t\t\t\t\na4\t4\t3\t\t\tx\t01 x\na7\t7\t6\t\t\ty\t02 y\n"
	if err := os.WriteFile(d+"/b.rows", []byte(shown), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(d+"/b.keys", []byte("keys\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := &Env{&session.Env{StateDir: d + "/state", SKey: "rex-w"}}
	if got := e.Pos("7"); got != "pos(4)\n" {
		t.Errorf("Pos(7) = %q, want pos(4), its row as shown", got)
	}
	if got := e.Pos("9"); got != "" {
		t.Errorf("Pos of a pane with no row: %q", got)
	}
	if b, _ := os.ReadFile(d + "/b.rows"); string(b) != shown {
		t.Errorf("the rows shown, rewritten: %q", b)
	}
	if b, _ := os.ReadFile(d + "/b.keys"); string(b) != "keys\n" {
		t.Errorf("the board's keys, rewritten: %q", b)
	}
}

// messages are the labels of the messages session sid shows now (Say's
// floating layers).
func messages(t *testing.T, sid string) string {
	t.Helper()
	out, err := exec.Command(rex.Bin(), "--autostart=false", "api", "call", "session.view", `{"session_id":"`+sid+`"}`).Output()
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Windows []struct {
			Layers []struct {
				Kind   string `json:"kind"`
				Blocks []struct {
					Label string `json:"label"`
				} `json:"blocks"`
			} `json:"layers"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatal(err)
	}
	var ls []string
	for _, w := range v.Windows {
		for _, l := range w.Layers {
			for _, b := range l.Blocks {
				if l.Kind == "floating" {
					ls = append(ls, b.Label)
				}
			}
		}
	}
	return strings.Join(ls, "|")
}

// alt+1 comes back: its first jump keeps where you were, and once nothing
// else needs you it takes you back there, saying so where you land. A
// press made elsewhere than where the run left you starts afresh, and a
// run an hour old is forgotten.
func TestNextComesBack(t *testing.T) {
	e, p, sid, _ := closing(t)
	home := e.RexHere
	need := func(st agent.State) {
		store.UpdateState(e.StateFile(p), func(r *agent.Record) { r.State, r.Since = st, json.Number(strconv.FormatInt(time.Now().Unix(), 10)) })
	}
	focused := func() mux.PaneID {
		m := e.Mux()
		m.Here = sid
		q, _ := m.Active()
		return q
	}
	ws, _ := e.Mux().Windows()
	var homeWin mux.WindowID
	for _, w := range ws {
		if isHome(w) {
			homeWin = w.ID
		}
	}

	// From home, to the thread that needs you: where you were is kept.
	need(agent.StateNeedsYou)
	e.Next()
	b, _ := files.FirstLine(e.backFile())
	if want := fmt.Sprintf("%s %s %s ", homeWin, home, sid); focused() != p || !strings.HasPrefix(b, want) {
		t.Fatalf("first jump: on %s, kept %q; want on %s, kept %q…", focused(), b, p, want)
	}
	// Answered, and alt+1 pressed there: nothing else needs you, so home.
	need(agent.StateIdle)
	e.RexHere = sid
	e.Next()
	if got, want := messages(t, home), "weaver: nothing else needs you: back to home"; got != want || files.Exists(e.backFile()) {
		t.Errorf("nothing else: home said %q, kept %v; want %q, and the place forgotten", got, files.Exists(e.backFile()), want)
	}
	// Once used, it is gone: alt+1 says no agent needs you, as before.
	e.RexHere = home
	e.Next()
	if got, want := messages(t, home), "weaver: no agent needs you"; got != want {
		t.Errorf("after coming back: %q, want %q", got, want)
	}

	// A press from elsewhere than where the run left you (you went home by
	// hand): no way back, and the run forgotten.
	need(agent.StateNeedsYou)
	e.Next()
	need(agent.StateIdle)
	e.Next()
	if got, want := messages(t, home), "weaver: no agent needs you"; got != want || files.Exists(e.backFile()) {
		t.Errorf("run left: home said %q, kept %v; want %q, and nothing kept", got, files.Exists(e.backFile()), want)
	}

	// A run whose last jump was over an hour ago: forgotten.
	need(agent.StateNeedsYou)
	e.Next()
	need(agent.StateIdle)
	old := strings.Fields(func() string { l, _ := files.FirstLine(e.backFile()); return l }())
	if len(old) != 4 {
		t.Fatalf("no place kept: %q", old)
	}
	e.writeBack(backPlace{from: mux.WindowID(old[0]), fromSession: old[1], to: old[2], at: time.Now().Unix() - backStale - 1})
	e.RexHere = sid
	e.Next()
	if got, want := messages(t, sid), "weaver: no agent needs you"; got != want || files.Exists(e.backFile()) {
		t.Errorf("an hour on: the thread said %q, kept %v; want %q, and the place forgotten", got, files.Exists(e.backFile()), want)
	}
}

// The way back names where it goes as alt+1's jumps name a thread: home, a
// view, a thread's number and name, a helper by its thread and window.
func TestPlaceName(t *testing.T) {
	rows := []session.Row{
		{Window: "5", Tnum: 3, Title: "Fix the tests"},
		{Window: "6", Thread: 3, WindowName: "reviews"},
	}
	for w, want := range map[mux.Window]string{
		{ID: "1", Index: 0}: "home",
		{ID: "2", Index: 900, View: "github", Name: "GH"}:   "github",
		{ID: "5", Index: 1, Name: "03 Fix the tests · r/b"}: "03 Fix the tests",
		{ID: "6", Index: 2, Name: "reviews"}:                "03 › reviews",
		{ID: "7", Index: 3, Name: "notes"}:                  "notes",
	} {
		if got := placeName(w, rows); got != want {
			t.Errorf("placeName(%+v) = %q, want %q", w, got, want)
		}
	}
}
