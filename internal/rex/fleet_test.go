package rex

import (
	"os"
	"path/filepath"
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
