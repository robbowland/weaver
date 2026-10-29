// Package mux is what weaver needs of the terminal multiplexer its session
// runs on: its panes and windows, by ids weaver's files keep, and what
// weaver does with them. tmux is one; weaver's own policy (thread numbers,
// state files, what counts as seen) stays out of it.
package mux

import (
	"os/exec"

	"github.com/robbowland/weaver/internal/agent"
)

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

// Window is one window of a session: its place, its name, the view it
// shows (empty for none) and whether it is the one the session shows.
type Window struct {
	ID     WindowID
	Index  int
	Name   string
	View   string
	Width  int
	Active bool
}

// Spawn is a program for a new pane: argv in Dir (empty: the
// multiplexer's choice), with Env (KEY=VALUE) added to its environment.
type Spawn struct {
	Argv []string
	Dir  string
	Env  []string
}

// NewWindow is a window to open: Spawn in its one pane, named Label, at
// index At when At >= 0 (an error when that index is taken), showing View
// when set. Background leaves the session where it is.
type NewWindow struct {
	Spawn
	Label      string
	View       string
	At         int
	Background bool
}

// NewSplit is a pane to open on the left of another: Cols wide (0: most of
// the window), titled Title. Background keeps the focus where it is.
type NewSplit struct {
	Spawn
	Cols       int
	Title      string
	Background bool
}

// StripItem is one name on the strip of weaver's windows (THREADS, and
// each view): its view (empty: home), as shown, and whether it is set up.
type StripItem struct {
	View  string
	Label string
	Ready bool
}

// Mux is the session weaver acts on, on its multiplexer. Methods that
// steer what you see (focus, names, widths, marks) are best-effort and say
// nothing of failing; a caller that needs to know reads the state after.
type Mux interface {
	// Life is the life of the session: a session made again, on this
	// server or another, is another life. Zero when it is not running.
	Life() agent.Where
	// Made is when the session was made, epoch seconds.
	Made() (int64, bool)
	// Running: is the session running?
	Running() bool
	// Quit ends the session and every pane in it.
	Quit() error

	// Panes are the session's panes.
	Panes() ([]Pane, error)
	// Windows are the session's windows.
	Windows() ([]Window, error)
	// Looking are the panes the multiplexer's clients show.
	Looking() ([]PaneID, error)
	// Active is the session's active pane.
	Active() (PaneID, bool)
	// PID is the pid of the process pane p runs.
	PID(p PaneID) (int, bool)
	// Screen is what pane p shows, with its colours.
	Screen(p PaneID) []byte

	// OpenWindow opens a window: its id, its pane and its width.
	OpenWindow(w NewWindow) (WindowID, PaneID, int, error)
	// SplitLeft opens a pane on the left of pane of: the new pane and the
	// pid of its process.
	SplitLeft(of PaneID, s NewSplit) (PaneID, int, error)
	// BreakOut moves pane p to a window of its own, named label, at index at
	// when at >= 0 (an error when that is taken); focus goes with it.
	BreakOut(p PaneID, label string, at int, focus bool) (WindowID, error)
	// Respawn starts s again in pane p, ending what runs there.
	Respawn(p PaneID, s Spawn) error
	// KeepExited keeps pane p when its program exits, showing what (and
	// how it exited) and hint.
	KeepExited(p PaneID, what, hint string)
	// Close ends pane p, and CloseWindow window w.
	Close(p PaneID)
	CloseWindow(w WindowID)
	// Place moves window w to index at, when that is free.
	Place(w WindowID, at int)
	// Label names window w, and Title pane p.
	Label(w WindowID, label string)
	Title(p PaneID, title string)
	// SetWidth makes pane p cols wide; Zoom has it fill its window, or
	// back.
	SetWidth(p PaneID, cols int)
	Zoom(p PaneID)

	// Show shows window w; Focus pane p, its window first.
	Show(w WindowID)
	Focus(p PaneID)

	// Type pastes text into pane p as one paste (its newlines do not
	// submit), then presses Enter.
	Type(p PaneID, text string) error
	// Say shows text for ms milliseconds where you are; SayToSession, to
	// everyone looking at the session.
	Say(ms int, text string)
	SayToSession(ms int, text string)

	// MarkUnseen marks the window pane p is in as holding an agent done
	// and unseen, or clears it.
	MarkUnseen(p PaneID, on bool)
	// Publish shows the session's counts: agents that need you, done and
	// unseen, working.
	Publish(need, done, work int)
	// Strip shows the strip of weaver's windows.
	Strip(items []StripItem)
	// Popup is the command that shows argv over where you are, sized by
	// size from the terminal's columns and lines, bordered or not, in dir
	// (empty: the multiplexer's choice); it returns when the popup closes.
	// False when no terminal shows the session.
	Popup(size func(cols, lines int) (int, int), border bool, dir string, argv []string) (*exec.Cmd, bool)
}
