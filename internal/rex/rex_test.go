package rex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
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
	nv, pid, err := s.SplitLeft(p, mux.NewSplit{Spawn: mux.Spawn{Argv: sleep}, Cols: 20, Title: "nvim: demo", Background: true})
	if err != nil {
		t.Fatal(err)
	}
	// A pid is the program's or none (0): Rex starts it through the
	// user's shell, which has not yet become it.
	if pid != 0 {
		if out, _ := exec.Command("/bin/ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output(); strings.TrimSpace(string(out)) != "/bin/sleep 600" {
			t.Errorf("the split's pid %d runs %q", pid, out)
		}
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

// slowRex is rex, answering session.new_split only once the program it
// starts says it is asking its number ($WVR_TEST_MARK is there), and a
// moment after.
const slowRex = `#!/bin/sh
"$WVR_TEST_REX" "$@"
s=$?
case " $* " in *" session.new_split "*)
  i=0
  while [ ! -e "$WVR_TEST_MARK" ] && [ $i -lt 100 ]; do sleep 0.1; i=$((i+1)); done
  sleep 0.3
esac
exit $s
`

// A program Respawn starts has its pane's number however soon it asks:
// here before Respawn has heard which block it is in.
func TestRespawnNumber(t *testing.T) {
	server(t)
	td := t.TempDir()
	dir := filepath.Join(td, "ids")
	sleep := []string{"/bin/sleep", "600"}
	s, err := Start(dir, "demo", "/", sleep)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Quit() })
	_, p, _, err := s.OpenWindow(mux.NewWindow{Spawn: mux.Spawn{Argv: sleep}, Label: "01 demo", At: 1, Background: true})
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(td, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "rex"), []byte(slowRex), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WVR_TEST_REX", Bin())
	t.Setenv("WVR_TEST_MARK", filepath.Join(td, "asking"))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Respawn(p, mux.Spawn{Argv: []string{self, "-test.run=^TestRespawnedProgram$"},
		Env: []string{"WVR_TEST_RESPAWNED=" + td}, Keep: true}); err != nil {
		t.Fatal(err)
	}
	for i := 0; ; i++ {
		if b, err := os.ReadFile(filepath.Join(td, "pane")); err == nil {
			if got := mux.PaneID(b); got != p {
				t.Errorf("the program started again has pane %q, want %q", got, p)
			}
			return
		}
		if i == 100 {
			t.Fatal("the program started again said nothing")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestRespawnedProgram is the program TestRespawnNumber starts: it writes
// the pane it finds itself in, as weaver does (FromEnv).
func TestRespawnedProgram(t *testing.T) {
	td := os.Getenv("WVR_TEST_RESPAWNED")
	if td == "" {
		t.Skip("TestRespawnNumber runs it")
	}
	if err := os.WriteFile(filepath.Join(td, "asking"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	h := FromEnv(filepath.Join(td, "ids"))
	if err := os.WriteFile(filepath.Join(td, "pane.new"), []byte(h.Pane), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(td, "pane.new"), filepath.Join(td, "pane")); err != nil {
		t.Fatal(err)
	}
}

// Alt N's form starts in the directory the pane reported (OSC 7, a
// file:// URL), its escapes undone: config/rex.lua's pwd.
func TestKeysPwd(t *testing.T) {
	server(t)
	src, err := os.ReadFile("../../config/rex.lua")
	if err != nil {
		t.Fatal(err)
	}
	here := filepath.Join(t.TempDir(), "a b%c")
	if err := os.Mkdir(here, 0o755); err != nil {
		t.Fatal(err)
	}
	report := "file://myhost" + (&url.URL{Path: here}).EscapedPath()
	s, err := Start(t.TempDir(), "demo", "/", []string{"/bin/sh", "-c", `printf '\033]7;%s\007' "$1"; exec /bin/sleep 600`, "sh", report})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Quit() })
	b := ""
	for i := 0; ; i++ {
		if sn, err := s.snapshot(true); err == nil {
			for id, in := range sn.Blocks {
				if in.Pwd != nil && in.Pwd.Pwd == report {
					b = id
				}
			}
		}
		if b != "" {
			break
		}
		if i == 50 {
			t.Fatal("the pane reported no directory")
		}
		time.Sleep(100 * time.Millisecond)
	}
	script := strings.ReplaceAll(string(src), "@WVR@", "/usr/bin/true") + "\nreturn pwd({ block_id = rex.args.block })\n"
	var got string
	if err := do(s.ID, script, map[string]any{"block": b}, &got); err != nil {
		t.Fatal(err)
	}
	if got != here {
		t.Errorf("pwd %q, want %q", got, here)
	}
}

// A pane's width is set in its own window, whatever the windows before it:
// here one of blocks that are not terminals, whose width is not known.
func TestBorder(t *testing.T) {
	split := func(a, b string, at float64) string {
		return `{"kind":"tiled","layout":{"split":{"direction":"horizontal","ratio":` + strconv.FormatFloat(at, 'f', -1, 64) +
			`,"before":{"block_id":"` + a + `","rect":[0,0,` + strconv.FormatFloat(at, 'f', -1, 64) + `,1]},` +
			`"after":{"block_id":"` + b + `","rect":[` + strconv.FormatFloat(at, 'f', -1, 64) + `,0,` + strconv.FormatFloat(1-at, 'f', -1, 64) + `,1]}}},`
	}
	in := `{"view":{"windows":[
		{"window_id":"window:a","layers":[` + split("block:a1", "block:a2", 0.5) + `"blocks":[
			{"block_id":"block:a1","creator_name":"com.example.other"},{"block_id":"block:a2","creator_name":"com.example.other"}]}]},
		{"window_id":"window:b","layers":[` + split("block:b1", "block:b2", 0.25) + `"blocks":[
			{"block_id":"block:b1","creator_name":"` + terminal + `"},{"block_id":"block:b2","creator_name":"` + terminal + `"}]}]}]},
		"blocks":{"block:b1":{"size":{"columns":25,"rows":50}},"block:b2":{"size":{"columns":75,"rows":50}}}}`
	var sn snapshot
	if err := json.Unmarshal([]byte(in), &sn); err != nil {
		t.Fatal(err)
	}
	for b, want := range map[string]float64{"block:b1": 0.3, "block:b2": 0.7} {
		before, after, ratio, ok := sn.border(b, 30)
		if !ok || before != "block:b1" || after != "block:b2" || math.Abs(ratio-want) > 1e-9 {
			t.Errorf("%s: %q %q %v %v, want ratio %v", b, before, after, ratio, ok, want)
		}
	}
	if _, _, _, ok := sn.border("block:a1", 30); ok {
		t.Error("a pane in a window of no known width was given one")
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

	// A message shows its text as it is, over the active window, for about
	// the time given, and takes no focus (nor keys).
	text := `it's $HOME; "$(echo not run)" ` + "`x`"
	for name, say := range map[string]func(int, string){"Say": s.Say, "SayToSession": s.SayToSession} {
		before, _ := s.Active()
		start := time.Now()
		say(1000, text)
		for i := 0; !shows(t, s, text); i++ {
			if i == 20 {
				t.Fatalf("%s: the text is not shown", name)
			}
			time.Sleep(100 * time.Millisecond)
		}
		if now, _ := s.Active(); now != before {
			t.Errorf("%s: the focus went from %q to %q", name, before, now)
		}
		for floating(t, s) > 0 {
			if time.Since(start) > 4*time.Second {
				t.Fatalf("%s: the message stays", name)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// shows: does a floating layer of the session show text?
func shows(t *testing.T, s Session, text string) bool {
	t.Helper()
	v, err := s.view()
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range v.Windows {
		for _, l := range w.Layers {
			for _, b := range l.Blocks {
				var f struct {
					Content string `json:"content"`
				}
				if l.Kind == "floating" && s.callS("block.method", map[string]any{"block_id": b.ID, "method": "format",
					"args": map[string]any{"format": "text"}}, &f) == nil && strings.Contains(f.Content, text) {
					return true
				}
			}
		}
	}
	return false
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
