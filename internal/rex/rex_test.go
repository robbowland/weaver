package rex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/mux"
)

// hermetic clears what weaver, Rex and tmux read from the environment,
// all but where rex is (REX_BIN_DIR): a test is the same in a weaver pane,
// a Rex terminal or tmux as anywhere else.
func hermetic(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k == "TMUX" || k == "TMUX_PANE" || strings.HasPrefix(k, "WEAVER_") ||
			strings.HasPrefix(k, "REX_") && k != "REX_BIN_DIR" {
			t.Setenv(k, "")
			_ = os.Unsetenv(k)
		}
	}
}

// server starts a Rex server of the test's own (skipped without rex) and
// points rex at it.
func server(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath(Bin()); err != nil {
		t.Skip("rex is not installed")
	}
	hermetic(t)
	// A Unix socket's path has room for about 100 bytes: not $TMPDIR's.
	d, err := os.MkdirTemp("/tmp", "wvr-rex")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	conf := filepath.Join(d, "init.lua")
	if err := os.WriteFile(conf, []byte("-- nothing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(d, "s.sock")
	// Its runtime files in d, not the user's Rex directory.
	run := filepath.Join(d, "run")
	t.Setenv("REX_RUNTIME_DIR", run)
	t.Setenv("REX_SERVER", "unix://"+sock)
	srv := exec.Command(Bin(), "server", "run", "--listen", "unix://"+sock, "--state-dir", filepath.Join(d, "state"), "--config", conf)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	// Stopped, not killed, so that Rex removes its runtime file as it goes.
	t.Cleanup(func() {
		done := make(chan error, 1)
		go func() { done <- srv.Wait() }()
		_ = srv.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = srv.Process.Kill()
			<-done
			t.Error("the Rex server did not stop")
		}
		if left, _ := filepath.Glob(filepath.Join(run, "servers", "*", "runtime.json")); len(left) > 0 {
			t.Errorf("the Rex server left %q", left)
		}
	})
	for i := 0; ; i++ {
		if call("server.status", map[string]any{}, nil) == nil {
			return
		}
		if i == 50 {
			t.Fatal("the Rex server did not start")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func paneIn(ps []mux.Pane, p mux.PaneID) (mux.Pane, bool) {
	for _, x := range ps {
		if x.ID == p {
			return x, true
		}
	}
	return mux.Pane{}, false
}

// What weaver does with a session, on Rex: a thread's window with its
// nvim, placed by number, typed into, started again and closed.
func TestSession(t *testing.T) {
	server(t)
	dir := t.TempDir()
	sleep := []string{"/bin/sleep", "600"}
	s, err := Start(dir, "demo", "/", sleep)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := Named(dir, "demo"); !ok || got.ID != s.ID {
		t.Fatalf("Named: %+v %v", got, ok)
	}
	if ws, err := s.Windows(); err != nil || len(ws) != 1 || ws[0].Name != "home" || ws[0].Index != 0 {
		t.Fatalf("home: %+v %v", ws, err)
	}

	w, p, _, err := s.OpenWindow(mux.NewWindow{Spawn: mux.Spawn{Argv: []string{"/bin/cat"}, Dir: "/tmp", Keep: true},
		Label: "01 demo", At: 1, Background: true})
	if err != nil {
		t.Fatal(err)
	}
	nv, _, err := s.SplitLeft(p, mux.NewSplit{Spawn: mux.Spawn{Argv: sleep}, Cols: 20, Title: "nvim: demo", Background: true})
	if err != nil {
		t.Fatal(err)
	}
	ps, err := s.Panes()
	if err != nil {
		t.Fatal(err)
	}
	if a, ok := paneIn(ps, p); !ok || a.Tab != w || a.TabIndex != 1 || a.Cmd != "/bin/cat" || a.Exited {
		t.Errorf("agent pane: %+v %v", a, ok)
	}
	if n, ok := paneIn(ps, nv); !ok || n.Tab != w || n.Title != "nvim: demo" {
		t.Errorf("nvim pane: %+v %v", n, ok)
	}

	// Threads by number, views after them, wherever each was opened.
	v, _, _, err := s.OpenWindow(mux.NewWindow{Spawn: mux.Spawn{Argv: sleep}, Label: "GITHUB", View: "github", At: 900, Background: true})
	if err != nil {
		t.Fatal(err)
	}
	w3, _, _, err := s.OpenWindow(mux.NewWindow{Spawn: mux.Spawn{Argv: sleep}, Label: "03 later", At: -1, Background: true})
	if err != nil {
		t.Fatal(err)
	}
	s.Place(w3, 3)
	if _, _, _, err := s.OpenWindow(mux.NewWindow{Spawn: mux.Spawn{Argv: sleep}, Label: "taken", At: 3}); err == nil {
		t.Error("a place another window has was given again")
	}
	ws, _ := s.Windows()
	var order []string
	for _, x := range ws {
		order = append(order, x.Name)
		if x.ID == v && x.View != "github" {
			t.Errorf("view window: %+v", x)
		}
	}
	if want := []string{"home", "01 demo", "03 later", "GITHUB"}; !slices.Equal(order, want) {
		t.Errorf("windows: %q, want %q", order, want)
	}

	if err := s.Type(p, "hello rex"); err != nil {
		t.Fatal(err)
	}
	for i := 0; !strings.Contains(string(s.Screen(p)), "hello rex"); i++ {
		if i == 30 {
			t.Fatalf("screen: %q", s.Screen(p))
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Started again, the agent is the same pane.
	old, _ := s.block(p)
	if err := s.Respawn(p, mux.Spawn{Argv: sleep, Keep: true}); err != nil {
		t.Fatal(err)
	}
	if now, _ := s.block(p); now == old || now == "" {
		t.Errorf("respawned pane names %q (was %q)", now, old)
	}
	ps, _ = s.Panes()
	if a, ok := paneIn(ps, p); !ok || a.Tab != w || a.Cmd != "/bin/sleep 600" {
		t.Errorf("respawned: %+v %v", a, ok)
	}

	s.Close(nv)
	s.CloseWindow(w3)
	ps, _ = s.Panes()
	if _, ok := paneIn(ps, nv); ok {
		t.Error("closed pane still listed")
	}
	if err := s.Quit(); err != nil || s.Running() {
		t.Errorf("quit: %v, running %v", err, s.Running())
	}
}

// What is on screen: Rex does not say which session an app shows, so no
// pane is known to be looked at, though the session has an active one.
func TestOnScreen(t *testing.T) {
	server(t)
	s, err := Start(t.TempDir(), "demo", "/", []string{"/bin/sleep", "600"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Quit() })
	if _, ok := s.Active(); !ok {
		t.Fatal("no active pane")
	}
	if ps, err := s.Looking(); len(ps) > 0 || err == nil {
		t.Errorf("looking: %q %v", ps, err)
	}

	// No app is attached: a popup cannot show, so none is made, and its
	// command fails to start, as its caller learns.
	c, ok := s.Popup(func(c, l int) (int, int) { return c / 2, l / 2 }, false, "", []string{"/bin/sleep", "600"})
	if ok {
		t.Error("a popup shows with no app attached")
	}
	if err := c.Start(); err == nil {
		_ = c.Process.Kill()
		_ = c.Wait()
		t.Error("a popup that cannot show started")
	}
	if n := floating(t, s); n > 0 {
		t.Errorf("%d floating layers left", n)
	}
}

// floating is how many floating layers the session's windows have.
func floating(t *testing.T, s Session) int {
	t.Helper()
	v, err := s.view()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, w := range v.Windows {
		for _, l := range w.Layers {
			if l.Kind == "floating" {
				n++
			}
		}
	}
	return n
}

// An app is attached unless its connection is lost: Rex says to take a
// missing or unknown state as connected. No other client counts.
func TestApp(t *testing.T) {
	for in, want := range map[string]bool{
		`{"info":{"kind":"app"},"connection_state":"connected"}`: true,
		`{"info":{"kind":"app"}}`:                                true,
		`{"info":{"kind":"app"},"connection_state":"resuming"}`:  true,
		`{"info":{"kind":"app"},"connection_state":"lost"}`:      false,
		`{"info":{"kind":"cli"},"connection_state":"connected"}`: false,
		`{"connection_state":"connected"}`:                       false,
	} {
		var c client
		if err := json.Unmarshal([]byte(in), &c); err != nil {
			t.Fatal(err)
		}
		if c.app() != want {
			t.Errorf("%s: app %v, want %v", in, !want, want)
		}
	}
}

// The server's pid is read from the runtime file Rex keeps under the
// hash of its endpoint, without a call and whatever else is there: here
// a stale file of another server once on the same endpoint, which sorts
// first, and no server to ask.
func TestServerPID(t *testing.T) {
	hermetic(t)
	run := t.TempDir()
	t.Setenv("REX_RUNTIME_DIR", run)
	ep := "unix:///nowhere/rex.sock"
	t.Setenv("REX_SERVER", ep)
	sum := sha256.Sum256([]byte(ep))
	for dir, pid := range map[string]int{hex.EncodeToString(sum[:]): os.Getpid(), "0000stale": os.Getppid()} {
		d := filepath.Join(run, "servers", dir)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(map[string]any{"endpoint": ep, "pid": pid})
		if err := os.WriteFile(filepath.Join(d, "runtime.json"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := serverPID(); got != os.Getpid() {
		t.Errorf("serverPID %d, want %d", got, os.Getpid())
	}
}

// A pane's number comes from its block, the same each time it is asked.
func TestFromEnv(t *testing.T) {
	hermetic(t)
	dir := t.TempDir()
	r := ids{dir}
	if err := r.made("session:abc", "weaver", 1); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REX_SERVER", "unix:///nowhere")
	t.Setenv("REX_SESSION", "session:abc")
	t.Setenv("REX_BLOCK", "block:def")
	h := FromEnv(dir)
	if !h.Inside || h.Name != "weaver" || h.Pane == "" || h.ID != "session:abc" {
		t.Fatalf("%+v", h)
	}
	if again := FromEnv(dir); again.Pane != h.Pane {
		t.Errorf("pane %q, then %q", h.Pane, again.Pane)
	}
	if !Chosen(dir) {
		t.Error("a session weaver made is not chosen")
	}
	t.Setenv("REX_SESSION", "session:other")
	if Chosen(dir) {
		t.Error("another session is chosen")
	}
}
