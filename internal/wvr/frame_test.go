package wvr

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// What is written shows inside the frame: every row as wide as it, the
// border round it as fzf draws one (the label from its third column), a
// long line cut onto the next, the last lines when they do not all fit, \r
// starting a line again, and of the escapes only colours, also when a read
// cuts one in two.
func TestFrameScreen(t *testing.T) {
	f := &frame{w: 30, h: 6, label: " [ WEAVER ] "}
	f.take("one\r\n\x1b[2Ktwo\n50%\r100%\n\x1b[3")
	f.take("2mred\x1b[0m " + strings.Repeat("x", 30))
	rows, n := f.screen()
	want := []string{
		"┌─ [ WEAVER ] ───────────────┐",
		"│ two                        │",
		"│ 100%                       │",
		"│ red xxxxxxxxxxxxxxxxxxxxxx │",
		"│ xxxxxxxx                   │",
		"└────────────────────────────┘",
	}
	for i, r := range rows {
		if got := ansi.Strip(r); i >= len(want) || got != want[i] {
			t.Errorf("row %d: %q", i, got)
		}
	}
	if len(rows) != len(want) || n != 4 {
		t.Errorf("%d rows, the last line on body row %d", len(rows), n)
	}
	if !strings.Contains(rows[3], "\x1b[32mred") {
		t.Errorf("the colour cut by a read: %q", rows[3])
	}
}

// An OSC cut in two by a read (a link from Worktrunk or git, weaver's own
// frame mark) is held till it ends, never shown as text: as a CSI is.
func TestFrameHoldsACutOSC(t *testing.T) {
	f := &frame{w: 40, h: 6, label: " x ", marks: make(chan struct{}, 1)}
	f.take("see \x1b]8;;https://exa")
	f.take("mple.com\x07here\x1b]8;;\x07 done\n")
	f.take(frameMark[:5])
	f.take(frameMark[5:] + "after\n")
	rows, _ := f.screen()
	got := ansi.Strip(strings.Join(rows, "\n"))
	if strings.Contains(got, "8;;") || strings.Contains(got, "weaver-frame") || !strings.Contains(got, "see here done") || !strings.Contains(got, "after") {
		t.Errorf("shows:\n%s", got)
	}
	select {
	case <-f.marks:
	default:
		t.Error("the frame mark cut in two was not seen")
	}
}
