package rex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/process"
)

// Session is the Rex session ID as weaver acts on it (mux.Mux). Weaver
// calls it Name (its files' key is Key(Name)) and keeps its numbers for
// Rex's blocks and windows in Dir. Rex has no status line or window marks:
// Publish, Strip and MarkUnseen do nothing (Rex shows an agent's state
// itself, from what Claude Code reports to it). Nor has it a message
// line: Say shows its text in a floating layer of its own.
type Session struct {
	ID   string
	Name string
	Dir  string
}

var _ mux.Mux = Session{}

// Key is the key of weaver's Rex session name in file names: rex-<name>,
// a / as _.
func Key(name string) string { return "rex-" + strings.ReplaceAll(name, "/", "_") }

func (s Session) ids() ids { return ids{s.Dir} }

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
// through env(1), since Rex sets none of its own, with WEAVER_MUX=rex so
// what weaver runs there acts on Rex, and WEAVER_WVR the weaver that
// started it. (Rex runs a command after the login shell's startup, whose
// PATH may put another wvr first: Homebrew's ahead of install.sh's.)
func layout(sp mux.Spawn, label string) map[string]any {
	opts := map[string]any{}
	if len(sp.Argv) > 0 {
		cmd := []string{"/usr/bin/env", "WEAVER_MUX=rex"}
		if self, err := os.Executable(); err == nil {
			cmd = append(cmd, "WEAVER_WVR="+self)
		}
		opts["command"] = append(append(cmd, sp.Env...), sp.Argv...)
	}
	if sp.Dir != "" {
		opts["cwd"] = sp.Dir
	}
	if sp.Keep != nil {
		opts["exit"] = map[string]any{"on_completion": false}
	}
	b := map[string]any{"flavor": terminal + ".shell", "options": opts}
	if label != "" {
		b["label"] = cut(label, 128)
	}
	return map[string]any{"block": b}
}

func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

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
// each terminal: every program weaver ran in it (home, agents, nvims)
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
			n, err := s.ids().num(w.Focused)
			return mux.PaneID(n), err == nil
		}
	}
	return "", false
}

// errUnseen is why Looking knows no pane.
var errUnseen = errors.New("rex: Rex does not say which session an app shows")

// Looking is no pane, and errUnseen: Rex does not say which session an
// app shows (it stays attached to every session it has shown), so no pane
// is known to be on screen.
func (s Session) Looking() ([]mux.PaneID, error) { return nil, errUnseen }

