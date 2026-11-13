package rex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/oneline"
	"github.com/robbowland/weaver/internal/process"
)

// Session is the Rex session ID as weaver acts on it, one of its Fleet's.
// Weaver calls it Name (its files' key is Key(Name)) and keeps its numbers
// for Rex's blocks and windows in Dir. Rex has no status line or window marks
// (it shows an agent's state itself, from what Claude Code reports to it),
// nor a message line: Say shows its text in a floating layer of its own.
type Session struct {
	ID   string
	Name string
	Dir  string
}

// Key is the key of weaver's Rex session name in file names: rex-<name>,
// a / as _.
func Key(name string) string { return "rex-" + strings.ReplaceAll(name, "/", "_") }

func (s Session) ids() ids { return ids{s.Dir} }

// num is the number of id, a block or window of the session.
func (s Session) num(id string) (string, error) { return s.ids().num(id, s.ID) }

// callS calls API method on the session.
func (s Session) callS(method string, in map[string]any, out any) error {
	if in == nil {
		in = map[string]any{}
	}
	in["session_id"] = s.ID
	return call(method, in, out)
}

func (s Session) block(p mux.PaneID) (string, error) {
	if b, ok := s.ids().id(string(p)); ok && strings.HasPrefix(b, "block:") {
		return b, nil
	}
	return "", errors.New("rex: no block is pane " + string(p))
}

func (s Session) window(w mux.WindowID) (string, error) {
	if id, ok := s.ids().id(string(w)); ok && strings.HasPrefix(id, "window:") {
		return id, nil
	}
	return "", errors.New("rex: no window is " + string(w))
}

func (s Session) blockMethod(p mux.PaneID, method string, args map[string]any, out any) error {
	b, err := s.block(p)
	if err != nil {
		return err
	}
	if args == nil {
		args = map[string]any{}
	}
	return s.callS("block.method", map[string]any{"block_id": b, "method": method, "args": args}, out)
}

// layout is one terminal running sp, labelled label: its environment
// through env(1), since Rex sets none of its own, with WEAVER_WVR the
// weaver that started it. (Rex runs a command after the login shell's startup, whose
// PATH may put another wvr first: Homebrew's ahead of install.sh's.)
func layout(sp mux.Spawn, label string) map[string]any {
	opts := map[string]any{}
	if len(sp.Argv) > 0 {
		cmd := []string{"/usr/bin/env"}
		if self, err := os.Executable(); err == nil {
			cmd = append(cmd, "WEAVER_WVR="+self)
		}
		opts["command"] = append(append(cmd, sp.Env...), sp.Argv...)
	}
	if sp.Dir != "" {
		opts["cwd"] = sp.Dir
	}
	if sp.Keep {
		opts["exit"] = map[string]any{"on_completion": false}
	}
	b := map[string]any{"flavor": terminal + ".shell", "options": opts}
	if label != "" {
		b["label"] = cut(label)
	}
	return map[string]any{"block": b}
}

// cut is s cut to the longest label Rex takes, 128 characters.
func cut(s string) string { return oneline.Cut(s, 128) }

// serverPID is the pid of the Rex server $REX_SERVER names: from the
// runtime file Rex keeps for it when there is one (no call: hooks ask),
// else asked. Rex keeps it at servers/<sha256 of the endpoint, in
// hex>/runtime.json in its runtime directory, so that one file is read,
// however many other servers left theirs.
func serverPID() int {
	if ep := os.Getenv("REX_SERVER"); ep != "" {
		sum := sha256.Sum256([]byte(ep))
		var r struct {
			Endpoint string `json:"endpoint"`
			Pid      int    `json:"pid"`
		}
		f := filepath.Join(runtimeDir(), "servers", hex.EncodeToString(sum[:]), "runtime.json")
		if b, err := os.ReadFile(f); err == nil && json.Unmarshal(b, &r) == nil && r.Endpoint == ep && process.Alive(r.Pid) {
			return r.Pid
		}
	}
	var st struct {
		Pid int `json:"pid"`
	}
	if call("server.status", map[string]any{}, &st) == nil {
		return st.Pid
	}
	return 0
}

