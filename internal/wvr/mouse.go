package wvr

import (
	"fmt"
	"os"
	"strings"

	"github.com/robbowland/weaver/internal/cli"
	"github.com/robbowland/weaver/internal/session"
)

// The mouse, as modern terminal apps have it (btop, lazygit, Textual's):
// whatever shows a key is a button for it (a footer's hints, a card's
// choices); a click picks a row, a double click (or enter) opens it; and
// the wheel scrolls what is under it. Every one of them is a key first.

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
// a hint that is no one key, as 1-9 and ↑↓ are). ctrl+q is Rex's (quit,
// after asking: wvr, as q quotes it), the rest fzf's own.
func hintAct(key, q string) string {
	switch key {
	case "1-9", "↑↓", "!", "…":
		return ""
	case "ctrl+q":
		return "execute-silent(" + q + " _popup quit)"
	}
	return "trigger(" + fzfKey(key) + ")"
}

// footClick is the action for a click on footer hs (gap apart), at fzf's
// FZF_CLICK_FOOTER_COLUMN; q quotes wvr.
func footClick(hs []choice, gap int, q string) string {
	col, _ := cli.EnvInt("FZF_CLICK_FOOTER_COLUMN")
	if h, ok := hintAt(hs, gap, col); ok {
		return hintAct(h.key, q) + "\n"
	}
	return ""
}

// Click is `wvr _click WHAT`: what a click does. On a footer (board, pick,
// form ROW), fzf's action for the hint clicked; on the empty board's
// welcome (title), its key line's.
func (e *Env) Click(args []string) {
	q := session.Quote(e.Self)
	switch arg(args, 0) {
	case "board":
		fmt.Print(footClick(e.boardHints(), 2, q))
	case "title":
		fmt.Print(welcomeClick(len(e.viewNames()) > 0, q))
	case "pick":
		fmt.Print(footClick(pickHints, 3, q))
	case "form":
		fmt.Print(footClick(formHints(arg(args, 1)), 3, q))
	}
}

// goTo goes where r says: home, next (the agent that needs you), or a
// view.
func (e *Env) goTo(r string) {
	if e.Session == "" {
		return
	}
	switch r {
	case "home":
		_, cur, home := e.where()
		e.toHome(cur, home)
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

// searching: is the list's search input showing (its keys typing, not
// acting)?
func searching() bool {
	in := os.Getenv("FZF_INPUT_STATE")
	return in != "" && in != "hidden"
}
