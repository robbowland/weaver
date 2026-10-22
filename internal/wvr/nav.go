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
	"github.com/robbowland/weaver/internal/oneline"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/tmux"
)

// Moving between agents: the board's jumps, Alt y (go home), Alt 1 (next),
// and tmux's hooks as you look (seen) or resize (fit).

// focus moves your client to pane p (its window first).
func (e *Env) focus(p tmux.PaneID) {
	e.Tmux("select-window", "-t", p.Target(), ";", "select-pane", "-t", p.Target())
}

// Goto is `wvr _goto PANE TAB`: jump to an agent from the home board
// (which stays) and count it as seen; with no PANE, to the tab.
func (e *Env) Goto(pane, tab string) {
	p, ok := tmux.ParsePane(pane)
	if !ok {
		if w, ok := tmux.ParseWindow(tab); ok {
			e.Tmux("select-window", "-t", w.Target())
		}
		return
	}
	e.focus(p)
	e.MarkSeen(p)
	e.PushStatus()
}

type tab struct {
	id         tmux.WindowID
	name, view string
	active     bool
}

// us separates the fields of a tmux format's line.
const us = "\x1f"

func (e *Env) tabs() []tab {
	out, err := e.Tmux("list-windows", "-t", "="+e.Session, "-F", "#{window_id}"+us+"#{window_name}"+us+"#{window_active}"+us+"#{@weaver-view}")
	if err != nil {
		return nil
	}
	var ts []tab
	for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		f := strings.Split(l, us)
		if id, ok := tmux.ParseWindow(f[0]); ok && len(f) == 4 {
			ts = append(ts, tab{id, f[1], f[3], f[2] == "1"})
		}
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
	c, ok := e.popupCmd(peekSize, []string{"-B"}, []string{"_peek", f, string(e.agentIn(cur.id))})
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
func (e *Env) agentIn(w tmux.WindowID) tmux.PaneID {
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
func (e *Env) toHome(cur tab, home tmux.WindowID) {
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

// selectHome shows home (window home), made again first if it was closed.
func (e *Env) selectHome(home tmux.WindowID) {
	if home == "" {
		if _, err := e.Tmux("new-window", "-t", "="+e.Session+":0", "-n", "home", "-e", "WEAVER_TMUX_SESSION="+e.Session, e.Self, "home"); err != nil {
			e.Tmux("new-window", "-t", "="+e.Session+":", "-n", "home", "-e", "WEAVER_TMUX_SESSION="+e.Session, e.Self, "home")
		}
		return
	}
	e.Tmux("select-window", "-t", home.Target())
}

// boardOn puts the boards' cursors on the thread whose window is w (its
// row, as the board's own rows place it: _pos).
func (e *Env) boardOn(w tmux.WindowID) {
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
func (e *Env) where() ([]tab, tab, tmux.WindowID) {
	ts := e.tabs()
	var cur tab
	var home tmux.WindowID
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

func (e *Env) goBack(ts []tab, cur tab, home tmux.WindowID) {
	if l, _ := files.FirstLine(e.lastFile()); l != "" {
		last, _ := tmux.ParseWindow(l)
		for _, t := range ts {
			if t.id == last && t.view == "" && t.id != home {
				e.Tmux("select-window", "-t", last.Target())
				return
			}
		}
	}
	if home != "" && cur.id != home {
		e.Tmux("select-window", "-t", home.Target())
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
		pane  tmux.PaneID
		tab   tmux.WindowID
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
			e.Say(2000, "weaver: no agent needs you")
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
	e.Say(2500, fmt.Sprintf("weaver: [%d/%d] %s", pick+1, len(cs), cs[pick].label))
	e.PushStatus()
}

// agentOf is the agent of pane p: the pane itself when it is an agent (it
// has state, a thread record or an nvim), or the agent whose nvim it is.
func (e *Env) agentOf(p tmux.PaneID) tmux.PaneID {
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
	out, err := e.Tmux("display-message", "-p", "-t", win.Target(), "#{window_active}:#{session_attached}:#{pane_id}")
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
	if ps, err := e.Tmux("list-panes", "-t", win.Target(), "-F", "#{pane_id}"); err == nil {
		for _, id := range strings.Fields(string(ps)) {
			p, _ := tmux.ParsePane(id)
			if l, _ := files.FirstLine(e.StateFile(p)); doneUnseen(l) {
				left = true
			}
		}
	}
	if !left {
		e.Tmux("set-option", "-w", "-u", "-t", win.Target(), "@weaver-unseen")
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
	out, err := e.Tmux("list-panes", "-t", win.Target(), "-F", "#{pane_id}")
	if err != nil {
		return
	}
	ps := strings.Fields(string(out))
	for _, id := range ps {
		p, _ := tmux.ParsePane(id)
		if !files.Exists(e.ThreadFile(p)) {
			continue
		}
		if e.NvimPane(p) == "" || len(ps) != 2 {
			continue
		}
		t, err := e.Tmux("display-message", "-p", "-t", p.Target(), "#{window_width}")
		total, ok := atoi(strings.TrimSpace(string(t)))
		if err != nil || !ok {
			continue
		}
		e.Tmux("resize-pane", "-t", p.Target(), "-x", strconv.Itoa(agentCols(total)))
	}
}