// runtimeDir is where Rex keeps its servers' runtime files:
// $REX_RUNTIME_DIR, else its directory in Application Support.
func runtimeDir() string {
	if d := os.Getenv("REX_RUNTIME_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library/Application Support/rex")
}

func (s Session) Life() agent.Where {
	if !s.Running() {
		return agent.Where{}
	}
	return agent.Where{Server: serverPID(), Session: s.ID}
}

// Made is when weaver made the session (Rex does not say).
func (s Session) Made() (int64, bool) {
	m, ok := s.ids().session(s.ID)
	return int64(m.at), ok && m.at > 0
}

// Restored: did Rex bring the session back after its server restarted?
// Rex restores a session as it was, its ids and all, with a fresh shell in
// each terminal: every program weaver ran in it (home, agents, panels)
// ended with the server weaver made it on. Known when the machine has
// started since weaver made it (no program outlives that, and a server
// started since may have the old one's pid), or when the server running is
// not the one weaver made it on; not, for a record from before weaver kept
// its server, or with no server answering.
func (s Session) Restored() bool {
	m, ok := s.ids().session(s.ID)
	if !ok {
		return false
	}
	if boot := process.BootTime(); boot > 0 && m.at > 0 && m.at < float64(boot) {
		return true
	}
	now := serverPID()
	return m.server > 0 && now > 0 && now != m.server
}

func (s Session) Running() bool {
	live, err := sessions()
	return err == nil && live[s.ID]
}

func (s Session) Quit() error { return s.callS("session.destroy", nil, nil) }

func (s Session) Panes() ([]mux.Pane, error) {
	sn, err := s.snapshot(true)
	if err != nil {
		return nil, err
	}
	return s.panesOf(sn), nil
}

func (s Session) Windows() ([]mux.Window, error) {
	sn, err := s.snapshot(false)
	if err != nil {
		return nil, err
	}
	return s.windowsOf(sn), nil
}

func (s Session) view() (view, error) {
	var v view
	err := s.callS("session.view", nil, &v)
	return v, err
}

func (s Session) Active() (mux.PaneID, bool) {
	v, err := s.view()
	if err != nil {
		return "", false
	}
	for _, w := range v.Windows {
		if w.ID == v.Active && w.Focused != "" {
			n, err := s.num(w.Focused)
			return mux.PaneID(n), err == nil
		}
	}
	return "", false
}

// client is a client attached to a session, as session.list_clients
// gives it.
type client struct {
	ID   string `json:"client_id"`
	Info struct {
		Kind string `json:"kind"`
	} `json:"info"`
	State string `json:"connection_state"`
}

// app: is c an app with a connection? Rex has a missing or unknown state
// taken as connected: only lost is not.
func (c client) app() bool { return c.Info.Kind == "app" && c.State != "lost" }

// attached: is an app attached to the session? It may be showing another.
func (s Session) attached() bool {
	var l struct {
		Clients []client `json:"clients"`
	}
	return s.callS("session.list_clients", nil, &l) == nil && slices.ContainsFunc(l.Clients, client.app)
}

func (s Session) Screen(p mux.PaneID) []byte {
	var f struct {
		Content string `json:"content"`
	}
	_ = s.blockMethod(p, "format", map[string]any{"format": "vt"}, &f)
	return []byte(f.Content)
}

// takenIn is taken, among the windows of view v.
func (s Session) takenIn(v view, at int, skip string) bool {
	for _, w := range v.Windows {
		if n, ok := s.ids().known(w.ID); ok && w.ID != skip && s.place(n) == at {
			return true
		}
	}
	return false
}

// placing is the view to place a new window at at by (placeIn), once at
// is known to be free in it. An error when at is another window's.
func (s Session) placing(at int) (view, bool, error) {
	v, err := s.view()
	if err == nil && s.takenIn(v, at, "") {
		return v, true, errors.New("rex: place " + strconv.Itoa(at) + " is another window's")
	}
	return v, err == nil, nil
}

