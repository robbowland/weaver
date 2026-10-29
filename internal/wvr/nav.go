package wvr

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/cli"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/oneline"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/tmux"
)

// Moving between agents: the board's jumps, Alt y (go home), Alt 1 (next),
// and tmux's hooks as you look (seen) or resize (fit).

// focus moves your client to pane p (its window first).
func (e *Env) focus(p mux.PaneID) { e.Mux().Focus(p) }

// Goto is `wvr _goto PANE TAB`: jump to an agent from the home board
// (which stays) and count it as seen; with no PANE, to the tab.
func (e *Env) Goto(pane, tab string) {
	p, ok := tmux.ParsePane(pane)
	if !ok {
		if w, ok := tmux.ParseWindow(tab); ok {
			e.Mux().Show(w)
		}
		return
	}
	e.focus(p)
	e.MarkSeen(p)
	e.PushStatus()
}

type tab struct {
	id         mux.WindowID
	name, view string
	active     bool
}

// The session's panes and windows are fetched once for all an operation
// looks up in them (on Rex each fetch is a snapshot of the session), then
// looked up in that: session.PaneIn, windowPanes, windowIn.

// windowPanes are the panes of window w among ps.
func windowPanes(ps []mux.Pane, w mux.WindowID) []mux.PaneID {
	var in []mux.PaneID
	for _, q := range ps {
		if q.Tab == w {
			in = append(in, q.ID)
		}
	}
	return in
}

// windowIn is window w among ws.
func windowIn(ws []mux.Window, w mux.WindowID) (mux.Window, bool) {
	for _, x := range ws {
		if x.ID == w {
			return x, true
		}
	}
	return mux.Window{}, false
}

// windowWidth is window w's columns, as the session's windows list it now.
func (e *Env) windowWidth(w mux.WindowID) (int, bool) {
	ws, _ := e.Mux().Windows()
	x, ok := windowIn(ws, w)
	return x.Width, ok && x.Width > 0
}

func (e *Env) tabs() []tab {
	ws, _ := e.Mux().Windows()
	var ts []tab
	for _, w := range ws {
		ts = append(ts, tab{w.ID, w.Name, w.View, w.Active})
	}
	return ts
}

// GoHome is `wvr go home` (Alt y): from a thread, the board in a popover
// over it (peek), where tab goes on to the views; from home on through the
// views (THREADS, GITHUB, LINEAR), and from the last back to where you came
// from; from anywhere else (or with no terminal to show a popover on) home,
// made again first if it was closed.
func (e *Env) GoHome() {
	ts, cur, home := e.where()
	back := func() { e.goBack(ts, cur, home) }
	views := e.viewNames()
	at := -1
	if cur.view != "" {
		for i, v := range views {
			if v == cur.view {
				at = i
			}
		}
	}
	switch {
	case home != "" && cur.id == home, at >= 0:
		if at+1 < len(views) {
			if _, err := e.OpenView(views[at+1]); err == nil {
				return
			}
		}
		back()
		return
	case cur.id != "" && cur.view == "" && e.peek(cur):
		return
	}
	e.toHome(cur, home)
}

// peek shows Alt y's popover over thread window cur (`wvr _peek`): the
// board, most of the terminal, its cursor on cur's thread, and on tab the
// views. It waits for the popover to close, then runs what was chosen there
// that the popover would have been in the way of: a jump, or a dialog (tmux
// shows one popup at a time). False when no terminal looks at the session.
func (e *Env) peek(cur tab) bool {
	if e.SockDirReady() != nil {
		return false
	}
	f := e.SockDir + "/" + e.SKey + ".peek." + strconv.Itoa(os.Getpid())
	os.Remove(f)
	defer os.Remove(f)
	c, ok := e.Mux().Popup(peekSize.of, false, "", []string{e.Self, "_peek", f, string(e.agentIn(cur.id))})
	if !ok || c.Run() != nil {
		return false
	}
	b, _ := os.ReadFile(f)
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l != "" {
			bestEffort(exec.Command(e.Self, strings.Split(l, "\t")...).Run())
		}
	}
	return true
}

