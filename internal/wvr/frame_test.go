package wvr

import (
	"strings"
	"testing"
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
		if got := reAnsi.ReplaceAllString(r, ""); i >= len(want) || got != want[i] {
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
