// Package mux is what weaver needs of the terminal multiplexer its session
// runs on: its panes and windows, by ids weaver's files keep.
package mux

// PaneID is a pane as weaver's file names and records keep it: tmux's %3
// is "3".
type PaneID string

// WindowID is a window by its number: tmux's @12 is "12".
type WindowID string

// Target is the pane as tmux names it.
func (p PaneID) Target() string { return "%" + string(p) }

// Target is the window as tmux names it.
func (w WindowID) Target() string { return "@" + string(w) }

// Pane is one pane of a session.
type Pane struct {
	ID       PaneID
	Tab      WindowID
	TabName  string
	TabIndex int
	Exited   bool
	Cmd      string
	W, H     int
	X, Y     int
	Title    string
	Cwd      string
	Focused  bool
}