// Peek is `wvr _peek FILE [PANE]`, Alt y's popover over a thread: the
// board, its cursor on PANE's row, and on tab and shift-tab the views set
// up beside it, round as a tab bar goes. What is chosen there that needs
// the popover gone is written to FILE (writePeek), for `wvr go home` to run
// once it has closed.
func (e *Env) Peek(args []string) error {
	file, at := arg(args, 0), arg(args, 1)
	if file == "" {
		return errors.New("usage: wvr _peek FILE [PANE]")
	}
	pages := []string{"home"}
	for _, n := range e.viewNames() {
		if e.viewNeeds(n).state == viewReady {
			pages = append(pages, n)
		}
	}
	i := 0
	for {
		step, err := "", error(nil)
		if src, ok := itemSourceOf(pages[i]); ok {
			step, err = e.itemsView(src, file)
		} else {
			step, err = e.ls(file, at)
		}
		switch {
		case err != nil:
			return err
		case step == "next":
			i = (i + 1) % len(pages)
		case step == "prev":
			i = (i + len(pages) - 1) % len(pages)
		default:
			return nil
		}
	}
}

// agentIn is the agent of window w's thread; "" when it has none.
func (e *Env) agentIn(w mux.WindowID) mux.PaneID {
	panes, _ := e.Panes()
	for _, p := range panes {
		if p.Tab == w && e.agentOf(p.ID) == p.ID {
			return p.ID
		}
	}
	return ""
}

// toHome goes home from window cur (kept, when a thread's, for going back
// to), made again first if it was closed.
func (e *Env) toHome(cur tab, home mux.WindowID) {
	if cur.id != "" && cur.view == "" && cur.id != home {
		bestEffort(os.WriteFile(e.lastFile(), []byte(string(cur.id)+"\n"), 0o644))
	}
	e.selectHome(home)
	// The board's cursor on the thread you came from: what you were looking
	// at stays in view as you step back.
	if cur.id != "" && cur.view == "" && cur.id != home {
		e.boardOn(cur.id)
	}
}

// GoView is `wvr go next|prev` (tab and shift-tab on home and in a view):
// the next or previous of home and the views, round as a tab bar goes.
func (e *Env) GoView(step int) {
	order := append([]string{"home"}, e.viewNames()...)
	_, cur, _ := e.where()
	at := 0
	for i, n := range order {
		if n == cur.view {
			at = i
		}
	}
	e.statusClick(order[((at+step)%len(order)+len(order))%len(order)])
}

// showHome shows home, made again first if it was closed.
func (e *Env) showHome() {
	_, _, home := e.where()
	e.selectHome(home)
}

// selectHome shows home (window home), made again first if it was closed.
func (e *Env) selectHome(home mux.WindowID) {
	if home == "" {
		w := mux.NewWindow{Spawn: mux.Spawn{Argv: []string{e.Self, "home"}}, Label: "home", At: 0}
		if _, _, _, err := e.Mux().OpenWindow(w); err != nil {
			w.At = -1
			_, _, _, _ = e.Mux().OpenWindow(w) // 0 is taken: wherever it goes; nothing more to try
		}
		return
	}
	e.Mux().Show(home)
}

// boardOn puts the boards' cursors on the thread whose window is w (its
// row, as the board's own rows place it: _pos).
func (e *Env) boardOn(w mux.WindowID) {
	p := e.agentIn(w)
	if p == "" {
		return
	}
	q := session.Quote(e.Self)
	socks, _ := filepath.Glob(e.SockDir + "/" + e.SKey + ".fzf.*.sock")
	for _, s := range socks {
		session.FzfPost(s, "transform("+q+" _pos "+string(p)+")")
	}
}

// Pos is `wvr _pos PANE`, in a board's fzf: the action that puts its
// cursor on PANE's row (nothing when it has none).
func (e *Env) Pos(pane string) string {
	if i := rowOf(e.Rows(), "a"+pane); i > 0 {
		return "pos(" + strconv.Itoa(i) + ")\n"
	}
	return ""
}

// where is the session's windows, the one you are on, and home.
func (e *Env) where() ([]tab, tab, mux.WindowID) {
	ts := e.tabs()
	var cur tab
	var home mux.WindowID
	for _, t := range ts {
		if t.active && cur.id == "" {
			cur = t
		}
		if t.name == "home" && t.view == "" && home == "" {
			home = t.id
		}
	}
	return ts, cur, home
}

