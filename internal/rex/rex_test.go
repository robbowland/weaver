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
	"sync"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/process"
	"github.com/robbowland/weaver/internal/rex/rextest"
)

// server starts a Rex server of the test's own (rextest.Server: skipped
// without rex) and points rex at it, and checks that serverPID finds it
// from the runtime file Rex keeps for it, as hooks do, without a call.
func server(t *testing.T) {
	t.Helper()
	rextest.Server(t, Bin())
	sum := sha256.Sum256([]byte(os.Getenv("REX_SERVER")))
	if _, err := os.Stat(filepath.Join(os.Getenv("REX_RUNTIME_DIR"), "servers", hex.EncodeToString(sum[:]), "runtime.json")); err != nil {
		t.Fatalf("the Rex server's runtime file: %v", err)
	}
	var st struct {
		Pid int `json:"pid"`
	}
	if err := call("server.status", map[string]any{}, &st); err != nil || st.Pid == 0 || serverPID() != st.Pid {
		t.Fatalf("serverPID %d, the server's %d (%v)", serverPID(), st.Pid, err)
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
	if got, ok, err := Named(dir, "demo"); !ok || err != nil || got.ID != s.ID {
		t.Fatalf("Named: %+v %v %v", got, ok, err)
	}
	if ws, err := s.Windows(); err != nil || len(ws) != 1 || ws[0].Name != "home" || ws[0].Index != 0 {
		t.Fatalf("home: %+v %v", ws, err)
	}

	w, p, width, err := s.OpenWindow(mux.NewWindow{Spawn: mux.Spawn{Argv: []string{"/bin/cat"}, Dir: "/tmp", Keep: &mux.Exit{}},
		Label: "01 demo", At: 1, Background: true})
	if err != nil {
		t.Fatal(err)
	}
	if ws, _ := s.Windows(); !slices.ContainsFunc(ws, func(x mux.Window) bool { return x.ID == w && x.Width == width && width > 0 }) {
		t.Errorf("window %q is %d wide: %+v", w, width, ws)
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
	if a, ok := paneIn(ps, p); !ok || a.Window != w || a.WindowIndex != 1 || a.Cmd != "/bin/cat" || a.Exited {
		t.Errorf("agent pane: %+v %v", a, ok)
	}
	if n, ok := paneIn(ps, nv); !ok || n.Window != w || n.Title != "nvim: demo" {
		t.Errorf("nvim pane: %+v %v", n, ok)
	}

	// Threads by number, views after them, wherever each was opened.
	v, _, _, err := s.OpenWindow(mux.NewWindow{Spawn: mux.Spawn{Argv: sleep}, Label: "GITHUB", View: "github", At: 900, Background: true})
	if err != nil {
		t.Fatal(err)
	}
	w3, _, _, err := s.OpenWindow(mux.NewWindow{Spawn: mux.Spawn{Argv: sleep}, Label: "03 later", At: mux.NextFree, Background: true})
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

	b, err := s.block(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run("send", "-s", s.ID, "-b", b, "hello rex"); err != nil {
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
	if err := s.Respawn(p, mux.Spawn{Argv: sleep, Keep: &mux.Exit{}}); err != nil {
		t.Fatal(err)
	}
	if now, _ := s.block(p); now == old || now == "" {
		t.Errorf("respawned pane names %q (was %q)", now, old)
	}
	ps, _ = s.Panes()
	if a, ok := paneIn(ps, p); !ok || a.Window != w || a.Cmd != "/bin/sleep 600" {
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
	t.Setenv("REX_BIN_DIR", bin)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Respawn(p, mux.Spawn{Argv: []string{self, "-test.run=^TestRespawnedProgram$"},
		Env: []string{"WVR_TEST_RESPAWNED=" + td}, Keep: &mux.Exit{}}); err != nil {
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

// A Respawn that fails once the new block is made leaves the pane as it
// was: the new block closed, the pane's number still the old block's.
// Here the registry cannot keep the new block's number.
func TestRespawnFails(t *testing.T) {
	server(t)
	dir := t.TempDir()
	sleep := []string{"/bin/sleep", "600"}
	s, err := Start(dir, "demo", "/", sleep)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Quit() })
	w, p, _, err := s.OpenWindow(mux.NewWindow{Spawn: mux.Spawn{Argv: sleep}, Label: "01 demo", At: 1, Background: true})
	if err != nil {
		t.Fatal(err)
	}
	old, _ := s.block(p)
	u := filepath.Join(dir, "u")
	if err := os.Chmod(u, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(u, 0o755) })
	if err := s.Respawn(p, mux.Spawn{Argv: sleep, Keep: &mux.Exit{}}); err == nil {
		t.Fatal("Respawn kept no number, yet did not fail")
	}
	if err := os.Chmod(u, 0o755); err != nil {
		t.Fatal(err)
	}
	if now, _ := s.block(p); now != old {
		t.Errorf("pane %q names %q, was %q", p, now, old)
	}
	ps, err := s.Panes()
	if err != nil {
		t.Fatal(err)
	}
	var in []mux.PaneID
	for _, x := range ps {
		if x.Window == w {
			in = append(in, x.ID)
		}
	}
	if len(in) != 1 {
		t.Errorf("the window has panes %q, want only %q", in, p)
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
	script := strings.NewReplacer("@WVR@", "/usr/bin/true", "@SESSIONS@", t.TempDir()).Replace(string(src)) +
		"\nreturn pwd({ block_id = rex.args.block })\n"
	var got string
	if err := do(s.ID, script, map[string]any{"block": b}, &got); err != nil {
		t.Fatal(err)
	}
	if got != here {
		t.Errorf("pwd %q, want %q", got, here)
	}
}

// weaver's keys are weaver's only in the Rex sessions it made (its record
// of them, the registry's sessions/): in any other, the key is the
// terminal's. config/rex.lua's weavers.
func TestKeysWeavers(t *testing.T) {
	server(t)
	src, err := os.ReadFile("../../config/rex.lua")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	s, err := Start(dir, "demo", "/", []string{"/bin/sleep", "600"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Quit() })
	script := strings.NewReplacer("@WVR@", "/usr/bin/true", "@SESSIONS@", dir+"/sessions").Replace(string(src)) +
		"\nreturn { weavers({ session_id = rex.args.mine }), weavers({ session_id = rex.args.other }), weavers({}) }\n"
	var got []bool
	if err := do(s.ID, script, map[string]any{"mine": s.ID, "other": "session:not-weavers"}, &got); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []bool{true, false, false}) {
		t.Errorf("weavers: weaver's session, another, none = %v, want [true false false]", got)
	}
}

// keysLua is config/rex.lua as wvr writes it: run by wvr, with sessions'
// record and settings, and then more Lua of the test's.
func keysLua(t *testing.T, wvr, sessions, settings, more string) string {
	t.Helper()
	src, err := os.ReadFile("../../config/rex.lua")
	if err != nil {
		t.Fatal(err)
	}
	return strings.NewReplacer("@WVR@", wvr, "@SESSIONS@", sessions, "--[[@SETTINGS@]]", settings).Replace(string(src)) + "\n" + more
}

// A key's wvr runs with the settings wvr baked in (Rex's server has an
// app's environment), from home when the pane's directory has gone; when it
// cannot run at all, the key is the terminal's (false), not swallowed.
func TestKeysRun(t *testing.T) {
	server(t)
	dir := t.TempDir()
	s, err := Start(dir, "demo", "/", []string{"/bin/sleep", "600"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Quit() })
	out := filepath.Join(t.TempDir(), "env")
	gone := filepath.Join(t.TempDir(), "gone")
	script := keysLua(t, "/bin/sh", dir+"/sessions", `XDG_STATE_HOME = "/the/state", WEAVER_VIEWS = ""`,
		`return { run({ session_id = rex.args.sid }, { "-c", "env > " .. rex.args.out .. "; pwd >> " .. rex.args.out }, rex.args.gone) }`)
	var ran []bool
	if err := do(s.ID, script, map[string]any{"sid": s.ID, "out": out, "gone": gone}, &ran); err != nil {
		t.Fatal(err)
	}
	var env string
	for i := 0; i < 50; i++ {
		if b, _ := os.ReadFile(out); strings.Contains(string(b), "REX_SESSION") {
			env = string(b)
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, want := range []string{"XDG_STATE_HOME=/the/state\n", "WEAVER_VIEWS=\n", "REX_SESSION=" + s.ID + "\n"} {
		if !strings.Contains(env, want) {
			t.Errorf("a key's wvr has no %q in its environment:\n%s", want, env)
		}
	}
	if !slices.Equal(ran, []bool{true}) || !strings.HasSuffix(env, os.Getenv("HOME")+"\n") {
		t.Errorf("run from a directory gone: %v, ran in %q, want true, from home", ran, env[strings.LastIndex(strings.TrimSuffix(env, "\n"), "\n")+1:])
	}
	script = keysLua(t, "/no/such/wvr", dir+"/sessions", "", `return { run({ session_id = rex.args.sid }, { "go", "home" }) }`)
	if err := do(s.ID, script, map[string]any{"sid": s.ID}, &ran); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ran, []bool{false}) {
		t.Errorf("run with no wvr there: %v, want false (the key the terminal's)", ran)
	}
}

// In one of weaver's popups (a floating layer: the form, a card, Alt y's
// board) weaver's keys are the popup's program's: it takes every key.
func TestKeysInPopup(t *testing.T) {
	server(t)
	dir := t.TempDir()
	s, err := Start(dir, "demo", "/", []string{"/bin/sleep", "600"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Quit() })
	v, err := s.view()
	if err != nil {
		t.Fatal(err)
	}
	var tiled string
	for _, w := range v.Windows {
		for _, l := range w.Layers {
			for _, b := range l.Blocks {
				tiled = b.ID
			}
		}
	}
	var out struct {
		Blocks []string `json:"block_ids"`
	}
	if err := s.callS("session.new_layer", map[string]any{"bounds": []float64{0.25, 0.25, 0.5, 0.5}, "focus": true,
		"layout": layout(mux.Spawn{Argv: []string{"/bin/sleep", "600"}}, "weaver")}, &out); err != nil || len(out.Blocks) == 0 {
		t.Fatal(err, out)
	}
	script := keysLua(t, "/usr/bin/true", dir+"/sessions", "",
		`return { inPopup({ block_id = rex.args.popup }), inPopup({ block_id = rex.args.tiled }), inPopup({}) }`)
	var got []bool
	if err := do(s.ID, script, map[string]any{"popup": out.Blocks[0], "tiled": tiled}, &got); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []bool{true, false, false}) {
		t.Errorf("inPopup: a popup's block, a window's, none = %v, want [true false false]", got)
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
	// Zoomed (Alt V), the zoomed block fills the window and the other keeps
	// the size it had: the window is the zoomed one's width, not its share's.
	z := `{"view":{"windows":[{"window_id":"window:z","zoomed_block_id":"block:z1","layers":[` + split("block:z1", "block:z2", 0.75) + `"blocks":[
		{"block_id":"block:z1","creator_name":"` + terminal + `"},{"block_id":"block:z2","creator_name":"` + terminal + `"}]}]}]},
		"blocks":{"block:z1":{"size":{"columns":160,"rows":50}},"block:z2":{"size":{"columns":40,"rows":50}}}}`
	if err := json.Unmarshal([]byte(z), &sn); err != nil {
		t.Fatal(err)
	}
	if _, _, ratio, ok := sn.border("block:z2", 40); !ok || math.Abs(ratio-0.75) > 1e-9 {
		t.Errorf("zoomed: the agent 40 of 160 wide at ratio %v %v, want 0.75", ratio, ok)
	}
}

// A window made behind you is not drawn yet (its terminals Rex's 80 by
// 24, which no app has asked a size of): it is as wide as the window the
// app shows, which it will be drawn at. Without an app (none drawn), each
// window is what its terminals say.
func TestSizeUndrawn(t *testing.T) {
	in := func(desired string) string {
		return `{"view":{"active_window_id":"window:a","windows":[
		{"window_id":"window:a","layers":[{"kind":"tiled","layout":{"rect":[0,0,1,1],"block_id":"block:a1"},"blocks":[{"block_id":"block:a1","creator_name":"` + terminal + `"}]}]},
		{"window_id":"window:b","layers":[{"kind":"tiled","layout":{"rect":[0,0,1,1],"block_id":"block:b1"},"blocks":[{"block_id":"block:b1","creator_name":"` + terminal + `"}]}]}]},
		"blocks":{"block:a1":{"size":{"columns":185,"rows":50` + desired + `}},"block:b1":{"size":{"columns":80,"rows":24,"desired":[]}}}}`
	}
	var sn snapshot
	if err := json.Unmarshal([]byte(in(`,"desired":[{"columns":185}]`)), &sn); err != nil {
		t.Fatal(err)
	}
	if c, l := sn.size(sn.View.Windows[1]); c != 185 || l != 50 {
		t.Errorf("a window not drawn yet: %dx%d, want the shown one's 185x50", c, l)
	}
	if err := json.Unmarshal([]byte(in(``)), &sn); err != nil {
		t.Fatal(err)
	}
	if c, _ := sn.size(sn.View.Windows[1]); c != 80 {
		t.Errorf("no app: %d wide, want its terminal's 80", c)
	}
}

// rex do picks the app only when one is connected: with two, weaver asks
// each app attached to the session by name (-C), and the session is shown
// when one takes it; the Remote Control notice only when every app refused
// for want of it.
func TestShowInAppMany(t *testing.T) {
	rextest.NoRex(t)
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	fake := "#!/bin/sh\necho \"$*\" >> '" + log + "'\n" + `case "$2 $3 $4 $5" in
"do session.select "*) echo "Error: more than one app is connected: name one with -C" >&2; exit 1 ;;
"-C client:a do session.select") [ -n "$OFF" ] && { echo "Error: Rex.app is not accepting actions. Turn on Remote Control in Rex's settings." >&2; exit 1; }; exit 0 ;;
"-C client:b do session.select") echo "Error: Rex.app is not accepting actions. Turn on Remote Control in Rex's settings." >&2; exit 1 ;;
"api call session.list_clients "*) echo '{"clients":[{"client_id":"client:a","info":{"kind":"app"}},{"client_id":"client:c","info":{"kind":"cli"}},{"client_id":"client:b","info":{"kind":"app"}}]}' ;;
*) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "rex"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REX_BIN_DIR", bin)
	s := Session{ID: "session:x", Name: "w", Dir: t.TempDir()}
	if err := s.showInApp("window:w"); err != nil {
		t.Errorf("shown by app a: %v", err)
	}
	b, _ := os.ReadFile(log)
	if strings.Contains(string(b), "client:c") || !strings.Contains(string(b), "-C client:a do session.select session_id=session:x window_id=window:w") {
		t.Errorf("asked, want each app (not the cli) by name:\n%s", b)
	}
	t.Setenv("OFF", "1")
	if err := s.showInApp("window:w"); !remoteOff(err) {
		t.Errorf("every app refused for want of Remote Control: %v, want that", err)
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

	// A message replaces the one still shown, as on a message line, even
	// one said at the same moment: never two at once.
	s.Say(5000, "the first message")
	s.SayToSession(5000, "the second message")
	if n := floating(t, s); n != 1 {
		t.Errorf("%d messages shown at once", n)
	}
	for i := 0; !shows(t, s, "the second message"); i++ {
		if i == 20 {
			t.Fatal("the second message is not shown")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if shows(t, s, "the first message") {
		t.Error("the first message is still shown")
	}
	var wg sync.WaitGroup
	for _, m := range []string{"the third message", "the fourth message"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Say(5000, m)
		}()
	}
	wg.Wait()
	if n := floating(t, s); n != 1 {
		t.Errorf("%d messages said together shown at once", n)
	}

	// A message's start is always on screen: one of wide characters, or
	// longer than the window (80 columns here), keeps its head.
	for _, text := range []string{"weaver: " + strings.Repeat("漢字", 20), "weaver: " + strings.Repeat("long ", 70)} {
		s.Say(5000, text)
		head := text[:len("weaver: ")+6]
		for i := 0; !strings.HasPrefix(strings.TrimSpace(message(t, s)), head); i++ {
			if i == 20 {
				t.Fatalf("the message shows %q, not its start %q", message(t, s), head)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// A message is as wide as its columns, wide characters and emoji two,
// marks none; cut, it keeps its start and says so.
func TestWidth(t *testing.T) {
	for text, want := range map[string]int{"weaver: ok": 10, "漢字": 4, "ｗｉｄｅ": 8, "é": 1, "👍": 2, "❤️": 2} {
		if got := width(text); got != want {
			t.Errorf("width(%q) %d, want %d", text, got, want)
		}
	}
	for _, c := range []struct {
		text string
		n    int
		want string
	}{
		{"weaver: ok", 10, "weaver: ok"},
		{"weaver: 漢字漢字", 13, "weaver: 漢字…"},
		{"weaver: 漢字漢字", 14, "weaver: 漢字…"},
		{"weaver: long thread name", 12, "weaver: lon…"},
	} {
		if got := head(c.text, c.n); got != c.want || width(got) > c.n {
			t.Errorf("head(%q, %d) %q, want %q", c.text, c.n, got, c.want)
		}
	}
}

// message is what weaver's message shows: the last of its lines, as many
// as its terminal has rows; "" when there is none. Its label, which heads
// it in the app, is what it shows too.
func message(t *testing.T, s Session) string {
	t.Helper()
	v, err := s.view()
	if err != nil {
		t.Fatal(err)
	}
	said := s.ids().noted("said", fileName(s.ID))
	for _, w := range v.Windows {
		for _, l := range w.Layers {
			for _, b := range l.Blocks {
				if l.Kind != "floating" || b.ID != said {
					continue
				}
				var sz struct {
					Rows int `json:"rows"`
				}
				var f struct {
					Content string `json:"content"`
				}
				if s.callS("block.method", map[string]any{"block_id": b.ID, "method": "size", "args": map[string]any{}}, &sz) != nil ||
					s.callS("block.method", map[string]any{"block_id": b.ID, "method": "format", "args": map[string]any{"format": "text"}}, &f) != nil {
					return ""
				}
				lines := strings.Split(strings.TrimRight(f.Content, "\n"), "\n")
				shown := strings.Join(lines[max(len(lines)-sz.Rows, 0):], "\n")
				if strings.TrimSpace(shown) != b.Label {
					t.Errorf("the message shows %q, its label (the app's heading) is %q", shown, b.Label)
				}
				return shown
			}
		}
	}
	return ""
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
	rextest.NoRex(t)
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
	rextest.NoRex(t)
	dir := t.TempDir()
	r := ids{dir}
	if err := r.made("session:abc", "weaver", time.Unix(1, 0), 0); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REX_SERVER", "unix:///nowhere")
	t.Setenv("REX_SESSION", "session:abc")
	t.Setenv("REX_BLOCK", "block:def")
	if p := serverPID(); p != 0 {
		t.Fatalf("reached a Rex server the test did not start (pid %d)", p)
	}
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

// Two weavers starting the same session at once make one, and both have
// it; once it runs, Ensure makes no other.
func TestEnsureOnce(t *testing.T) {
	server(t)
	dir := t.TempDir()
	sleep := []string{"/bin/sleep", "600"}
	var prepared int
	var mu sync.Mutex
	prepare := func() error {
		mu.Lock()
		defer mu.Unlock()
		prepared++
		return nil
	}
	got := make([]Session, 2)
	var wg sync.WaitGroup
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, _, err := Ensure(dir, "demo", "/", sleep, prepare)
			if err != nil {
				t.Error(err)
			}
			got[i] = s
		}()
	}
	wg.Wait()
	t.Cleanup(func() { _ = got[0].Quit() })
	if got[0].ID == "" || got[0].ID != got[1].ID || prepared != 1 {
		t.Errorf("two at once: sessions %q and %q, prepared %d times; want one, once", got[0].ID, got[1].ID, prepared)
	}
	if s, husk, err := Ensure(dir, "demo", "/", sleep, prepare); err != nil || s.ID != got[0].ID || prepared != 1 || husk != nil {
		t.Errorf("again: %q %v, prepared %d times; want %q, once", s.ID, err, prepared, got[0].ID)
	}
	var l struct {
		Sessions []struct {
			Label string `json:"label"`
		} `json:"sessions"`
	}
	if err := call("session.list", map[string]any{}, &l); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, x := range l.Sessions {
		if x.Label == "demo" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("Rex runs %d sessions named demo, want 1", n)
	}
}

// While Rex does not answer, a session weaver made is neither running nor
// not (an error), and Ensure makes none in its place.
func TestEnsureNoAnswer(t *testing.T) {
	server(t)
	dir := t.TempDir()
	s, err := Start(dir, "demo", "/", []string{"/bin/sleep", "600"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Quit() })
	at, run := os.Getenv("REX_SERVER"), os.Getenv("REX_RUNTIME_DIR")
	// No server there, and none in its runtime directory to fall back to.
	none := t.TempDir()
	t.Setenv("REX_SERVER", "unix://"+none+"/none.sock")
	t.Setenv("REX_RUNTIME_DIR", none)
	if _, running, err := Named(dir, "demo"); err == nil {
		t.Errorf("Named with no answer: running %v, no error", running)
	}
	made := false
	if _, _, err := Ensure(dir, "demo", "/", []string{"/bin/sleep", "600"}, func() error { made = true; return nil }); err == nil || made {
		t.Errorf("Ensure with no answer: %v, prepared %v; want an error, nothing made", err, made)
	}
	t.Setenv("REX_SERVER", at)
	t.Setenv("REX_RUNTIME_DIR", run)
	if _, running, err := Named(dir, "demo"); err != nil || !running {
		t.Errorf("Named once Rex answers: %v %v", running, err)
	}
}

// BreakOut moves a pane to a window of its own, at the place asked and
// with the width it says, leaving its old window the rest; a place another
// window has is refused, and the pane stays where it was.
func TestBreakOut(t *testing.T) {
	server(t)
	dir := t.TempDir()
	sleep := []string{"/bin/sleep", "600"}
	s, err := Start(dir, "demo", "/", sleep)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Quit() })
	w, p, _, err := s.OpenWindow(mux.NewWindow{Spawn: mux.Spawn{Argv: sleep}, Label: "01 shared", At: 1, Background: true})
	if err != nil {
		t.Fatal(err)
	}
	q, _, err := s.SplitLeft(p, mux.NewSplit{Spawn: mux.Spawn{Argv: sleep}, Background: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.BreakOut(q, "taken", 1, true); err == nil {
		t.Error("broken out to a place another window has")
	}
	// The pane has its window's focus, and another window is the one shown
	// (active): in the background, that stays the active one.
	before, err := s.view()
	if err != nil {
		t.Fatal(err)
	}
	qb, _ := s.block(q)
	if err := s.callS("session.focus_block", map[string]any{"block_id": qb}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.callS("session.focus_window", map[string]any{"window_id": before.Active}, nil); err != nil {
		t.Fatal(err)
	}
	log := logRex(t)
	out, width, err := s.BreakOut(q, "02 out", 2, true)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(log); strings.Count(string(b), "session.view") != 1 {
		t.Errorf("the session's view asked for %d times, want once:\n%s", strings.Count(string(b), "session.view"), b)
	}
	ws, _ := s.Windows()
	o, ok := windowOf(ws, out)
	if !ok || o.Index != 2 || o.Name != "02 out" || width <= 0 || o.Width != width {
		t.Errorf("its window: %+v %v, said %d wide", o, ok, width)
	}
	ps, _ := s.Panes()
	if x, ok := paneIn(ps, q); !ok || x.Window != out {
		t.Errorf("the pane broken out: %+v %v, want in %q", x, ok, out)
	}
	if x, ok := paneIn(ps, p); !ok || x.Window != w {
		t.Errorf("the pane left: %+v %v, want in %q", x, ok, w)
	}
	if after, _ := s.view(); after.Active != before.Active {
		t.Errorf("active window after a break-out in the background: %q, want %q as before", after.Active, before.Active)
	}
}

// logRex has rex, as weaver runs it, log each call it is given: the log.
func logRex(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath(Bin())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	wrap := "#!/bin/sh\necho \"$*\" >> '" + log + "'\nexec '" + bin + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "rex"), []byte(wrap), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REX_BIN_DIR", dir)
	return log
}

func windowOf(ws []mux.Window, w mux.WindowID) (mux.Window, bool) {
	for _, x := range ws {
		if x.ID == w {
			return x, true
		}
	}
	return mux.Window{}, false
}

// The rex weaver runs is the one Rex's terminals say is theirs
// ($REX_BIN_DIR), ahead of any on PATH; when that has none, PATH's.
func TestBin(t *testing.T) {
	own, other := t.TempDir(), t.TempDir()
	for _, d := range []string{own, other} {
		if err := os.WriteFile(filepath.Join(d, "rex"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", other)
	t.Setenv("REX_BIN_DIR", own)
	if got := Bin(); got != filepath.Join(own, "rex") {
		t.Errorf("with REX_BIN_DIR: %q, want its rex", got)
	}
	t.Setenv("REX_BIN_DIR", t.TempDir())
	if got := Bin(); got != filepath.Join(other, "rex") {
		t.Errorf("REX_BIN_DIR without a rex: %q, want PATH's", got)
	}
	// An app's environment (Rex's server runs weaver's keys with it): no
	// REX_BIN_DIR, no rex on PATH; the Rex app's server's own.
	home := t.TempDir()
	app := filepath.Join(home, appServerRex)
	if err := os.MkdirAll(filepath.Dir(app), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(app, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("REX_BIN_DIR", "")
	t.Setenv("PATH", t.TempDir())
	if got, dir := Bin(), BinDir(); got != app || dir != filepath.Dir(app) {
		t.Errorf("in an app's environment: %q in %q, want the app's server's", got, dir)
	}
}

// A session Rex brings back after its server restarts is a husk: its ids
// as they were, a shell in each terminal. Ensure makes it again and gives
// the husk back, for the caller to end; one on the server it was made on
// is neither restored nor made again.
func TestEnsureRestored(t *testing.T) {
	// A Unix socket's path has room for about 100 bytes: not $TMPDIR's.
	at, err := os.MkdirTemp("/tmp", "wvr-rex")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(at) })
	stop := rextest.ServerAt(t, Bin(), at)
	dir := t.TempDir()
	sleep := []string{"/bin/sleep", "600"}
	nothing := func() error { return nil }
	s, err := Start(dir, "demo", "/", sleep)
	if err != nil {
		t.Fatal(err)
	}
	if s.Restored() {
		t.Error("a session on the server it was made on: restored")
	}
	if got, husk, err := Ensure(dir, "demo", "/", sleep, nothing); err != nil || husk != nil || got.ID != s.ID {
		t.Errorf("Ensure while it runs: %q %v %v, want %q and no husk", got.ID, husk, err, s.ID)
	}
	stop()
	rextest.ServerAt(t, Bin(), at)
	if _, running, err := Named(dir, "demo"); err != nil || !running {
		t.Skipf("this Rex did not bring the session back (%v, %v)", running, err)
	}
	if !s.Restored() {
		t.Fatal("a session Rex brought back after a restart: not restored")
	}
	got, husk, err := Ensure(dir, "demo", "/", sleep, nothing)
	if err != nil || husk == nil || husk.ID != s.ID || got.ID == s.ID || got.Restored() {
		t.Fatalf("Ensure of a restored session: %q, husk %v, %v; want a new one, the old as the husk", got.ID, husk, err)
	}
	if err := husk.Quit(); err != nil {
		t.Fatal(err)
	}
	if again, _, err := Named(dir, "demo"); err != nil || again.ID != got.ID {
		t.Errorf("after: %q %v, want %q", again.ID, err, got.ID)
	}
	t.Cleanup(func() { _ = got.Quit() })
}

// A session weaver made before the machine started, running now, is one
// Rex brought back, whatever pid its server has; one made since is not
// (no server answering to say otherwise).
func TestRestoredAcrossABoot(t *testing.T) {
	rextest.NoRex(t)
	dir := t.TempDir()
	r := ids{dir}
	if err := r.made("session:before", "w", time.Unix(process.BootTime()-60, 0), 0); err != nil {
		t.Fatal(err)
	}
	if err := r.made("session:since", "w", time.Now(), 0); err != nil {
		t.Fatal(err)
	}
	if !(Session{ID: "session:before", Dir: dir}).Restored() {
		t.Error("made before the machine started: not restored")
	}
	if (Session{ID: "session:since", Dir: dir}).Restored() {
		t.Error("made since, its server not known: restored")
	}
}

// A server that read init.lua before weaver.lua was there binds none of
// weaver's keys until it reads its configuration again.
func TestKeysLoaded(t *testing.T) {
	at, err := os.MkdirTemp("/tmp", "wvr-rex")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(at) })
	if err := os.WriteFile(filepath.Join(at, "init.lua"), []byte("pcall(require, \"weaver\")\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rextest.ServerAt(t, Bin(), at)
	if KeysLoaded() {
		t.Fatal("keys loaded before weaver.lua was written")
	}
	if err := os.WriteFile(filepath.Join(at, "weaver.lua"), []byte("rex.bind(\"alt+y\", function() return true end)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if KeysLoaded() {
		t.Error("keys loaded without Rex reading its configuration again")
	}
	if err := ReloadConfig(); err != nil {
		t.Fatal(err)
	}
	if !KeysLoaded() {
		t.Error("keys not loaded after ReloadConfig")
	}
}

// With Remote Control off the app takes no actions: weaver says so, as a
// message (the server's to show), and at most once in a while, rather than
// moving nothing you could see without a word.
func TestRemoteOffSaid(t *testing.T) {
	rextest.NoRex(t)
	dir := t.TempDir()
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	fake := "#!/bin/sh\necho \"$*\" >> '" + log + "'\n" + `case "$2 $3" in
"do session.select") echo "Error: Rex.app is not accepting actions. Turn on Remote Control in Rex's settings." >&2; exit 1 ;;
"api call") echo '{}' ;;
*) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "rex"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REX_BIN_DIR", bin)
	s := Session{ID: "session:x", Name: "w", Dir: dir}
	// Starting shows the session in the app, and says for itself when it
	// cannot (startRex): no message, nor the wait before the next.
	if err := s.ShowInApp(); err == nil {
		t.Fatal("the app took the action")
	}
	s.show("window:a")
	s.show("window:b")
	b, _ := os.ReadFile(log)
	if n := strings.Count(string(b), "new_layer"); n != 1 || !strings.Contains(string(b), "Remote Control") {
		t.Errorf("said %d times, want once, of Remote Control:\n%s", n, b)
	}
	// Said before the fallback makes the window shown the server's active
	// one, while that is still the window the app shows.
	if i, j := strings.Index(string(b), "new_layer"), strings.Index(string(b), "focus_window"); i < 0 || j < 0 || i > j {
		t.Errorf("the message came after the fallback:\n%s", b)
	}
}
