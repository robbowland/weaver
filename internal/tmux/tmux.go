// Package tmux is what weaver reads of tmux: its pane and window ids, and
// the panes of a session as list-panes lists them.
package tmux

import (
	"strconv"
	"strings"

	"github.com/robbowland/weaver/internal/mux"
)

// PaneID, WindowID and Pane are mux's: tmux's %3 is "3", @12 is "12".
type (
	PaneID   = mux.PaneID
	WindowID = mux.WindowID
	Pane     = mux.Pane
)

// Text is s as tmux takes it literally where it would expand formats: a
// window's or pane's name, a start directory, a message. Text weaver does
// not write (an issue's title, an agent's, a branch, a path) goes through
// it, so its #{...} and #[...] stay what they say.
func Text(s string) string { return strings.ReplaceAll(s, "#", "##") }

// ParsePane is the pane s names: tmux's %3, zellij's terminal_3, or 3;
// false when it names none.
func ParsePane(s string) (PaneID, bool) {
	s = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(s), "terminal_"), "%")
	return PaneID(s), digits(s)
}

// ParseWindow is the window s names: tmux's @12, or 12; false when it
// names none.
func ParseWindow(s string) (WindowID, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "@")
	return WindowID(s), digits(s)
}

const us = "\x1f"

// PanesFormat is the list-panes format (-F) ParsePanes reads.
var PanesFormat = strings.Join([]string{"#{pane_id}", "#{window_id}", "#{window_name}", "#{window_index}", "#{pane_dead}",
	"#{pane_start_command}", "#{pane_width}", "#{pane_height}", "#{pane_left}", "#{pane_top}", "#{pane_title}",
	"#{pane_current_path}", "#{pane_active}"}, us)

// ParsePanes are the panes list-panes printed in PanesFormat; a line it
// cannot read is left out.
func ParsePanes(out []byte) []Pane {
	var ps []Pane
	for _, l := range strings.Split(string(out), "\n") {
		x := strings.Split(l, us)
		if len(x) != 13 {
			continue
		}
		p := Pane{TabName: x[2], Exited: x[4] == "1", Cmd: x[5], Title: x[10], Cwd: x[11], Focused: x[12] == "1"}
		var okp, okw bool
		p.ID, okp = ParsePane(x[0])
		p.Tab, okw = ParseWindow(x[1])
		n := []*int{&p.TabIndex, &p.W, &p.H, &p.X, &p.Y}
		s := []string{x[3], x[6], x[7], x[8], x[9]}
		ok := okp && okw
		for j := range n {
			v, err := strconv.Atoi(s[j])
			if err != nil || !digits(s[j]) {
				ok = false
				break
			}
			*n[j] = v
		}
		if ok {
			ps = append(ps, p)
		}
	}
	return ps
}

// digits is whether s is one or more ASCII digits.
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