// lastFile keeps the thread's window Alt y left, for going back to it.
func (e *Env) lastFile() string { return e.StateDir + "/" + e.SKey + ".last" }

// GoBack is `wvr go back` (q and esc on home and in a view): back to the
// thread's window you came from; when that is gone (or was nowhere), home.
func (e *Env) GoBack() {
	ts, cur, home := e.where()
	e.goBack(ts, cur, home)
}

func (e *Env) goBack(ts []tab, cur tab, home mux.WindowID) {
	if l, _ := files.FirstLine(e.lastFile()); l != "" {
		last, _ := tmux.ParseWindow(l)
		for _, t := range ts {
			if t.id == last && t.view == "" && t.id != home {
				e.Mux().Show(last)
				return
			}
		}
	}
	if home != "" && cur.id != home {
		e.Mux().Show(home)
	}
}

// uiLock is one `wvr edit` or `wvr next` per session at a time: it waits
// for the one at work (at most 10 s). A lock older than 10 s was left by a
// killed process. Its release.
func (e *Env) uiLock() func() {
	release, _ := e.takeUILock(false)
	return release
}

// tryUILock is uiLock for a key pressed again while the first press is at
// work: dropped (false) rather than waiting.
func (e *Env) tryUILock() (func(), bool) {
	return e.takeUILock(true)
}

func (e *Env) takeUILock(nowait bool) (func(), bool) {
	d := e.StateDir + "/" + e.SKey + ".ui"
	os.MkdirAll(e.StateDir, 0o755)
	for i := 0; ; i++ {
		if os.Mkdir(d, 0o755) == nil {
			break
		}
		now := time.Now().Unix()
		t := now
		if l, err := files.FirstLine(d + "/at"); err == nil {
			if n, ok := atoi(l); ok {
				t = int64(n)
			}
		}
		if now-t > 10 {
			os.RemoveAll(d)
			continue
		}
		if nowait {
			return func() {}, false
		}
		if i >= 100 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	os.WriteFile(d+"/at", []byte(strconv.FormatInt(time.Now().Unix(), 10)+"\n"), 0o644)
	return func() { os.RemoveAll(d) }, true
}

// Next is `wvr next` (Alt 1): to the agent that has needed you longest;
// failing that, the one that finished longest ago and you haven't looked at
// yet. Pressed again on one of those, on to the next, round and round.
func (e *Env) Next() {
	// Pressed again while the last press is still at work: that one moves
	// you, so this one does nothing.
	release, ok := e.tryUILock()
	if !ok {
		return
	}
	defer release()
	rows, now := e.AgentRows()
	type c struct {
		class int
		since int64
		pane  mux.PaneID
		tab   mux.WindowID
		label string
	}
	var cs []c
	for _, r := range rows {
		if r.Pane == "" {
			continue
		}
		// Who needs you first, then who is done and unseen.
		class := 0
		switch r.Need(now) {
		case session.NeedYou:
		case session.NeedLook:
			class = 1
		case session.NeedWait, session.NeedNothing:
			continue
		}
		// What tmux's message line says as you land: whose, and why.
		l := r.Called()
		if i := strings.IndexByte(l, '\n'); i >= 0 {
			l = l[:i]
		}
		if rlen(l) > 40 {
			l = oneline.Cut(l, 39) + "…"
		}
		if r.Tnum > 0 {
			l = fmt.Sprintf("%02d %s", r.Tnum, l)
		}
		if r.State == agent.StateNeedsYou {
			l += " · needs you"
			if r.Waiting != "" {
				l += " · " + oneline.Cut(r.Waiting, 50)
			}
		} else {
			l += " · done"
		}
		cs = append(cs, c{class, r.Since, r.Pane, r.Tab, l})
	}
	if len(cs) == 0 {
		if cli.IsTTY(1) {
			fmt.Println("weaver: no agent needs you")
			time.Sleep(time.Second)
		} else {
			e.Mux().Say(2000, "weaver: no agent needs you")
		}
		return
	}
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].class != cs[j].class {
			return cs[i].class < cs[j].class
		}
		return cs[i].since < cs[j].since
	})
	// Where you are now, to go on from there: the session's active pane (a
	// popup or run-shell is not a pane).
	focused := e.focusedPane()
	at := -1
	for i, x := range cs {
		if x.pane == focused {
			at = i
		}
	}
	pick := (at + 1) % len(cs)
	e.focus(cs[pick].pane)
	e.MarkSeen(cs[pick].pane)
	e.Mux().Say(2500, fmt.Sprintf("weaver: [%d/%d] %s", pick+1, len(cs), cs[pick].label))
	e.PushStatus()
}