// client is a client attached to a session, as session.list_clients
// gives it.
type client struct {
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

func (s Session) PID(p mux.PaneID) (int, bool) {
	var pr struct {
		Child *proc `json:"child"`
	}
	if s.blockMethod(p, "process", nil, &pr) != nil || pr.Child == nil {
		return 0, false
	}
	return pr.Child.Pid, pr.Child.Pid > 0
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
// is known to be free in it: none for NextFree. An error when at is
// another window's.
func (s Session) placing(at int) (view, bool, error) {
	if at < 0 {
		return view{}, false, nil
	}
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
	if err := s.callS("session.new_window", map[string]any{"window_label": cut(w.Label, 128), "focus": !w.Background,
		"layout": layout(w.Spawn, "")}, &out); err != nil {
		return "", "", 0, err
	}
	if out.Window == "" || len(out.Blocks) == 0 {
		return "", "", 0, errors.New("rex: no window made")
	}
	wn, err := s.ids().num(out.Window)
	if err != nil {
		return "", "", 0, err
	}
	pn, err := s.ids().num(out.Blocks[0])
	if err != nil {
		return "", "", 0, err
	}
	_ = s.ids().note("cmd", pn, strings.Join(w.Argv, " "))
	_ = s.ids().note("view", wn, w.View)
	if w.At >= 0 {
		s.placeIn(v, seen, out.Window, mux.WindowID(wn), w.At)
	}
	if !w.Background {
		_ = s.showInApp(out.Window)
	}
	// Its one pane fills it: the window is as wide as the pane.
	return mux.WindowID(wn), mux.PaneID(pn), s.cols(mux.PaneID(pn)), nil
}

// cols is pane p's columns.
func (s Session) cols(p mux.PaneID) int {
	var sz struct {
		Columns int `json:"columns"`
	}
	_ = s.blockMethod(p, "size", nil, &sz)
	return sz.Columns
}

// SplitLeft gives no pid (0): Rex starts the program through the user's
// login shell, which becomes it only once its startup is done, so no
// process is the program's when the block is made.
func (s Session) SplitLeft(of mux.PaneID, n mux.NewSplit) (mux.PaneID, int, error) {
	anchor, err := s.block(of)
	if err != nil {
		return "", 0, err
	}
	ratio := 0.8
	if total := s.cols(of); n.Cols > 0 && total > 0 {
		ratio = min(max(float64(n.Cols)/float64(total), 0.05), 0.95)
	}
	var out struct {
		Blocks []string `json:"block_ids"`
	}
	if err := s.callS("session.new_split", map[string]any{"anchor_id": anchor, "direction": "horizontal", "side": "before",
		"ratio": ratio, "focus": !n.Background, "layout": layout(n.Spawn, n.Title)}, &out); err != nil {
		return "", 0, err
	}
	if len(out.Blocks) == 0 {
		return "", 0, errors.New("rex: no pane made")
	}
	pn, err := s.ids().num(out.Blocks[0])
	if err != nil {
		return "", 0, err
	}
	_ = s.ids().note("cmd", pn, strings.Join(n.Argv, " "))
	return mux.PaneID(pn), 0, nil
}

// BreakOut opens a window with a stand-in, moves p beside it and closes
// the stand-in: Rex makes windows only of new blocks. p fills its new
// window: its width is p's.
func (s Session) BreakOut(p mux.PaneID, label string, at int, background bool) (mux.WindowID, int, error) {
	b, err := s.block(p)
	if err != nil {
		return "", 0, err
	}
	v, seen, err := s.placing(at)
	if err != nil {
		return "", 0, err
	}
	var out struct {
		Window string   `json:"window_id"`
		Blocks []string `json:"block_ids"`
	}
	if err := s.callS("session.new_window", map[string]any{"window_label": cut(label, 128), "focus": !background,
		"layout": layout(mux.Spawn{Argv: []string{"/bin/sleep", "60"}}, "")}, &out); err != nil {
		return "", 0, err
	}
	if out.Window == "" || len(out.Blocks) == 0 {
		return "", 0, errors.New("rex: no window made")
	}
	stand := out.Blocks[0]
	if err := s.callS("session.move_block", map[string]any{"block_id": b, "anchor_block_id": stand}, nil); err != nil {
		_ = s.callS("session.close_window", map[string]any{"window_id": out.Window}, nil)
		return "", 0, err
	}
	_ = s.callS("block.close", map[string]any{"block_id": stand}, nil)
	wn, err := s.ids().num(out.Window)
	if err != nil {
		return "", 0, err
	}
	if at >= 0 {
		s.placeIn(v, seen, out.Window, mux.WindowID(wn), at)
	}
	if !background {
		_ = s.showInApp(out.Window)
		_ = s.callS("session.focus_block", map[string]any{"block_id": b}, nil)
	}
	return mux.WindowID(wn), s.cols(p), nil
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
	if focused {
		s.Focus(p)
	}
	return nil
}

func (s Session) Close(p mux.PaneID) {
	if b, err := s.block(p); err == nil {
		_ = s.callS("block.close", map[string]any{"block_id": b}, nil)
	}
}

func (s Session) CloseWindow(w mux.WindowID) {
	if id, err := s.window(w); err == nil {
		_ = s.callS("session.close_window", map[string]any{"window_id": id}, nil)
	}
}

// Place keeps place at for window w and moves it among the session's
// windows: before the first that weaver placed further on.
func (s Session) Place(w mux.WindowID, at int) {
	id, err := s.window(w)
	if err != nil {
		return
	}
	v, err := s.view()
	s.placeIn(v, err == nil, id, w, at)
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
		_ = s.callS("session.set_window_label", map[string]any{"window_id": id, "label": cut(label, 128)}, nil)
	}
}

func (s Session) Title(p mux.PaneID, title string) {
	if b, err := s.block(p); err == nil {
		_ = s.callS("session.set_block_label", map[string]any{"block_id": b, "label": cut(title, 128)}, nil)
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
		_ = s.callS("session.resize_pane", map[string]any{"before_block_id": before, "after_block_id": after,
			"direction": "horizontal", "ratio": ratio}, nil)
	}
}

func (s Session) Zoom(p mux.PaneID) {
	if b, err := s.block(p); err == nil {
		_ = s.callS("session.zoom_block", map[string]any{"block_id": b}, nil)
	}
}

// Show shows window w in the Rex app (showInApp); where the app will not,
// it is the session's active window at least.
func (s Session) Show(w mux.WindowID) {
	id, err := s.window(w)
	if err != nil {
		return
	}
	if s.showInApp(id) != nil {
		_ = s.callS("session.focus_window", map[string]any{"window_id": id}, nil)
	}
}

// Focus focuses pane p and shows its window in the Rex app (showInApp).
func (s Session) Focus(p mux.PaneID) {
	b, err := s.block(p)
	if err != nil {
		return
	}
	if v, err := s.view(); err == nil {
		if w := v.windowOf(b); w != "" {
			_ = s.showInApp(w)
		}
	}
	_ = s.callS("session.focus_block", map[string]any{"block_id": b}, nil)
}

// showInApp asks the Rex app to show window id (Rex's) of the session. The
// app keeps its own tab for a session, which the server's active window
// does not move: session.focus_window changes what the server calls
// active, not what you see, and the app sets it back to its tab when it
// next shows the session. So whatever is to be seen goes through the app,
// which takes actions only with Remote Control on (an error otherwise).
// Not for what happens behind you (a hook, threads restored at start): it
// takes the app to this session.
func (s Session) showInApp(id string) error {
	args := []string{"do", "session.select", "session_id=" + s.ID}
	if id != "" {
		args = append(args, "window_id="+id)
	}
	_, err := run(args...)
	return err
}

// Type pastes text (a paste event: the program sees one bracketed paste,
// so its newlines do not submit), then presses Enter once it has landed.
func (s Session) Type(p mux.PaneID, text string) error {
	b, err := s.block(p)
	if err != nil {
		return err
	}
	if err := s.callS("block.event", map[string]any{"block_id": b,
		"event": map[string]any{"type": "paste", "payload": map[string]any{"text": text}}}, nil); err != nil {
		return errors.New("rex would not paste into pane " + string(p))
	}
	time.Sleep(200 * time.Millisecond)
	_, err = run("send-key", "-s", s.ID, "-b", b, "enter")
	return err
}

// sayScript prints $1, the text (an argument: the shell never reads it),
// on a clear screen with no cursor and no wrapping (what does not fit is
// cut at the right, never scrolling its start away), then waits $2
// seconds.
const sayScript = `printf '\033[2J\033[H\033[?25l\033[?7l %s' "$1"; exec /bin/sleep "$2"`

// Say shows text for about ms milliseconds at the foot of the active
// window, as tmux's message line does: a floating layer that takes no
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
	text = head(text, max(cols-2, 1))
	w := min(width(text)+2, cols)
	fw, fh := float64(w)/float64(cols), 1/float64(lines)
	var out struct {
		Blocks []string `json:"block_ids"`
	}
	if s.callS("session.new_layer", map[string]any{"bounds": []float64{0, 1 - fh, fw, fh}, "focus": false,
		"layout": map[string]any{"block": map[string]any{"flavor": terminal + ".shell", "label": cut(text, 128), "options": map[string]any{
			"shell": "none", "size": map[string]any{"columns": w, "rows": 1},
			"exit":    map[string]any{"on_completion": true, "quick_exit_threshold_ms": 0},
			"command": []string{"/bin/sh", "-c", sayScript, "sh", text, strconv.FormatFloat(float64(ms)/1000, 'f', 3, 64)},
		}}}}, &out) == nil && len(out.Blocks) > 0 {
		_ = s.ids().note("said", said, out.Blocks[0])
	}
}

