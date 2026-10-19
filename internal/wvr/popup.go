package wvr

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
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
	return e.popup("the form", formSize, []string{"-B", "-d", dir}, args)
}

// cardPopup opens `wvr ARGS` (a card) in a popup with tmux's border.
func (e *Env) cardPopup(args ...string) error {
	return e.popup("the card", cardSize, nil, args)
}

// popup opens wvr with args (what it shows) in a popup of size z (opts:
// its border, its directory) on the client looking at the session, from a
// process of its own, so whatever asked (a list, waiting on its command)
// goes on at once.
func (e *Env) popup(what string, z popupSize, opts, args []string) error {
	argv := []string{"display-popup"}
	// The client looking at this session (what asked may have none: tmux's
	// run-shell), and its size.
	cw, ch := 0, 0
	if out, err := e.Tmux("list-clients", "-t", "="+e.Session, "-F", "#{client_activity} #{client_name} #{client_width} #{client_height}"); err == nil {
		at := ""
		best := []string(nil)
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if f := strings.Fields(l); len(f) == 4 && f[0] >= at {
				best, at = f, f[0]
			}
		}
		if best != nil {
			argv = append(argv, "-c", best[1])
			cw, _ = strconv.Atoi(best[2])
			ch, _ = strconv.Atoi(best[3])
		}
	}
	w, h := z.of(cw, ch)
	argv = append(argv, "-E", "-w", strconv.Itoa(w), "-h", strconv.Itoa(h))
	argv = append(append(argv, opts...), "--", e.Self)
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
	case "adopt":
		// An agent you started by hand becomes a thread where it is, said on
		// the status line; anything else opens the picker of conversations.
		if p, ok := paneArg(arg(args, 1)); ok && files.Exists(e.StateFile(p)) && !files.Exists(e.ThreadFile(p)) {
			msg, err := e.adoptPane(p)
			if err != nil {
				msg = "weaver: " + err.Error()
			}
			e.Tmux("display-message", "-d", "3000", msg)
			return nil
		}
		dir, _ := os.Getwd()
		return e.formPopup(dir, "adopt")
	}
	return errors.New("usage: wvr _popup new [--from PANE] | close PANE | forget THREAD | adopt [PANE]")
}
