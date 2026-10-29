package wvr

import (
	"errors"
	"os"
	"syscall"

	"github.com/robbowland/weaver/internal/cli"
	"github.com/robbowland/weaver/internal/files"
)

// Dialogs open as popups over where you are, each kind one size, so one
// looks and lands the same however it was asked for (a key, a click, a
// menu, a view), and what you were looking at stays behind it.

// popupSize is how big a kind of dialog is on a terminal: a share of it,
// within bounds (the least it needs, the most worth reading).
type popupSize struct {
	share      float64
	minW, maxW int
	hShare     float64
	minH, maxH int
}

var (
	// The new-thread form and its pickers: wide where there is room (paths
	// are long), as tall as its rows and a picker's need, so its keys stay
	// near what they act on.
	formSize = popupSize{0.62, 96, 160, 0, 22, 22}
	// A card: closing a thread, forgetting a lost one.
	cardSize = popupSize{0.5, 80, 120, 0, 14, 14}
	// A thread's new name: one line to type it on, with what it does.
	nameSize = popupSize{0.5, 72, 120, 0, 8, 8}
	// Alt y's popover over a thread: the board, most of the terminal, the
	// thread still there around it.
	peekSize = popupSize{0.9, 60, 1 << 16, 0.9, 16, 1 << 16}
)

// of is the size on a terminal cw x ch (never past its edges).
func (z popupSize) of(cw, ch int) (int, int) {
	w := min(max(int(float64(cw)*z.share), z.minW), z.maxW)
	h := min(max(int(float64(ch)*z.hShare), z.minH), z.maxH)
	if cw > 4 {
		w = min(w, cw-4)
	}
	if ch > 4 {
		h = min(h, ch-2)
	}
	return w, h
}

// formPopup opens `wvr ARGS` (the form) in a popup, in dir; the form draws
// its own border.
func (e *Env) formPopup(dir string, args ...string) error {
	return e.popup("the form", formSize, false, dir, args)
}

// cardPopup opens `wvr ARGS` (a card) in a popup with the multiplexer's
// border.
func (e *Env) cardPopup(args ...string) error {
	return e.popup("the card", cardSize, true, "", args)
}

// popup opens wvr with args (what it shows) in a popup of size z, bordered
// or not, in dir, over where you are, from a process of its own, so
// whatever asked (a list, waiting on its command) goes on at once.
func (e *Env) popup(what string, z popupSize, border bool, dir string, args []string) error {
	c, _ := e.Mux().Popup(z.of, border, dir, append([]string{e.Self}, args...))
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil {
		e.Mux().Say(2500, "weaver: could not open "+what)
		return cli.ErrSaid
	}
	return nil
}

// Popup is `wvr _popup new|close|forget ...`: the board's n, x on a thread,
// x on a lost one, as the dialogs Alt N and the menus open.
func (e *Env) Popup(args []string) error {
	switch firstArg(args) {
	case "new":
		dir, _ := os.Getwd()
		return e.formPopup(dir, args...)
	case "close":
		return e.cardPopup(args...)
	case "forget":
		return e.cardPopup(append([]string{"resume", "--forget"}, args[1:]...)...)
	case "adopt":
		// An agent you started by hand becomes a thread where it is, said on
		// the status line; anything else opens the picker of conversations.
		if p, ok := e.paneArg(arg(args, 1)); ok && files.Exists(e.StateFile(p)) && !files.Exists(e.ThreadFile(p)) {
			msg, err := e.adoptPane(p)
			if err != nil {
				msg = "weaver: " + err.Error()
			}
			e.Mux().Say(3000, msg)
			return nil
		}
		dir, _ := os.Getwd()
		return e.formPopup(dir, "adopt")
	case "rename":
		// Only a thread has a name to give it.
		p, ok := e.paneArg(arg(args, 1))
		if !ok || !files.Exists(e.ThreadFile(p)) {
			e.Mux().Say(3000, "weaver: only a thread can be renamed: adopt it first (a)")
			return nil
		}
		return e.popup("the prompt", nameSize, false, "", []string{"rename", string(p)})
	}
	return errors.New("usage: wvr _popup new [--from PANE] | close PANE | forget THREAD | adopt [PANE] | rename PANE")
}
