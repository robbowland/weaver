package rex

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/mux"
)

// A thread's session is weaver's as home is: its panes are listed with
// home's, each call goes to the session its pane is in, and closing it
// leaves home running. Wvr in it is in home's session by name, its files
// keyed by home.
func TestFleet(t *testing.T) {
	server(t)
	dir := t.TempDir()
	sleep := []string{"/bin/sleep", "600"}
	home, err := Start(dir, "demo", "/", sleep)
	if err != nil {
		t.Fatal(err)
	}
	f := Fleet{Home: home}
	wt, _ := filepath.EvalSymlinks(t.TempDir())
	w, p, _, err := f.OpenSession("demo/greet", mux.NewWindow{Spawn: mux.Spawn{Argv: sleep, Dir: wt}, Label: "main", Background: true})
	if err != nil {
		t.Fatal(err)
	}
	ts, err := f.threads()
	if err != nil || len(ts) != 1 || ts[0].ID == home.ID {
		t.Fatalf("threads: %+v %v", ts, err)
	}
	thread := ts[0]
	panel, err := f.SplitLeft(p, mux.NewSplit{Spawn: mux.Spawn{Argv: sleep}, Title: "panel", Background: true})
	if err != nil {
		t.Fatalf("a split in the thread's session: %v", err)
	}
	ps, err := f.Panes()
	if err != nil {
		t.Fatal(err)
	}
	if a, ok := paneIn(ps, p); !ok || a.Window != w || a.Cwd != wt {
		t.Errorf("the thread's agent: %+v %v", a, ok)
	}
	if n, ok := paneIn(ps, panel); !ok || n.Title != "panel" {
		t.Errorf("its panel: %+v %v", n, ok)
	}
	if len(ps) != 3 {
		t.Errorf("panes: %+v, want home's and the thread's two", ps)
	}
	f.Title(p, "renamed")
	if ps, _ := thread.Panes(); !func() bool { a, ok := paneIn(ps, p); return ok && a.Title == "renamed" }() {
		t.Errorf("a title given in the thread's session: %+v", ps)
	}

	t.Setenv("REX_SESSION", thread.ID)
	if h := FromEnv(dir); h.Name != "demo" || h.Home != home.ID || h.ID != thread.ID || !Chosen(dir) {
		t.Errorf("in the thread's session: %+v", h)
	}
	if id, ok := (ids{dir}).named("demo"); !ok || id != home.ID {
		t.Errorf("demo is %q, want home %q", id, home.ID)
	}
	os.Unsetenv("REX_SESSION")

	f.CloseSession(p)
	if ts, err := f.threads(); err != nil || len(ts) != 0 {
		t.Errorf("threads once closed: %+v %v", ts, err)
	}
	if !home.Running() {
		t.Error("home went with the thread's session")
	}
}

