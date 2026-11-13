package wvr

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/cli"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/oneline"
	"github.com/robbowland/weaver/internal/session"
	"golang.org/x/term"
)

// Moving between agents: the board's jumps, Alt y (go home) and Alt 1
// (next), each of which counts the agent it reaches as seen.

// focus moves your client to pane p (its window first).
func (e *Env) focus(p mux.PaneID) { e.Mux().Focus(p) }

// Goto is `wvr _goto PANE TAB`: jump to an agent from the home board
// (which stays), to its helper when its wait is the helper's
// (session.Row.Waiter), and count it as seen; with no PANE, to the tab.
func (e *Env) Goto(pane, tab string) {
	p, ok := mux.ParsePane(pane)
	if !ok {
		if w, ok := mux.ParseWindow(tab); ok {
			e.Mux().Show(w)
		}
		return
	}
	if r, _, ok := e.RowOf(p); ok {
		p = r.Waiter()
	}
	e.focus(p)
	e.MarkSeen(p)
	e.PushStatus()
}

// isHome: is w home's window? The one weaver placed at 0 (its registry
// says), showing no view: never by its label, which anyone may change.
func isHome(w mux.Window) bool { return w.Index == 0 && w.View == "" }

// The session's panes and windows are fetched once for all an operation
// looks up in them (on Rex each fetch is a snapshot of the session), then
// looked up in that: session.PaneIn, windowPanes, windowIn.