func (s Session) OpenWindow(w mux.NewWindow) (mux.WindowID, mux.PaneID, int, error) {
	v, seen, err := s.placing(w.At)
	if err != nil {
		return "", "", 0, err
	}
	var out struct {
		Window string   `json:"window_id"`
		Blocks []string `json:"block_ids"`
	}
	// Never the server's active window first: one the app does not show,
	// where a message would go unseen (show).
	if err := s.callS("session.new_window", map[string]any{"window_label": cut(w.Label), "focus": false,
		"layout": layout(w.Spawn, "")}, &out); err != nil {
		return "", "", 0, err
	}
	if out.Window == "" || len(out.Blocks) == 0 {
		return "", "", 0, errors.New("rex: no window made")
	}
	wn, err := s.num(out.Window)
	if err != nil {
		return "", "", 0, err
	}
	pn, err := s.num(out.Blocks[0])
	if err != nil {
		return "", "", 0, err
	}
	_ = s.ids().note("cmd", pn, strings.Join(w.Argv, " "))
	_ = s.ids().note("view", wn, w.View)
	s.placeIn(v, seen, out.Window, mux.WindowID(wn), w.At)
	if !w.Background {
		s.show(out.Window)
	}
	// Its one pane fills it: the window is as wide as the pane.
	return mux.WindowID(wn), mux.PaneID(pn), s.drawnCols(mux.PaneID(pn)), nil
}

// drawnCols is the columns pane p is drawn at, or will be: its own once an
// app has drawn it (asked it for a size). Rex sizes a terminal no app has
// drawn at 80 columns (a window made behind you), so then, with an app
// attached, its share of the width the app draws the session's windows
// at, the active window's (snapshot.size); with none, its own. One rex do
// (drawnLua) asks all it takes; a Rex whose scripts cannot, call by call.
func (s Session) drawnCols(p mux.PaneID) int {
	b, err := s.block(p)
	if err != nil {
		return 0
	}
	// Over the control connection each call is a fraction of a millisecond.
	if wireReady() {
		return s.drawnColsEach(p)
	}
	d, err := s.drawn(b)
	if err != nil && strings.Contains(err.Error(), "-e:") {
		return s.drawnColsEach(p)
	}
	if len(d.Size.Desired) > 0 || !slices.ContainsFunc(d.Clients, client.app) || d.Snap == nil {
		return d.Size.Columns
	}
	return d.Snap.colsOf(b, d.Size.Columns)
}

// drawnAnswer is what drawnLua says of a block.
type drawnAnswer struct {
	Size struct {
		Columns int                         `json:"columns"`
		Desired list[struct{ Columns int }] `json:"desired"`
	} `json:"size"`
	Clients list[client] `json:"clients"`
	Snap    *snapshot    `json:"snap"`
}

func (s Session) drawn(b string) (drawnAnswer, error) {
	var d drawnAnswer
	err := do(s.ID, drawnScript, map[string]any{"block": b}, &d)
	return d, err
}

// drawnLua is what drawnCols asks, in one: block rex.args.block's size;
// unless an app has drawn it, the session's clients; and with an app among
// them, a snapshot as snapshotLua's (its view and its terminals' sizes).
const drawnLua = `
local term = "` + terminal + `"
local size = rex.block.call(term, "size", { block_id = rex.args.block }) or {}
local out = { size = size }
if #(size.desired or {}) > 0 then return out end
local l, err = rex.session.list_clients{}
if l == nil then error(err, 0) end
out.clients = l.clients
local app = false
for _, c in ipairs(l.clients or {}) do
  if c.info and c.info.kind == "app" and c.connection_state ~= "lost" then app = true end
end
if not app then return out end
local view, err = rex.session.view{}
if not view then error(err, 0) end
local blocks = {}
for _, w in ipairs(view.windows or {}) do
  for _, layer in ipairs(w.layers or {}) do
    if layer.kind == "tiled" then
      for _, b in ipairs(layer.blocks or {}) do
        if b.creator_name == term then
          blocks[b.block_id] = { size = rex.block.call(term, "size", { block_id = b.block_id }) }
        end
      end
    end
  end
end
out.snap = { view = view, blocks = blocks }
return out
`

// drawnScript is the script drawnCols runs: drawnLua (a test's stand-in).
var drawnScript = drawnLua