// agentOf is the agent of pane p: the pane itself when it is an agent (it
// has state, a thread record or an nvim), or the agent whose nvim it is.
func (e *Env) agentOf(p mux.PaneID) mux.PaneID {
	if !digits(string(p)) {
		return ""
	}
	for _, x := range []string{"json", "thread", "nvim"} {
		if files.Exists(e.PaneFile(p, x)) {
			return p
		}
	}
	for _, f := range e.SessionFiles("nvim") {
		if q, _ := files.FirstLine(f); q == string(p) {
			return session.PaneOfFile(f)
		}
	}
	return ""
}

func doneUnseen(l string) bool {
	return strings.Contains(l, `"state":"done"`) && !strings.Contains(l, `"seen":true`)
}

// Seen is `wvr _seen WINDOW` (tmux's pane-focus-in hook, 1.5 s after you
// focus a window flagged as holding an agent done and unseen): if you are
// still on it, the agent you are looking at is seen.
func (e *Env) Seen(window string) {
	win, ok := tmux.ParseWindow(window)
	if !ok {
		return
	}
	out, err := e.Tmux("display-message", "-p", "-t", tmux.WindowTarget(win), "#{window_active}:#{session_attached}:#{pane_id}")
	if err != nil {
		return
	}
	info := strings.TrimSpace(string(out))
	f := strings.SplitN(info, ":", 3)
	if len(f) != 3 || f[0] != "1" || f[1] == "" || f[1] == "0" || !strings.HasPrefix(f[2], "%") {
		return
	}
	p, _ := tmux.ParsePane(f[2])
	a := e.agentOf(p)
	if a == "" {
		return
	}
	if l, _ := files.FirstLine(e.StateFile(a)); doneUnseen(l) {
		e.MarkSeen(a)
	}
	left := false
	ps, _ := e.Mux().Panes()
	for _, q := range windowPanes(ps, win) {
		if l, _ := files.FirstLine(e.StateFile(q)); doneUnseen(l) {
			left = true
		}
	}
	if !left {
		e.Mux().MarkUnseen(p, false)
	}
	e.PushStatus()
}

// agentCols is the agent's columns in a thread window total columns wide:
// $WEAVER_AGENT_WIDTH of it (20%), at least $WEAVER_AGENT_MIN_COLS (44) as
// long as nvim keeps 60; columns given outright as they are.
func agentCols(total int) int {
	w := os.Getenv("WEAVER_AGENT_WIDTH")
	pct := 20
	switch {
	case strings.HasSuffix(w, "%") && len(w) >= 2 && len(w) <= 3 && digits(w[:len(w)-1]) && w[0] != '0':
		pct, _ = strconv.Atoi(w[:len(w)-1])
	case len(w) >= 1 && len(w) <= 3 && digits(w) && w[0] != '0':
		n, _ := strconv.Atoi(w)
		return n
	}
	least := 44
	if n, ok := cli.EnvInt("WEAVER_AGENT_MIN_COLS"); ok {
		least = n
	}
	c := total * pct / 100
	if least > total-60 {
		least = total - 60
	}
	if c < least {
		c = least
	}
	return c
}

// Fit is `wvr _fit WINDOW` (tmux's window-resized hook): its thread's
// agent back to its width, since tmux takes a narrower window's columns from
// the rightmost pane, the agent's.
func (e *Env) Fit(window string) {
	win, ok := tmux.ParseWindow(window)
	if !ok {
		return
	}
	all, _ := e.Mux().Panes()
	ps := windowPanes(all, win)
	if len(ps) != 2 {
		return
	}
	total := 0
	for _, p := range ps {
		if !files.Exists(e.ThreadFile(p)) || e.NvimPane(p) == "" {
			continue
		}
		if total == 0 {
			var ok bool
			if total, ok = e.windowWidth(win); !ok {
				return
			}
		}
		e.Mux().SetWidth(p, agentCols(total))
	}
}