// windowPanes are the panes of window w among ps.
func windowPanes(ps []mux.Pane, w mux.WindowID) []mux.PaneID {
	var in []mux.PaneID
	for _, q := range ps {
		if q.Window == w {
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

// GoHome is `wvr go home` (Alt y): from a thread, the board in a popover
// over it (peek), where tab goes on to the views; from home on through the
// views (THREADS, GITHUB, LINEAR), and from the last back to where you came
// from; from anywhere else (or with no terminal to show a popover on) home,
// made again first if it was closed.
func (e *Env) GoHome() {
	ws, cur, home := e.where()
	back := func() { e.goBack(ws, cur, home) }
	views := e.viewNames()
	at := -1
	if cur.View != "" {
		for i, v := range views {
			if v == cur.View {
				at = i
			}
		}
	}
	switch {
	case home != "" && cur.ID == home, at >= 0:
		if at+1 < len(views) {
			if _, err := e.OpenView(views[at+1]); err == nil {
				return
			}
		}
		back()
		return
	case cur.ID != "" && cur.View == "" && e.peek(cur, ""):
		return
	}
	e.toHome(cur, home)
}

// GoLocal is `wvr go local` (Alt Y): in a thread's session, the thread's
// own board in a popover over it, its agent and its helpers (peek); from
// anywhere else, or with no terminal to show a popover on, the board of
// every thread (GoHome).
func (e *Env) GoLocal() {
	if e.RexHere != "" && e.RexHere != e.RexID {
		if _, cur, _ := e.where(); cur.ID != "" && e.peek(cur, e.RexHere) {
			return
		}
	}
	e.GoHome()
}

// peek shows Alt y's popover over thread window cur (`wvr _peek`): the
// board, most of the terminal, its cursor on cur's agent, and on tab the
// views; with local, the board of the agents in Rex session local alone.
// It waits for the popover to close, then runs what was chosen there that
// the popover would have been in the way of: a jump, or a dialog (one
// popup at a time). False when no terminal looks at the session.
func (e *Env) peek(cur mux.Window, local string) bool {
	if e.SockDirReady() != nil {
		return false
	}
	f := e.SockDir + "/" + e.SKey + ".peek." + strconv.Itoa(os.Getpid())
	os.Remove(f)
	defer os.Remove(f)
	c, ok := e.Mux().Popup(peekSize.of, "", []string{e.Self, "_peek", f, string(e.agentIn(cur.ID)), local})
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

// Peek is `wvr _peek FILE [PANE [SESSION]]`, Alt y's popover over a
// thread: the board, its cursor on PANE's row, and on tab and shift-tab the
// views set up beside it, round as a tab bar goes; with SESSION (Alt Y's),
// the board of the agents in that Rex session alone (LS_SESSION, which
// its reloads inherit), no views. What is chosen there that needs the
// popover gone is written to FILE (writePeek), for `wvr go home` to run
// once it has closed.
func (e *Env) Peek(args []string) error {
	file, at, local := arg(args, 0), arg(args, 1), arg(args, 2)
	if file == "" {
		return errors.New("usage: wvr _peek FILE [PANE [SESSION]]")
	}
	pages := []string{"home"}
	if local != "" {
		os.Setenv("LS_SESSION", local)
	}
	for _, n := range e.viewNames() {
		if e.viewNeeds(n).state == viewReady && local == "" {
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
		if p.Window == w && e.agentOf(p.ID) == p.ID {
			return p.ID
		}
	}
	return ""
}

// toHome goes home from window cur (kept, when a thread's, for going back
// to), made again first if it was closed.
func (e *Env) toHome(cur mux.Window, home mux.WindowID) {
	if cur.ID != "" && cur.View == "" && cur.ID != home {
		bestEffort(os.WriteFile(e.lastFile(), []byte(string(cur.ID)+"\n"), 0o644))
	}
	e.selectHome(home)
	// The board's cursor on the thread you came from: what you were looking
	// at stays in view as you step back.
	if cur.ID != "" && cur.View == "" && cur.ID != home {
		e.boardOn(cur.ID)
	}
}

// GoView is `wvr go next|prev` (tab and shift-tab on home and in a view):
// the next or previous of home and the views, round as a tab bar goes.
func (e *Env) GoView(step int) {
	order := append([]string{"home"}, e.viewNames()...)
	_, cur, _ := e.where()
	at := 0
	for i, n := range order {
		if n == cur.View {
			at = i
		}
	}
	e.goTo(order[((at+step)%len(order)+len(order))%len(order)])
}

// showHome shows home, made again first if it was closed.
func (e *Env) showHome() {
	_, _, home := e.where()
	e.selectHome(home)
}

// selectHome shows home (window home), made again first if it was closed.
func (e *Env) selectHome(home mux.WindowID) {
	if home == "" {
		w := mux.NewWindow{Spawn: mux.Spawn{Argv: []string{e.Self, "home"}}, Label: mux.HomeName, At: 0}
		_, _, _, _ = e.Mux().OpenWindow(w) // best-effort: nothing more to try
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
	if i := rowOf(e.Rows(""), "a"+pane); i > 0 {
		return "pos(" + strconv.Itoa(i) + ")\n"
	}
	return ""
}

// where is the session's windows, the one you are on, and home.
func (e *Env) where() ([]mux.Window, mux.Window, mux.WindowID) {
	ws, _ := e.Mux().Windows()
	var cur mux.Window
	var home mux.WindowID
	for _, w := range ws {
		if w.Active && cur.ID == "" {
			cur = w
		}
		if isHome(w) && home == "" {
			home = w.ID
		}
	}
	return ws, cur, home
}

// lastFile keeps the thread's window Alt y left, for going back to it.
func (e *Env) lastFile() string { return e.StateDir + "/" + e.SKey + ".last" }

// GoBack is `wvr go back` (q and esc on home and in a view): back to the
// thread's window you came from; when that is gone (or was nowhere), home.
func (e *Env) GoBack() {
	ws, cur, home := e.where()
	e.goBack(ws, cur, home)
}

func (e *Env) goBack(ws []mux.Window, cur mux.Window, home mux.WindowID) {
	if l, _ := files.FirstLine(e.lastFile()); l != "" {
		last, _ := mux.ParseWindow(l)
		for _, w := range ws {
			if w.ID == last && w.View == "" && w.ID != home {
				e.Mux().Show(last)
				return
			}
		}
	}
	if home != "" && cur.ID != home {
		e.Mux().Show(home)
	}
}

// uiLock is one `wvr review` or `wvr next` per session at a time: it waits for the one at work (at most 10 s), looking every 20 ms so
// a queue of key presses keeps up. A lock older than 10 s was left by a
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
	bestEffort(os.MkdirAll(e.StateDir, 0o755))
	for i := 0; ; i++ {
		if os.Mkdir(d, 0o755) == nil {
			break
		}
		now := time.Now().Unix()
		t := now
		if l, err := files.FirstLine(d + "/at"); err == nil {
			if n, ok := cli.Atoi(l); ok {
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
		if i >= 500 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	bestEffort(os.WriteFile(d+"/at", []byte(strconv.FormatInt(time.Now().Unix(), 10)+"\n"), 0o644))
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
			// A helper merely done is its thread's to look at.
			if r.Helper() {
				continue
			}
			class = 1
		case session.NeedWait, session.NeedNothing:
			continue
		}
		// What the message says as you land: whose, and why.
		l := r.Called()
		if i := strings.IndexByte(l, '\n'); i >= 0 {
			l = l[:i]
		}
		l = ansi.Truncate(l, 40, "…")
		switch {
		case r.Tnum > 0:
			l = fmt.Sprintf("%02d %s", r.Tnum, l)
		case r.Helper():
			l = fmt.Sprintf("%02d › %s", r.Thread, cmp.Or(r.WindowName, l))
		}
		if r.State == agent.StateNeedsYou {
			l += " · needs you"
			if r.Waiting != "" {
				l += " · " + oneline.Cut(r.Waiting, 50)
			}
		} else {
			l += " · done"
		}
		cs = append(cs, c{class, r.Since, r.Pane, l})
	}
	if len(cs) == 0 {
		if term.IsTerminal(1) {
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
	// popup or a key's wvr is not a pane).
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
// has state, a thread record or a review panel), or the agent whose review
// panel it is.
func (e *Env) agentOf(p mux.PaneID) mux.PaneID {
	if !cli.Digits(string(p)) {
		return ""
	}
	for _, x := range []string{"json", "thread", "panel"} {
		if files.Exists(e.PaneFile(p, x)) {
			return p
		}
	}
	for _, f := range e.SessionFiles("panel") {
		if q, _ := files.FirstLine(f); q == string(p) {
			return session.PaneOfFile(f)
		}
	}
	return ""
}

// agentCols is the agent's columns in a thread window total columns wide:
// $WEAVER_AGENT_WIDTH of it (20%), at least $WEAVER_AGENT_MIN_COLS (44) as
// long as the review panel keeps 60; columns given outright as they are.
func agentCols(total int) int {
	w := os.Getenv("WEAVER_AGENT_WIDTH")
	pct := 20
	switch {
	case strings.HasSuffix(w, "%") && len(w) >= 2 && len(w) <= 3 && cli.Digits(w[:len(w)-1]) && w[0] != '0':
		pct, _ = strconv.Atoi(w[:len(w)-1])
	case len(w) >= 1 && len(w) <= 3 && cli.Digits(w) && w[0] != '0':
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
