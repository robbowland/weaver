package wvr

import (
	"os"
	"strconv"
	"strings"
)

// CountsFmt is the status line's counts in tmux's colours: needs you, done,
// working, each only when there are some.
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

// StatusLine is `wvr status --tmux SESSION`: the counts PushStatus last
// wrote for the session, in the status line's colours.
func StatusLine(args []string) string {
	k := ""
	if len(args) > 1 && args[1] != "" {
		k = "tmux-" + args[1]
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

// StatusText is plain `wvr status`: the session's counts as cells,
// `[!] 1  [x] 2  [/] 1`, each only when there are some (done counts the
// ones not yet seen).
func (e *Env) StatusText() string {
	rows, _ := e.AgentRows()
	var n, d, w int
	for _, r := range rows {
		if r.Pane == "" {
			continue
		}
		switch r.State {
		case StateNeedsYou:
			n++
		case StateWorking, StateStarting:
			w++
		case StateDone:
			if r.Seen != "true" {
				d++
			}
		case StateIdle, StateInterrupted, StateEnded:
			// Not counted.
		}
	}
	var out []string
	for _, c := range []struct {
		g string
		k int
	}{{GNeed, n}, {GDone, d}, {GWork, w}} {
		if c.k > 0 {
			out = append(out, c.g+" "+strconv.Itoa(c.k))
		}
	}
	return strings.Join(out, "  ")
}
