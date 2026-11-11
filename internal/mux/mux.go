// Package mux is what weaver needs of the terminal multiplexer its session
// runs on (Rex): its panes and windows, by ids weaver's files keep, and
// what weaver does with them. weaver's own policy (thread numbers, state
// files, what counts as seen) stays out of it.
package mux

import (
	"os/exec"
	"strings"

	"github.com/robbowland/weaver/internal/agent"
)

// PaneID is a pane as weaver's file names and records keep it: the number
// weaver gave its Rex block, "3".
type PaneID string

// WindowID is a window as weaver's files keep it: the number weaver gave
// its Rex window, "12".
type WindowID string

// ParsePane is the pane s names (its number); false when it names none.
func ParsePane(s string) (PaneID, bool) {
	s = strings.TrimSpace(s)
	return PaneID(s), digits(s)
}

// ParseWindow is the window s names (its number); false when it names
// none.
func ParseWindow(s string) (WindowID, bool) {
	s = strings.TrimSpace(s)
	return WindowID(s), digits(s)
}

func digits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// Pane is one pane of weaver's sessions, in Session (the multiplexer's
// id for it).
type Pane struct {
	ID          PaneID
	Session     string
	Window      WindowID
	WindowName  string
	WindowIndex int
	Exited      bool
	Cmd         string
	W, H        int
	X, Y        int
	Title       string
	Cwd         string
	Focused     bool
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
// Keep, when set, keeps the pane when the program exits, for what it said
// last to be read; nil, the pane goes with it.
type Spawn struct {
	Argv []string
	Dir  string
	Env  []string
	Keep *Exit
}

// Exit is what a pane kept when its program exits says, where its
// multiplexer can say it (Rex shows its own): What happened, how the
// program exited, and Hint, what to do next.
type Exit struct {
	What, Hint string
}

// HomeName is the name of home's window, weaver's board: the window so
// named that shows no view is home, wherever it is.
const HomeName = "home"

// NextFree is the index to open a window at, or break a pane out to, when
// any free one will do: the session's next.
const NextFree = -1

// NewWindow is a window to open: Spawn in its one pane, named Label, at
// index At (NextFree: the session's next free one; else an error when
// that index is taken), showing View when set. Background leaves the
// session where it is.
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
	// Active is the session's active pane.
	Active() (PaneID, bool)
	// PID is the pid of the process pane p runs.
	PID(p PaneID) (int, bool)
	// Screen is what pane p shows, with its colours.
	Screen(p PaneID) []byte

	// OpenWindow opens a window: its id, its pane and its width.
	OpenWindow(w NewWindow) (WindowID, PaneID, int, error)
	// OpenSession opens a session of its own for a thread, labelled
	// label, w its first window: the window, its pane and its width.
	OpenSession(label string, w NewWindow) (WindowID, PaneID, int, error)
	// CloseSession ends the thread's session pane p is in, and every pane
	// in it.
	CloseSession(p PaneID)
	// SplitLeft opens a pane on the left of pane of: the new pane and the
	// pid of its process, 0 when that is not known yet (Rex starts it
	// through the user's shell).
	SplitLeft(of PaneID, s NewSplit) (PaneID, int, error)
	// BreakOut moves pane p to a window of its own in the session, named
	// label, at index at (NextFree: its next free one; else an error when
	// that is taken): the window and its width. Background leaves the
	// session where it is; else the focus goes with the pane.
	BreakOut(p PaneID, label string, at int, background bool) (WindowID, int, error)
	// Respawn starts s again in pane p, ending what runs there.
	Respawn(p PaneID, s Spawn) error
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

	// Say shows text for ms milliseconds where you are; SayToSession, to
	// everyone looking at the session.
	Say(ms int, text string)
	SayToSession(ms int, text string)

	// Popup is the command that shows argv over where you are, sized by
	// size from the terminal's columns and lines, in dir (empty: the
	// multiplexer's choice); it returns when the popup closes. False when
	// no terminal shows the session.
	Popup(size func(cols, lines int) (int, int), dir string, argv []string) (*exec.Cmd, bool)
}
