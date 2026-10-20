package wvr

import (
	"reflect"
	"testing"
)

// In Alt y's popover, tab and shift-tab step between the board and the
// views; what would open a dialog or go elsewhere is left for once the
// popover has closed, as the wvr commands the board would have run.
func TestPeekChoice(t *testing.T) {
	agent := "a7\t7\t3\tx"
	lost := "lsid-1\tsid-1\t\tx"
	for _, c := range []struct {
		key, sel string
		step     string
		cmds     [][]string
	}{
		{"next", agent, "next", nil},
		{"prev", "", "prev", nil},
		{"jump", agent, "", [][]string{{"_goto", "7", "3"}}},
		{"resume", lost, "", [][]string{{"resume", "sid-1"}}},
		{"jump", lost, "", [][]string{{"resume", "sid-1"}}},
		{"ctrl-e", agent, "", [][]string{{"_goto", "7", "3"}, {"edit", "7"}}},
		{"new", agent, "", [][]string{{"_popup", "new", "--from", "7"}}},
		{"new", "", "", [][]string{{"_popup", "new"}}},
		{"close", agent, "", [][]string{{"_popup", "close", "7"}}},
		{"close", lost, "", [][]string{{"_popup", "forget", "sid-1"}}},
		{"adopt", agent, "", [][]string{{"_popup", "adopt", "7"}}},
		{"adopt", lost, "", [][]string{{"_popup", "adopt"}}},
		{"alt-1", "", "", [][]string{{"next"}}},
		{"rename", agent, "", [][]string{{"_popup", "rename", "7"}}},
		{"rename", lost, "", nil},
		{"restart", agent, "", [][]string{{"restart", "7"}}},
		{"restart", lost, "", nil},
		{"ctrl-e", lost, "", nil},
	} {
		step, cmds := peekChoice(c.key, c.sel)
		if step != c.step || !reflect.DeepEqual(cmds, c.cmds) {
			t.Errorf("%s on %q: %q %q, want %q %q", c.key, c.sel, step, cmds, c.step, c.cmds)
		}
	}

	item, heading := "i\to/r#45\t1\t2\tAdd a flag", "h\t\t\t\tQUEUE"
	for _, c := range []struct {
		key, sel string
		step     string
		cmds     [][]string
	}{
		{"next", item, "next", nil},
		{"from", item, "", [][]string{{"_items", "github", "from", "o/r#45"}}},
		{"review", item, "", [][]string{{"_items", "github", "from", "o/r#45", "--review"}}},
		{"from", heading, "", nil},
		{"alt-1", item, "", [][]string{{"next"}}},
	} {
		step, cmds := peekItemChoice("github", c.key, c.sel)
		if step != c.step || !reflect.DeepEqual(cmds, c.cmds) {
			t.Errorf("view: %s on %q: %q %q, want %q %q", c.key, c.sel, step, cmds, c.step, c.cmds)
		}
	}
}
