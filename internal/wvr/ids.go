package wvr

import (
	"path/filepath"
	"strconv"
	"strings"
)

// PaneID is a tmux pane by its number, as weaver's file names and records
// keep it: tmux's %3 is "3".
type PaneID string

// WindowID is a tmux window by its number: tmux's @12 is "12".
type WindowID string

// target is the pane as tmux names it.
func (p PaneID) target() string { return "%" + string(p) }

// target is the window as tmux names it.
func (w WindowID) target() string { return "@" + string(w) }

// pane is the pane a record keeps as a number.
func (n jsonInt) pane() PaneID { return PaneID(strconv.Itoa(int(n))) }

// paneOfFile is the pane of one of weaver's per-agent files
// ($STATE_DIR/<skey>_<pane>.<ext>).
func paneOfFile(f string) PaneID {
	b := f[:len(f)-len(filepath.Ext(f))]
	return PaneID(b[strings.LastIndexByte(b, '_')+1:])
}

// parsePane is the pane s names: tmux's %3, zellij's terminal_3, or 3;
// false when it names none.
func parsePane(s string) (PaneID, bool) {
	s = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(s), "terminal_"), "%")
	return PaneID(s), digits(s)
}

// parseWindow is the window s names: tmux's @12, or 12; false when it
// names none.
func parseWindow(s string) (WindowID, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "@")
	return WindowID(s), digits(s)
}
