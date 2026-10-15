package wv

import (
	"os"
	"strconv"
	"strings"
)

// CountsFmt is the status line's counts in tmux's colours (the script's
// counts_fmt): needs you, done, working, each only when there are some.
func CountsFmt(n, d, w int) string {
	out := ""
	if n > 0 {
		out += "#[fg=#616161][#[fg=#ff3b2f]!#[fg=#616161]]#[fg=#999999] " + strconv.Itoa(n) + "  "
	}
	if d > 0 {
		out += "#[fg=#616161][#[fg=#39d97a]x#[fg=#616161]]#[fg=#999999] " + strconv.Itoa(d) + "  "
	}
	if w > 0 {
		out += "#[fg=#616161][#[fg=#999999]/#[fg=#616161]]#[fg=#999999] " + strconv.Itoa(w) + "  "
	}
	return out
}

// StatusLine is `weaver status --tmux SESSION` (or --zjstatus): the counts
// push_status last wrote for the session, in the status line's colours.
func StatusLine(args []string) string {
	k := os.Getenv("ZELLIJ_SESSION_NAME")
	if args[0] == "--tmux" {
		k = ""
		if len(args) > 1 && args[1] != "" {
			k = "tmux-" + args[1]
		}
	}
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home := os.Getenv("HOME")
		if home == "" {
			home = "/nonexistent"
		}
		state = home + "/.local/state"
	}
	c := [3]int{}
	if k != "" {
		if l, err := firstLine(state + "/weaver/" + strings.ReplaceAll(k, "/", "_") + ".status"); err == nil {
			f := strings.Fields(l)
			ok := len(f) >= 3
			for i := 0; ok && i < 3; i++ {
				ok = digits(f[i])
			}
			for i := 0; ok && i < 3; i++ {
				c[i], _ = strconv.Atoi(f[i])
			}
		}
	}
	return CountsFmt(c[0], c[1], c[2])
}
