package wvr

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/robbowland/weaver/internal/cli"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/tmux"
)

// The mouse, as modern terminal apps have it (btop, lazygit, Textual's):
// whatever shows a key is a button for it (a footer's hints, the status
// line's THREADS, GITHUB and LINEAR and its counts, a card's choices); a
// click picks a row, a double click (or enter) opens it; the wheel scrolls
// what is under it; and a right click opens a menu of what can be done to
// what is under it, each with its key. Every one of them is a key first.

// hintLine is hs as a footer: each key in grey, what it does faint, gap
// spaces between.
func hintLine(hs []choice, gap int) string {
	var b strings.Builder
	for i, h := range hs {
		if i > 0 {
			b.WriteString(strings.Repeat(" ", gap))
		}
		b.WriteString(CGrey + h.key + COff)
		if h.what != "" {
			b.WriteString(" " + CDim + h.what + COff)
		}
	}
	return b.String()
}

// hintAt is the hint at column col (from 1) of hintLine(hs, gap).
func hintAt(hs []choice, gap, col int) (choice, bool) {
	at := 1
	for _, h := range hs {
		w := rlen(h.key)
		if h.what != "" {
			w += 1 + rlen(h.what)
		}
		if col >= at && col < at+w {
			return h, true
		}
		at += w + gap
	}
	return choice{}, false
}

// hintAct is what fzf does for a click on a hint: what its key does ("" for
// a hint that is no one key, as 1-9 and ↑↓ are). ^q is tmux's (quit), the
// rest fzf's own.
func hintAct(key string) string {
	switch {
	case key == "1-9", key == "↑↓":
		return ""
	case key == "⏎":
		return "trigger(enter)"
	case key == "^q":
		return "execute-silent(tmux send-keys -K C-q)"
	case strings.HasPrefix(key, "^"):
		return "trigger(ctrl-" + strings.ToLower(key[1:]) + ")"
	case strings.HasPrefix(key, "Alt "):
		return "trigger(alt-" + key[4:] + ")"
	}
	return "trigger(" + key + ")"
}

// footClick is the action for a click on footer hs (gap apart), at fzf's
// FZF_CLICK_FOOTER_COLUMN.
func footClick(hs []choice, gap int) string {
	col, _ := cli.EnvInt("FZF_CLICK_FOOTER_COLUMN")
	if h, ok := hintAt(hs, gap, col); ok {
		return hintAct(h.key) + "\n"
	}
	return ""
}

// Click is `wvr _click WHAT`: what a click does. On a footer (board, pick,
// form ROW), fzf's action for the hint clicked; on the empty board's
// welcome (title), its key line's; on the status line (status RANGE, the
// range clicked), it goes there.
func (e *Env) Click(args []string) {
	switch firstArg(args) {
	case "board":
		fmt.Print(footClick(boardHints(), 2))
	case "title":
		fmt.Print(welcomeClick(len(e.viewNames()) > 0))
	case "pick":
		fmt.Print(footClick(pickHints, 3))
	case "form":
		fmt.Print(footClick(formHints(arg(args, 1)), 3))
	case "status":
		e.statusClick(arg(args, 1))
	}
}

// statusClick goes where the status line's range r says: home (THREADS,
// and the working count), next (the counts of agents that need you or are
// done), or a view.
func (e *Env) statusClick(r string) {
	if e.Session == "" {
		return
	}
	switch r {
	case "home":
		_, cur, home := e.where()
		e.toHome(cur, home)
	case "next":
		e.Next()
	default:
		for _, n := range e.viewNames() {
			if n == r {
				bestEffort(e.View([]string{n}))
			}
		}
	}
}

// arg is argument i, or "".
func arg(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}

// menuItem is a line of a weaver menu: what it does, its key in the menu
// (the same as weaver's own for it, where it has one), and the tmux
// command it runs. The zero menuItem is a rule between groups.
type menuItem struct{ what, key, cmd string }

// showMenu shows ms as tmux's menu on client c at column x, under line y
// (over it when there is no room under it), or over the status line (y
// -1).
func (e *Env) showMenu(c string, x, y int, ms []menuItem) {
	if len(ms) == 0 || c == "" {
		return
	}
	cw, ch := 0, 0
	if out, err := e.Tmux("display-message", "-p", "-c", c, "#{client_width} #{client_height}"); err == nil {
		f := strings.Fields(string(out))
		if len(f) == 2 {
			cw, _ = strconv.Atoi(f[0])
			ch, _ = strconv.Atoi(f[1])
		}
	}
	if y < 0 {
		y = max(ch-1, 0)
	}
	w := 0
	for _, m := range ms {
		w = max(w, rlen(m.what)+rlen(m.key)+3)
	}
	w += 4
	h := len(ms) + 2
	// tmux sets a menu's bottom on the line before y.
	my := y + 1 + h
	if ch > 0 && my > ch-1 {
		my = y
	}
	if cw > 0 && x+w > cw {
		x = max(cw-w, 0)
	}
	// -M: it takes the mouse (opened by a command, not a mouse binding);
	// -O: the right click's release, off it, leaves it open.
	args := []string{"display-menu", "-MO", "-c", c, "-x", strconv.Itoa(x), "-y", strconv.Itoa(my)}
	for _, m := range ms {
		if m.what == "" {
			args = append(args, "")
			continue
		}
		args = append(args, strings.ReplaceAll(m.what, "#", "##"), m.key, m.cmd)
	}
	e.Tmux(args...)
}