// drawnColsEach is drawnCols a call at a time: the size, the clients, the
// snapshot.
func (s Session) drawnColsEach(p mux.PaneID) int {
	var sz struct {
		Columns int                         `json:"columns"`
		Desired list[struct{ Columns int }] `json:"desired"`
	}
	_ = s.blockMethod(p, "size", nil, &sz)
	if len(sz.Desired) > 0 || !s.attached() {
		return sz.Columns
	}
	b, err := s.block(p)
	if err != nil {
		return sz.Columns
	}
	sn, err := s.snapshot(false)
	if err != nil {
		return sz.Columns
	}
	return sn.colsOf(b, sz.Columns)
}

// colsOf is block b's share of the width of its window, as the snapshot
// sizes it; else cols.
func (sn snapshot) colsOf(b string, cols int) int {
	for _, w := range sn.View.Windows {
		if _, at := w.tiled(); len(at[b]) == 4 {
			if c, _ := sn.size(w); c > 0 {
				return int(math.Round(at[b][2] * float64(c)))
			}
		}
	}
	return cols
}

// SplitLeft opens a pane on the left of pane of. Rex starts the program
// through the user's login shell, which becomes it only once its startup
// is done, so no process is the program's yet as the block is made: the
// program says its pid itself (`wvr _panel` binds itself).
func (s Session) SplitLeft(of mux.PaneID, n mux.NewSplit) (mux.PaneID, error) {
	anchor, err := s.block(of)
	if err != nil {
		return "", err
	}
	ratio := 0.8
	if n.Cols > 0 {
		total := n.Of
		if total <= 0 {
			total = s.drawnCols(of)
		}
		if total > 0 {
			ratio = min(max(float64(n.Cols)/float64(total), 0.05), 0.95)
		}
	}
	var out struct {
		Blocks []string `json:"block_ids"`
	}
	if err := s.callS("session.new_split", map[string]any{"anchor_id": anchor, "direction": "horizontal", "side": "before",
		"ratio": ratio, "focus": !n.Background, "layout": layout(n.Spawn, n.Title)}, &out); err != nil {
		return "", err
	}
	if len(out.Blocks) == 0 {
		return "", errors.New("rex: no pane made")
	}
	pn, err := s.num(out.Blocks[0])
	if err != nil {
		return "", err
	}
	_ = s.ids().note("cmd", pn, strings.Join(n.Argv, " "))
	_ = s.ids().note("split", string(of), strconv.FormatFloat(ratio, 'f', -1, 64))
	return mux.PaneID(pn), nil
}

// Respawn starts sp in a block beside p's, which it then closes, and has
// p name the new block (before its program can learn otherwise): Rex
// starts no program again in a block.
func (s Session) Respawn(p mux.PaneID, sp mux.Spawn) error {
	old, err := s.block(p)
	if err != nil {
		return err
	}
	label, focused := "", false
	if v, err := s.view(); err == nil {
		for _, w := range v.Windows {
			for _, l := range w.Layers {
				for _, x := range l.Blocks {
					if x.ID == old {
						label, focused = x.Label, w.ID == v.Active && w.Focused == old
					}
				}
			}
		}
	}
	if err := s.ids().replace(string(p), func() (string, error) {
		var out struct {
			Blocks []string `json:"block_ids"`
		}
		if err := s.callS("session.new_split", map[string]any{"anchor_id": old, "direction": "horizontal", "side": "after",
			"ratio": 0.5, "layout": layout(sp, label)}, &out); err != nil {
			return "", err
		}
		if len(out.Blocks) == 0 {
			return "", errors.New("rex: no pane made")
		}
		return out.Blocks[0], nil
	}, func(id string) { _ = s.callS("block.close", map[string]any{"block_id": id}, nil) }); err != nil {
		return err
	}
	_ = s.ids().note("cmd", string(p), strings.Join(sp.Argv, " "))
	if s.callS("block.close", map[string]any{"block_id": old}, nil) != nil {
		// Still there: a pane of its own again, not one weaver cannot see.
		s.ids().unretire(old)
	}
	// Its window's focus kept, behind you (restart runs out of sight).
	if b, err := s.block(p); err == nil && focused {
		s.focusBlock(b)
	}
	return nil
}

