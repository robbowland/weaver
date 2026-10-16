package markdown

import (
	"regexp"
	"strings"
	"testing"
)

var plain = Style{}

var reSGRTest = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestRender(t *testing.T) {
	src := "## Summary\n\nFixes the **flaky** test in `ci.yml`, see [the run](https://example.com/run).\n\n" +
		"- [x] wrote it\n- [ ] tested it\n\n1. one\n2. two\n\n> quoted\n\n```go\nfunc f() {}\n```\n\n" +
		"<!-- a template's note -->\n<details><summary>Logs</summary>\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n---\n\n![shot](https://x/y.png)\n"
	got := Render(src, 40, plain)
	want := strings.Join([]string{
		"SUMMARY",
		"",
		"Fixes the flaky test in ci.yml, see the",
		"run.",
		"",
		"✓ wrote it",
		"○ tested it",
		"",
		"1. one",
		"2. two",
		"",
		"│ quoted",
		"",
		"  func f() {}",
		"",
		"Logs",
		"",
		"a │ b",
		"──┼──",
		"1 │ 2",
		"",
		"──────",
		"",
		"[shot]",
	}, "\n")
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// Every line fits, styles and all, and a word longer than the line is cut
// across lines.
func TestRenderWraps(t *testing.T) {
	st := Style{Text: "\x1b[38;2;153;153;153m", Strong: "\x1b[1m", Faint: "\x1b[2m", Good: "\x1b[32m", Off: "\x1b[0m"}
	got := Render("a **bold** word and supercalifragilisticexpialidocious, then more words to wrap", 12, st)
	for _, l := range strings.Split(got, "\n") {
		if n := len([]rune(reSGRTest.ReplaceAllString(l, ""))); n > 12 {
			t.Errorf("%q is %d wide", l, n)
		}
	}
	if !strings.Contains(got, "\x1b[1mbold\x1b[0m") {
		t.Errorf("bold lost: %q", got)
	}
}
