package rex

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
	panel, _, err := f.SplitLeft(p, mux.NewSplit{Spawn: mux.Spawn{Argv: sleep}, Title: "panel", Background: true})
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

	os.WriteFile(mark, nil, 0o644)
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