// Quit from a thread's session (ctrl+q's card runs there) ends that
// session last, everything else first: ending it ends the quitting wvr.
// When Rex will not list the threads, home stays and so does the error.
func TestFleetQuit(t *testing.T) {
	server(t)
	dir := t.TempDir()
	sleep := []string{"/bin/sleep", "600"}
	home, err := Start(dir, "demo", "/", sleep)
	if err != nil {
		t.Fatal(err)
	}
	f := Fleet{Home: home}
	var ids []string
	for _, l := range []string{"a", "b", "c"} {
		_, p, _, err := f.OpenSession(l, mux.NewWindow{Spawn: mux.Spawn{Argv: sleep}, Label: l, Background: true})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, f.of(string(p)).ID)
	}
	// rex as weaver runs it, each call logged; session.list failing while
	// a mark is there.
	bin, err := exec.LookPath(Bin())
	if err != nil {
		t.Fatal(err)
	}
	wd := t.TempDir()
	log, mark := wd+"/rex.log", wd+"/no-list"
	wrap := "#!/bin/sh\necho \"$*\" >> '" + log + "'\ncase \" $* \" in *\" session.list \"*) [ -e '" + mark + "' ] && exit 1;; esac\nexec '" + bin + "' \"$@\"\n"
	if err := os.WriteFile(wd+"/rex", []byte(wrap), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REX_BIN_DIR", wd)
	// Through the rex command, which the wrapper stands in for.
	t.Setenv("WEAVER_REX_WIRE", "0")

	if err := os.WriteFile(mark, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	err = (Fleet{Home: home, Here: ids[0]}).Quit()
	os.Remove(mark)
	if err == nil || !home.Running() {
		t.Errorf("quit with no threads listed: %v, home running %v", err, home.Running())
	}
	os.Remove(log)
	if err := (Fleet{Home: home, Here: ids[0]}).Quit(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	var ended []string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.Contains(l, "session.destroy") {
			for _, id := range append([]string{home.ID}, ids...) {
				if strings.Contains(l, id) {
					ended = append(ended, id)
				}
			}
		}
	}
	if len(ended) != 4 || ended[3] != ids[0] {
		t.Errorf("ended %q, want all four, %s (where it runs) last", ended, ids[0])
	}
}

// Home closed alone and made again finds the threads' sessions still
// running: they are the new home's, from the fleet's and from inside them.
func TestFleetHomeAgain(t *testing.T) {
	server(t)
	dir := t.TempDir()
	sleep := []string{"/bin/sleep", "600"}
	old, err := Start(dir, "demo", "/", sleep)
	if err != nil {
		t.Fatal(err)
	}
	_, p, _, err := (Fleet{Home: old}).OpenSession("a", mux.NewWindow{Spawn: mux.Spawn{Argv: sleep}, Label: "a", Background: true})
	if err != nil {
		t.Fatal(err)
	}
	thread := (Fleet{Home: old}).of(string(p)).ID
	if err := old.Quit(); err != nil {
		t.Fatal(err)
	}
	home, err := Start(dir, "demo", "/", sleep)
	if err != nil {
		t.Fatal(err)
	}
	if ts, err := (Fleet{Home: home}).threads(); err != nil || len(ts) != 1 || ts[0].ID != thread {
		t.Errorf("the new home's threads: %+v %v, want %s", ts, err, thread)
	}
	t.Setenv("REX_SESSION", thread)
	if h := FromEnv(dir); h.Home != home.ID {
		t.Errorf("inside the thread, home is %s, want %s", h.Home, home.ID)
	}
}

// A focus you ask for (a new thread, a jump) shows its pane's session and
// window in the app though that window is its session's only one, so
// already active: the app may show another session, and Rex says not
// which. What goes on behind you (a session opened in the background, an
// agent started again in its pane) never asks the app.
func TestFocusShowsItsSession(t *testing.T) {
	server(t)
	dir := t.TempDir()
	sleep := []string{"/bin/sleep", "600"}
	home, err := Start(dir, "demo", "/", sleep)
	if err != nil {
		t.Fatal(err)
	}
	f := Fleet{Home: home}
	// rex as weaver runs it, each call logged.
	bin, err := exec.LookPath(Bin())
	if err != nil {
		t.Fatal(err)
	}
	wd := t.TempDir()
	log := wd + "/rex.log"
	wrap := "#!/bin/sh\necho \"$*\" >> '" + log + "'\nexec '" + bin + "' \"$@\"\n"
	if err := os.WriteFile(wd+"/rex", []byte(wrap), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REX_BIN_DIR", wd)
	calls := func() string { b, _ := os.ReadFile(log); return string(b) }

	w, p, _, err := f.OpenSession("t", mux.NewWindow{Spawn: mux.Spawn{Argv: sleep}, Label: "t", Background: true})
	if err != nil {
		t.Fatal(err)
	}
	s := f.of(string(p))
	win, err := s.window(w)
	if err != nil {
		t.Fatal(err)
	}
	// Over the control connection, an app connected (as the Rex app is).
	asked := make(chan map[string]any, 8)
	fakeApp(t, wireEndpoint(), s.ID, false, asked)
	if err := f.Respawn(p, mux.Spawn{Argv: sleep}); err != nil {
		t.Fatal(err)
	}
	select {
	case a := <-asked:
		t.Errorf("behind you, the app was asked %+v", a)
	case <-time.After(200 * time.Millisecond):
	}
	f.Focus(p)
	select {
	case a := <-asked:
		if args, _ := a["args"].(map[string]any); a["action"] != "session.select" || args["session_id"] != s.ID || args["window_id"] != win {
			t.Errorf("focus: the app was asked %+v, want its session and window shown", a)
		}
	case <-time.After(2 * time.Second):
		t.Error("focus: the app was not asked to show it")
	}

	// Through the rex command, alike.
	t.Setenv("WEAVER_REX_WIRE", "0")
	if err := f.Respawn(p, mux.Spawn{Argv: sleep}); err != nil {
		t.Fatal(err)
	}
	if c := calls(); strings.Contains(c, "session.select") {
		t.Errorf("behind you, the app was asked to show:\n%s", c)
	}
	f.Focus(p)
	if want := "do session.select session_id=" + s.ID + " window_id=" + win; !strings.Contains(calls(), want) {
		t.Errorf("focus: no %q in:\n%s", want, calls())
	}
}

// The fleet is looked at in one call, home's and every thread's session
// that runs; a Rex whose scripts lack what that call uses is looked at
// session by session, alike; a Rex that fails a call fails the look.
func TestFleetLook(t *testing.T) {
	server(t)
	dir := t.TempDir()
	sleep := []string{"/bin/sleep", "600"}
	home, err := Start(dir, "demo", "/", sleep)
	if err != nil {
		t.Fatal(err)
	}
	f := Fleet{Home: home}
	var ps []mux.PaneID
	for range 3 {
		_, p, _, err := f.OpenSession("demo/t", mux.NewWindow{Spawn: mux.Spawn{Argv: sleep, Dir: "/"}, Label: "main", Background: true})
		if err != nil {
			t.Fatal(err)
		}
		ps = append(ps, p)
	}
	// One gone: a thread closed is no session to look at.
	f.CloseSession(ps[2])
	names := func(l []mux.Pane) string {
		var s []string
		for _, p := range l {
			s = append(s, string(p.ID)+"@"+p.Session)
		}
		return strings.Join(s, " ")
	}
	one, err := f.Panes()
	if err != nil || len(one) != 3 {
		t.Fatalf("panes in one look: %+v %v", one, err)
	}
	if _, ok := paneIn(one, ps[2]); ok {
		t.Errorf("a closed thread's pane listed: %s", names(one))
	}
	ws, err := f.Windows()
	if err != nil || len(ws) != 3 {
		t.Errorf("windows in one look: %+v %v", ws, err)
	}
	// Through the rex command: one rex do, alike; and, its scripts
	// failing, session by session.
	t.Setenv("WEAVER_REX_WIRE", "0")
	lua, err := f.Panes()
	if err != nil || names(lua) != names(one) {
		t.Errorf("panes through the rex command: %s (%v), want %s", names(lua), err, names(one))
	}
	defer func(s string) { fleetScript = s }(fleetScript)
	fleetScript = `return rex.no_such_thing.at_all`
	each, err := f.Panes()
	if err != nil || names(each) != names(one) {
		t.Errorf("panes session by session: %s (%v), want %s", names(each), err, names(one))
	}
	fleetScript = `error("rex: down", 0)`
	if _, err := f.Panes(); err == nil {
		t.Error("a failed look gave panes")
	}
}

// A pane's columns as drawn are asked in one call: its size, and with no
// app attached (none is to a test's server) its own; with one, the
// session's snapshot, which places it in its window (here the script
// counts any client as an app). A Rex whose scripts fail asks call by
// call, alike.
func TestDrawnCols(t *testing.T) {
	server(t)
	dir := t.TempDir()
	sleep := []string{"/bin/sleep", "600"}
	home, err := Start(dir, "demo", "/", sleep)
	if err != nil {
		t.Fatal(err)
	}
	_, p, cols, err := home.OpenWindow(mux.NewWindow{Spawn: mux.Spawn{Argv: sleep}, Label: "w", At: 1, Background: true})
	if err != nil || cols != 80 {
		t.Fatalf("a window's pane: %d columns (%v), want Rex's 80", cols, err)
	}
	left, err := home.SplitLeft(p, mux.NewSplit{Spawn: mux.Spawn{Argv: sleep}, Cols: 20, Of: 100, Background: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := home.drawnCols(left); got != 80 {
		t.Errorf("drawnCols with no app: %d, want its own 80", got)
	}
	defer func(s string) { drawnScript = s }(drawnScript)
	// Through the rex command: one rex do (drawnLua).
	t.Setenv("WEAVER_REX_WIRE", "0")
	if got := home.drawnCols(left); got != 80 {
		t.Errorf("drawnCols through the rex command: %d, want its own 80", got)
	}
	drawnScript = `return rex.no_such_thing.at_all`
	if got := home.drawnCols(left); got != 80 {
		t.Errorf("drawnCols call by call: %d, want 80", got)
	}
	drawnScript = strings.Replace(drawnLua, `c.info.kind == "app"`, `true`, 1)
	b, _ := home.block(left)
	d, err := home.drawn(b)
	if err != nil || d.Snap == nil || len(d.Clients) == 0 {
		t.Fatalf("drawn with an app: %+v %v", d, err)
	}
	// Split at 0.2 of a window Rex draws each terminal of at 80: the
	// window is 400 wide (80 / 0.2), the panel a fifth of it.
	if got := d.Snap.colsOf(b, 0); got != 80 {
		t.Errorf("colsOf from the snapshot: %d, want 80", got)
	}
	if _, ok := d.Snap.Blocks[b]; !ok {
		t.Errorf("the snapshot's terminals: %+v, want %s among them", d.Snap.Blocks, b)
	}
}

// A block that goes while the fleet is looked at (closed between the view
// and its terminal's methods: a respawn, a panel closed) fails nothing:
// its methods answer nothing, as a snapshot of the session takes it.
func TestFleetLookBlockGone(t *testing.T) {
	server(t)
	dir := t.TempDir()
	sleep := []string{"/bin/sleep", "600"}
	home, err := Start(dir, "demo", "/", sleep)
	if err != nil {
		t.Fatal(err)
	}
	f := Fleet{Home: home}
	if _, _, _, err := f.OpenSession("demo/t", mux.NewWindow{Spawn: mux.Spawn{Argv: sleep, Dir: "/"}, Label: "main", Background: true}); err != nil {
		t.Fatal(err)
	}
	defer func(s string) { fleetScript = s }(fleetScript)
	t.Setenv("WEAVER_REX_WIRE", "0")
	const at = "local function look(sid, view)\n  local blocks = {}\n"
	if !strings.Contains(fleetLua, at) {
		t.Fatal("fleetLua's look has moved: the test's stand-in needs it")
	}
	fleetScript = strings.Replace(fleetLua, at, at+`  local w = (view.windows or {})[1]
  if w then table.insert(w.layers[1].blocks, { block_id = "block:01a1d24b-d314-742e-bd20-ec3db53be037", creator_name = term }) end
`, 1)
	ps, err := f.Panes()
	if err != nil {
		t.Fatalf("a look with a block gone: %v", err)
	}
	if len(ps) < 2 {
		t.Errorf("panes: %+v, want home's and the thread's still", ps)
	}
}