// SayToSession is Say: what the session shows is its active window.
func (s Session) SayToSession(ms int, text string) { s.Say(ms, text) }

// head is text cut to n columns, an ellipsis ending it when cut: its start
// always shows.
func head(text string, n int) string {
	if width(text) <= n {
		return text
	}
	w := 0
	for i, r := range text {
		if w+cells(r) > n-1 {
			return text[:i] + "…"
		}
		w += cells(r)
	}
	return text
}

// width is how many columns text takes in a terminal.
func width(text string) int {
	w := 0
	for _, r := range text {
		w += cells(r)
	}
	return w
}

// cells is how many columns rune r takes in a terminal: 2 for a wide one,
// none for a mark or format character that joins another (but 1 for VS16,
// which makes the symbol before it a wide emoji), else 1. It errs wide.
func cells(r rune) int {
	switch {
	case r == 0xFE0F:
		return 1
	case unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf):
		return 0
	}
	for _, x := range wide {
		if r >= x[0] && r <= x[1] {
			return 2
		}
	}
	return 1
}

// wide are the runes a terminal shows two columns wide: East Asian wide
// and fullwidth ones (Unicode's East_Asian_Width W and F) and emoji, a
// few narrow ones with them rather than a wide one left out.
var wide = [][2]rune{
	{0x1100, 0x115F}, {0x231A, 0x231B}, {0x2329, 0x232A}, {0x23E9, 0x23EC}, {0x23F0, 0x23F0}, {0x23F3, 0x23F3},
	{0x25FD, 0x25FE}, {0x2614, 0x2615}, {0x2648, 0x2653}, {0x267F, 0x267F}, {0x2693, 0x2693}, {0x26A1, 0x26A1},
	{0x26AA, 0x26AB}, {0x26BD, 0x26BE}, {0x26C4, 0x26C5}, {0x26CE, 0x26CE}, {0x26D4, 0x26D4}, {0x26EA, 0x26EA},
	{0x26F2, 0x26F3}, {0x26F5, 0x26F5}, {0x26FA, 0x26FA}, {0x26FD, 0x26FD}, {0x2705, 0x2705}, {0x270A, 0x270B},
	{0x2728, 0x2728}, {0x274C, 0x274C}, {0x274E, 0x274E}, {0x2753, 0x2755}, {0x2757, 0x2757}, {0x2795, 0x2797},
	{0x27B0, 0x27B0}, {0x27BF, 0x27BF}, {0x2B1B, 0x2B1C}, {0x2B50, 0x2B50}, {0x2B55, 0x2B55},
	{0x2E80, 0x303E}, {0x3041, 0x33FF}, {0x3400, 0x4DBF}, {0x4E00, 0x9FFF}, {0xA000, 0xA4CF}, {0xA960, 0xA97F},
	{0xAC00, 0xD7A3}, {0xF900, 0xFAFF}, {0xFE10, 0xFE19}, {0xFE30, 0xFE6F}, {0xFF00, 0xFF60}, {0xFFE0, 0xFFE6},
	{0x16FE0, 0x16FE4}, {0x17000, 0x18CFF}, {0x1AFF0, 0x1B2FF},
	{0x1F004, 0x1F004}, {0x1F0CF, 0x1F0CF}, {0x1F18E, 0x1F18E}, {0x1F191, 0x1F19A}, {0x1F200, 0x1F2FF},
	{0x1F300, 0x1F64F}, {0x1F680, 0x1F6FF}, {0x1F7E0, 0x1F7EB}, {0x1F90C, 0x1F9FF}, {0x1FA70, 0x1FAFF},
	{0x20000, 0x2FFFD}, {0x30000, 0x3FFFD},
}

func (s Session) MarkUnseen(mux.PaneID, bool) {}
func (s Session) Publish(mux.Counts)          {}
func (s Session) Strip([]mux.StripItem)       {}

// Popup opens argv in a floating layer over the active window, sized by
// size from the window's columns and lines; the command waits for it to
// end. With no app attached to the session, or no layer made, nothing is
// shown: false, and a command that fails to start.
func (s Session) Popup(size func(cols, lines int) (int, int), _ bool, dir string, argv []string) (*exec.Cmd, bool) {
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
