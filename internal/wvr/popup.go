package wvr

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/robbowland/weaver/internal/cli"
)

// Dialogs open as popups over where you are, each kind one size, so one
// looks and lands the same however it was asked for (a key, a click, a
// menu, a view), and what you were looking at stays behind it.

const (
	// The new-thread form and its pickers (Alt N's binding says the same).
	formPopupW, formPopupH = 96, 20
	// A card: closing a thread, forgetting a lost one.
	cardPopupW, cardPopupH = 80, 12
)

// formPopup opens `wvr ARGS` (the form) in a popup, in dir; the form draws
// its own border.
func (e *Env) formPopup(dir string, args ...string) error {
	return e.popup("the form", []string{"-B", "-w", strconv.Itoa(formPopupW), "-h", strconv.Itoa(formPopupH), "-d", dir}, args)
}

// cardPopup opens `wvr ARGS` (a card) in a popup with tmux's border.
func (e *Env) cardPopup(args ...string) error {
	return e.popup("the card", []string{"-w", strconv.Itoa(cardPopupW), "-h", strconv.Itoa(cardPopupH)}, args)
}

// popup opens wvr with args (what it shows) in a popup (opts: its place
// and border) on the client looking at the session, from a process of its
// own, so whatever asked (a list, waiting on its command) goes on at once.
func (e *Env) popup(what string, opts, args []string) error {
	argv := []string{"display-popup"}
	// The client looking at this session: what asked may have none
	// (tmux's run-shell).
	if out, err := e.Tmux("list-clients", "-t", "="+e.Session, "-F", "#{client_activity} #{client_name}"); err == nil {
		best, at := "", ""
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if t, c, ok := strings.Cut(l, " "); ok && t >= at {
				best, at = c, t
			}
		}
		if best != "" {
			argv = append(argv, "-c", best)
		}
	}
	argv = append(append(append(argv, "-E"), opts...), "--", e.Self)
	argv = append(argv, args...)
	if !e.InTmux {
		argv = append(append([]string{}, e.TSock...), argv...)
	}
	c := exec.Command("tmux", argv...)
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil {
		e.Tmux("display-message", "-d", "2500", "weaver: could not open "+what)
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
	}
	return errors.New("usage: wvr _popup new [--from PANE] | close PANE | forget THREAD")
}
