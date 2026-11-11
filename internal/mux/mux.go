// Package mux is weaver's words for what its sessions on Rex hold: panes
// and windows by the ids weaver's files keep, and the programs it starts
// in them (rex.Fleet acts on them). weaver's own policy (thread numbers,
// state files, what counts as seen) stays out of it.
package mux

import "strings"

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
