package wvr

import (
	"regexp"
	"strings"
	"testing"
)

var reSGR = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// A click anywhere on a hint, its key or what it does, is that hint;
// between hints, none.
func TestHintAt(t *testing.T) {
	hs := []choice{{"⏎", "JUMP"}, {"1-9", ""}, {"n", "NEW"}, {"^q", "QUIT"}}
	line := reSGR.ReplaceAllString(hintLine(hs, 2), "")
	if want := "⏎ JUMP  1-9  n NEW  ^q QUIT"; line != want {
		t.Fatalf("hintLine = %q, want %q", line, want)
	}
	for _, c := range []struct {
		col  int
		want string
	}{{1, "⏎"}, {6, "⏎"}, {7, ""}, {9, "1-9"}, {14, "n"}, {18, "n"}, {19, ""}, {21, "^q"}, {27, "^q"}, {28, ""}} {
		if h, _ := hintAt(hs, 2, c.col); h.key != c.want {
			t.Errorf("col %d: %q, want %q", c.col, h.key, c.want)
		}
	}
}

func TestHintAct(t *testing.T) {
	for key, want := range map[string]string{
		"⏎": "trigger(enter)", "esc": "trigger(esc)", "?": "trigger(?)", "n": "trigger(n)",
		"^r": "trigger(ctrl-r)", "^e": "trigger(ctrl-e)", "Alt R": "trigger(alt-R)", "Alt B": "trigger(alt-B)",
		"^q": "execute-silent(tmux send-keys -K C-q)", "1-9": "", "↑↓": "",
	} {
		if got := hintAct(key); got != want {
			t.Errorf("hintAct(%q) = %q, want %q", key, got, want)
		}
	}
}

// A board row's menu has its keys while browsing; searching, only those
// that do not type.
func TestBoardMenu(t *testing.T) {
	whats := func(ms []menuItem) []string {
		var out []string
		for _, m := range ms {
			out = append(out, m.what+":"+m.cmd)
		}
		return out
	}
	t.Setenv("FZF_INPUT_STATE", "hidden")
	got := whats(boardMenu("a1"))
	want := []string{"jump to it:Enter", "nvim:C-e", "close:x", ":", "new thread:n", "help:?"}
	if len(got) != len(want) {
		t.Fatalf("thread row: %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("thread row: %q, want %q", got, want)
		}
	}
	t.Setenv("FZF_INPUT_STATE", "enabled")
	if got := whats(boardMenu("l2")); len(got) != 1 || got[0] != "resume:Enter" {
		t.Fatalf("lost row, searching: %q", got)
	}
}

// Each of the welcome's key lines is the button for its key; no other
// line does anything.
func TestWelcomeClicks(t *testing.T) {
	text, acts := welcome(200, 56, true)
	lines := strings.Split(text, "\n")
	if len(lines) != len(acts) {
		t.Fatalf("%d lines, %d actions", len(lines), len(acts))
	}
	got := map[string]string{}
	for i, l := range lines {
		if acts[i] != "" {
			key := strings.TrimSpace(reSGR.ReplaceAllString(l, ""))[:5]
			got[strings.TrimSpace(key)] = acts[i]
		}
	}
	want := map[string]string{"n": "trigger(n)", "Alt N": "execute-silent(tmux send-keys -K M-N)", "Alt y": "execute-silent(tmux send-keys -K M-y)"}
	if len(got) != len(want) {
		t.Fatalf("buttons %q, want %q", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%q: %q, want %q", k, got[k], v)
		}
	}
}

// A dialog takes a share of the terminal within its bounds, and never
// runs past its edges.
func TestPopupSize(t *testing.T) {
	for _, c := range []struct{ cw, ch, w, h int }{
		{200, 56, 124, 22},  // a share of the width, its own height
		{100, 30, 96, 22},   // at least the least it needs
		{400, 120, 160, 22}, // at most what is worth reading
		{90, 20, 86, 18},    // inside a small terminal
	} {
		if w, h := formSize.of(c.cw, c.ch); w != c.w || h != c.h {
			t.Errorf("form on %dx%d: %dx%d, want %dx%d", c.cw, c.ch, w, h, c.w, c.h)
		}
	}
}