func (s Session) Close(p mux.PaneID) {
	if b, err := s.block(p); err == nil {
		_ = s.callS("block.close", map[string]any{"block_id": b}, nil)
	}
}

// placeIn is Place of window w (Rex's id) by view v of the session, when
// it was seen: a place another window has is left to it; unseen, the place
// is kept and the window not moved.
func (s Session) placeIn(v view, seen bool, id string, w mux.WindowID, at int) {
	if seen && s.takenIn(v, at, id) {
		return
	}
	_ = s.ids().note("want", string(w), strconv.Itoa(at))
	if !seen {
		return
	}
	in := map[string]any{"window_id": id}
	for _, x := range v.Windows {
		if n, ok := s.ids().known(x.ID); ok && x.ID != id && s.place(n) > at {
			in["before_window_id"] = x.ID
			break
		}
	}
	_ = s.callS("session.move_window", in, nil)
}

func (s Session) Label(w mux.WindowID, label string) {
	if id, err := s.window(w); err == nil {
		_ = s.callS("session.set_window_label", map[string]any{"window_id": id, "label": cut(label)}, nil)
	}
}

func (s Session) Title(p mux.PaneID, title string) {
	if b, err := s.block(p); err == nil {
		_ = s.callS("session.set_block_label", map[string]any{"block_id": b, "label": cut(title)}, nil)
	}
}

// SetWidth moves the border beside p, when p and another block split its
// window side by side.
func (s Session) SetWidth(p mux.PaneID, cols int) {
	b, err := s.block(p)
	if err != nil {
		return
	}
	sn, err := s.snapshot(false)
	if err != nil {
		return
	}
	if before, after, ratio, ok := sn.border(b, cols); ok {
		if s.callS("session.resize_pane", map[string]any{"before_block_id": before, "after_block_id": after,
			"direction": "horizontal", "ratio": ratio}, nil) == nil {
			_ = s.ids().note("split", string(p), strconv.FormatFloat(ratio, 'f', -1, 64))
		}
	}
}

// Moved: was the split beside pane p moved by someone else than weaver
// and the Rex app? It is where neither left it: not even (the app evens a
// window's panes out when they change) and not at the ratio weaver last
// gave it (SetWidth). Not known (no split, Rex not answering): false.
func (s Session) Moved(p mux.PaneID) bool {
	b, err := s.block(p)
	if err != nil {
		return false
	}
	v, err := s.view()
	if err != nil {
		return false
	}
	ratio, ok := v.splitBeside(b)
	if !ok {
		return false
	}
	near := func(a, b float64) bool { return math.Abs(a-b) < 0.005 }
	if near(ratio, 0.5) {
		return false
	}
	last, err := strconv.ParseFloat(s.ids().noted("split", string(p)), 64)
	return err != nil || !near(ratio, last)
}

func (s Session) Zoom(p mux.PaneID) {
	if b, err := s.block(p); err == nil {
		_ = s.callS("session.zoom_block", map[string]any{"block_id": b}, nil)
	}
}

// Show shows window w (show).
func (s Session) Show(w mux.WindowID) {
	if id, err := s.window(w); err == nil {
		s.show(id)
	}
}

// Focus takes you to pane p: its session and window shown in the app
// (show), then the pane focused. Shown whether or not its window is the
// session's active one: the app may show another session altogether, and
// Rex says not which. For what you asked for (a jump, a new thread), never
// for what goes on behind you, which focusBlock keeps to the server.
func (s Session) Focus(p mux.PaneID) {
	b, err := s.block(p)
	if err != nil {
		return
	}
	if v, err := s.view(); err == nil {
		if w := v.windowOf(b); w != "" {
			s.show(w)
		}
	}
	s.focusBlock(b)
}

// focusBlock focuses block b in its window, as the server keeps it; what
// the app shows stays as it is.
func (s Session) focusBlock(b string) {
	_ = s.callS("session.focus_block", map[string]any{"block_id": b}, nil)
}

