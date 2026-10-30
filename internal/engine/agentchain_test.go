package engine

import (
	"testing"

	"github.com/robbowland/weaver/internal/hooks"
)

// unapply gives back what a file held before a call's hunks, as git's diff
// and Claude Code's structuredPatch write them; a file changed again since
// is not guessed at.
func TestUnapply(t *testing.T) {
	h := func(os, ol, ns, nl int, lines ...string) hooks.Hunk {
		return hooks.Hunk{OldStart: os, OldLines: ol, NewStart: ns, NewLines: nl, Lines: lines}
	}
	for _, c := range []struct {
		name, now string
		hunks     []hooks.Hunk
		want      string
		ok        bool
	}{
		{"a line appended", "one\ntwo\nx\n", []hooks.Hunk{h(1, 2, 1, 3, " one", " two", "+x")}, "one\ntwo\n", true},
		{"a line changed in the middle", "a\nB\nc\n", []hooks.Hunk{h(1, 3, 1, 3, " a", "-b", "+B", " c")}, "a\nb\nc\n", true},
		{"two hunks", "1\nTWO\n3\n4\n5\n6\n7\nSEVEN\n", []hooks.Hunk{
			h(1, 3, 1, 3, " 1", "-2", "+TWO", " 3"),
			h(7, 2, 7, 2, " 7", "-8", "+SEVEN"),
		}, "1\n2\n3\n4\n5\n6\n7\n8\n", true},
		{"lines removed, none added (the hunk names the line before)", "a\nd\n", []hooks.Hunk{h(2, 2, 1, 0, "-b", "-c")}, "a\nb\nc\nd\n", true},
		{"the file emptied", "", []hooks.Hunk{h(1, 2, 0, 0, "-a", "-b")}, "a\nb\n", true},
		{"a newline added at the end", "a\nb\n", []hooks.Hunk{h(1, 2, 1, 2, " a", "-b", `\ No newline at end of file`, "+b")}, "a\nb", true},
		{"the newline at the end removed", "a\nb", []hooks.Hunk{h(1, 2, 1, 2, " a", "-b", "+b", `\ No newline at end of file`)}, "a\nb\n", true},
		{"changed again since", "one\nother\n", []hooks.Hunk{h(1, 2, 1, 3, " one", " two", "+x")}, "", false},
		{"a hunk past the end", "one\n", []hooks.Hunk{h(5, 1, 5, 1, " five")}, "", false},
	} {
		got, ok := unapply(c.now, c.hunks)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: got %q, %v; want %q, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}