// menuRun is a menu's tmux command for wvr ARGS, in the background.
func (e *Env) menuRun(args ...string) string {
	return "run-shell -b \"" + session.Quote(e.Self) + " " + strings.Join(args, " ") + " >/dev/null 2>&1 || true\""
}

// navMenu is what weaver's menus share: where to go (home, the views, the
// agent that needs you), a new thread, and quitting.
func (e *Env) navMenu() []menuItem {
	ms := []menuItem{{"threads", "y", e.menuRun("_click", "status", "home")}}
	for _, n := range e.viewNames() {
		ms = append(ms, menuItem{n, n[:1], e.menuRun("_click", "status", n)})
	}
	return append(ms, menuItem{"next that needs you", "1", e.menuRun("next")},
		menuItem{"new thread", "n", "send-keys -K M-N"})
}

// threadMenu is what can be done to the thread whose agent is pane a.
func (e *Env) threadMenu(a tmux.PaneID) []menuItem {
	if a == "" {
		return nil
	}
	return []menuItem{
		{"nvim", "v", e.menuRun("edit", a.Target())},
		{"rename thread", "r", e.menuRun("_popup", "rename", a.Target())},
		{"close thread", "x", e.menuRun("_popup", "close", a.Target())},
	}
}

// Menu is `wvr _menu WHERE ...`, a right click: the status line's (status
// CLIENT X), a pane's whose program takes no mouse (pane CLIENT X Y PANE),
// or a row of the board's (board KIND, from its fzf).
func (e *Env) Menu(args []string) {
	if e.Session == "" {
		return
	}
	num := func(i int) int { n, _ := strconv.Atoi(arg(args, i)); return n }
	quit := menuItem{"quit weaver", "q", "send-keys -K C-q"}
	switch firstArg(args) {
	case "status":
		c := arg(args, 1)
		var ms []menuItem
		if out, err := e.Tmux("display-message", "-p", "-c", c, "#{pane_id}"); err == nil {
			p, _ := tmux.ParsePane(string(out))
			if t := e.threadMenu(e.agentOf(p)); t != nil {
				ms = append(t, menuItem{})
			}
		}
		ms = append(ms, e.navMenu()...)
		ms = append(ms, menuItem{}, quit)
		e.showMenu(c, num(2), -1, ms)
	case "pane":
		p, _ := tmux.ParsePane(arg(args, 4))
		var ms []menuItem
		if t := e.threadMenu(e.agentOf(p)); t != nil {
			ms = append(t, menuItem{})
		}
		if out, err := e.Tmux("display-message", "-p", "#{buffer_size}"); err == nil && strings.TrimSpace(string(out)) != "" {
			ms = append(ms, menuItem{"paste", "p", "paste-buffer -p -t " + p.Target()}, menuItem{})
		}
		ms = append(ms, e.navMenu()...)
		ms = append(ms, menuItem{}, quit)
		e.showMenu(arg(args, 1), num(2), num(3), ms)
	case "board":
		e.rowMenu(boardMenu(arg(args, 1)))
	}
}

// rowMenu shows ms where the right click that opened it was (tmux's binding
// keeps that on the list's pane as @weaver-mouse: client, column, line),
// each item a key to the list, which acts on the row the click moved to.
func (e *Env) rowMenu(ms []menuItem) {
	p := os.Getenv("TMUX_PANE")
	if p == "" || len(ms) == 0 {
		return
	}
	for i := range ms {
		if ms[i].cmd != "" {
			ms[i].cmd = "send-keys -t " + p + " " + ms[i].cmd
		}
	}
	out, err := e.Tmux("display-message", "-p", "-t", p, "#{@weaver-mouse}")
	f := strings.Fields(string(out))
	if err != nil || len(f) != 3 {
		return
	}
	x, _ := strconv.Atoi(f[1])
	y, _ := strconv.Atoi(f[2])
	e.showMenu(f[0], x, y, ms)
}

// searching: is the list's search input showing (its keys typing, not
// acting)?
func searching() bool {
	in := os.Getenv("FZF_INPUT_STATE")
	return in != "" && in != "hidden"
}

// boardMenu is a board row's menu: a thread's (kind a…) or a lost one's
// (l…), as keys to the board; what is not a row has the board's own.
func boardMenu(kind string) []menuItem {
	var ms []menuItem
	switch {
	case strings.HasPrefix(kind, "a"):
		ms = []menuItem{{"jump to it", "", "Enter"}, {"nvim", "v", "C-e"}}
		if !searching() {
			ms = append(ms, menuItem{"rename", "r", "r"}, menuItem{"close", "x", "x"})
		}
	case strings.HasPrefix(kind, "l"):
		ms = []menuItem{{"resume", "", "Enter"}}
		if !searching() {
			ms = append(ms, menuItem{"forget", "x", "x"})
		}
	}
	if searching() {
		return ms
	}
	if len(ms) > 0 {
		ms = append(ms, menuItem{})
	}
	return append(ms, menuItem{"new thread", "n", "n"}, menuItem{"help", "?", "?"})
}