// show shows window id (Rex's): in the app (showInApp), else, the app
// refusing, as the server's active window at least. Refused for want of
// Remote Control, it says so (sayRemoteOff) first, while the server's
// active window is still the one the app shows.
func (s Session) show(id string) {
	err := s.showInApp(id)
	if err == nil {
		return
	}
	if remoteOff(err) {
		s.sayRemoteOff()
	}
	_ = s.callS("session.focus_window", map[string]any{"window_id": id}, nil)
}

// showInApp asks the Rex app to show window id (Rex's) of the session. The
// app keeps its own tab for a session, which the server's active window
// does not move: session.focus_window changes what the server calls
// active, not what you see, and the app sets it back to its tab when it
// next shows the session. So whatever is to be seen goes through the app,
// which takes actions only with Remote Control on (an error otherwise).
// Not for what happens behind you (a hook, threads restored at start): it
// takes the app to this session.
//
// rex do picks the app only when exactly one is connected (or REX_CLIENT
// names one): with more (two Macs on one server, say) each app attached to
// the session is asked by name (-C), and it is shown when one took it.
func (s Session) showInApp(id string) error {
	if err, ok := s.showInAppWire(id); ok {
		return err
	}
	args := []string{"do", "session.select", "session_id=" + s.ID}
	if id != "" {
		args = append(args, "window_id="+id)
	}
	_, err := run(args...)
	if err == nil || remoteOff(err) {
		return err
	}
	var l struct {
		Clients []client `json:"clients"`
	}
	if s.callS("session.list_clients", nil, &l) != nil {
		return err
	}
	shown := false
	for _, c := range l.Clients {
		if !c.app() || c.ID == "" {
			continue
		}
		if _, e := run(append([]string{"-C", c.ID}, args...)...); e == nil {
			shown = true
		} else if remoteOff(e) {
			err = e // why it was not shown: said (show)
		}
	}
	if shown {
		return nil
	}
	return err
}

// showInAppWire is showInApp over the control connection, asking as rex
// do does (client.perform of the app's session.select, its arguments as
// rex do's key=value pairs give them): the one app connected; with more,
// each app attached to the session. Not ok when there is no connection.
func (s Session) showInAppWire(id string) (err error, ok bool) {
	if !wireReady() {
		return nil, false
	}
	args := map[string]any{"session_id": s.ID}
	if id != "" {
		args["window_id"] = id
	}
	perform := func(c string) error {
		return call("client.perform", map[string]any{"client_id": c, "action": "session.select", "args": args, "origin": "cli"}, nil)
	}
	var l struct {
		Clients []client `json:"clients"`
	}
	if err := call("client.list", map[string]any{}, &l); err != nil {
		return err, true
	}
	var apps []string
	for _, c := range l.Clients {
		if c.app() && c.ID != "" {
			apps = append(apps, c.ID)
		}
	}
	switch len(apps) {
	case 0:
		return errors.New("rex: no app is connected"), true
	case 1:
		return perform(apps[0]), true
	}
	err = errors.New("rex: more than one app is connected, and none attached to the session showed it")
	if s.callS("session.list_clients", nil, &l) != nil {
		return err, true
	}
	shown := false
	for _, c := range l.Clients {
		if !c.app() || c.ID == "" {
			continue
		}
		if e := perform(c.ID); e == nil {
			shown = true
		} else if remoteOff(e) {
			err = e // why it was not shown: said (show)
		}
	}
	if shown {
		return nil, true
	}
	return err, true
}

// remoteOff: is err an app's refusal for want of Remote Control?
func remoteOff(err error) bool { return err != nil && strings.Contains(err.Error(), "Remote Control") }

// remoteOffEvery is how often weaver says the app takes no actions.
const remoteOffEvery = 10 * time.Minute

// sayRemoteOff says that the Rex app shows nothing weaver asks without
// Remote Control, at most every remoteOffEvery (rex/remote-off): else
// weaver's moves would do nothing you could see, and say nothing. A message
// is the server's to show, which needs no Remote Control.
func (s Session) sayRemoteOff() {
	f := s.ids().path("", "remote-off")
	if st, err := os.Stat(f); err == nil && time.Since(st.ModTime()) < remoteOffEvery {
		return
	}
	if s.ids().write("", "remote-off", time.Now().Format(time.RFC3339)) != nil {
		return
	}
	s.Say(8000, "weaver: for weaver to show you its windows, turn on Remote Control in Rex's settings (Rex Server)")
}

// sayScript prints $1, the text (an argument: the shell never reads it),
// on a clear screen with no cursor and no wrapping (what does not fit is
// cut at the right, never scrolling its start away), then waits $2
// seconds.
const sayScript = `printf '\033[2J\033[H\033[?25l\033[?7l %s' "$1"; exec /bin/sleep "$2"`

// Say shows text for about ms milliseconds at the foot of the active
// window: a floating layer that takes no
// focus (nor keys), whose program prints it and exits, which closes it.
// The layer is labelled with the text: Rex heads a floating layer with its
// label, over its one row. It replaces the message shown before, in any
// window, so two are never shown at once: its block is kept (said/<session>
// in the registry), and Says hold a lock of their own (say) from closing it
// till theirs is made and kept. It is one line, as wide as the text's
// columns (wide characters two), cut at the window's width with its start
// kept. Its control characters show as spaces.
func (s Session) Say(ms int, text string) {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
	if unlock, err := s.ids().lockOn("say"); err == nil {
		defer unlock()
	}
	cols, lines := 160, 48
	if sn, err := s.snapshot(false); err == nil {
		cols, lines = sn.activeSize()
	}
	said := fileName(s.ID)
	if old := s.ids().noted("said", said); old != "" {
		_ = s.callS("block.close", map[string]any{"block_id": old}, nil)
	}
	text = ansi.Truncate(text, max(cols-2, 1), "…")
	w := min(ansi.StringWidth(text)+2, cols)
	fw, fh := float64(w)/float64(cols), 1/float64(lines)
	var out struct {
		Blocks []string `json:"block_ids"`
	}
	if s.callS("session.new_layer", map[string]any{"bounds": []float64{0, 1 - fh, fw, fh}, "focus": false,
		"layout": map[string]any{"block": map[string]any{"flavor": terminal + ".shell", "label": cut(text), "options": map[string]any{
			"shell": "none", "size": map[string]any{"columns": w, "rows": 1},
			"exit":    map[string]any{"on_completion": true, "quick_exit_threshold_ms": 0},
			"command": []string{"/bin/sh", "-c", sayScript, "sh", text, strconv.FormatFloat(float64(ms)/1000, 'f', 3, 64)},
		}}}}, &out) == nil && len(out.Blocks) > 0 {
		_ = s.ids().note("said", said, out.Blocks[0])
	}
}

// Popup opens argv in a floating layer over the active window, sized by
// size from the window's columns and lines; the command waits for it to
// end. With no app attached to the session, or no layer made, nothing is
// shown: false, and a command that fails to start.
func (s Session) Popup(size func(cols, lines int) (int, int), dir string, argv []string) (*exec.Cmd, bool) {
	if !s.attached() {
		return failed(errors.New("rex: no app is attached to the session")), false
	}
	cols, lines, active := 160, 48, ""
	if sn, err := s.snapshot(false); err == nil {
		cols, lines = sn.activeSize()
		active = sn.View.Active
	}
	pw, ph := size(cols, lines)
	fw, fh := min(float64(pw)/float64(cols), 1), min(float64(ph)/float64(lines), 1)
	in := map[string]any{"bounds": []float64{(1 - fw) / 2, (1 - fh) / 2, fw, fh}, "focus": true,
		"layout": layout(mux.Spawn{Argv: argv, Dir: dir}, "weaver")}
	if active != "" {
		in["window_id"] = active
	}
	var out struct {
		Blocks []string `json:"block_ids"`
	}
	if err := s.callS("session.new_layer", in, &out); err != nil {
		return failed(err), false
	}
	if len(out.Blocks) == 0 {
		return failed(errors.New("rex: no popup made")), false
	}
	return rexCmd("wait", "-s", s.ID, out.Blocks[0]), true
}

// failed is a command whose Start returns err.
func failed(err error) *exec.Cmd { return &exec.Cmd{Err: err} }
