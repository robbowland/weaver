package markdown

import (
	"regexp"
	"strings"
	"testing"
)

var plain = Style{}

var reSGRTest = regexp.MustCompile(`\x1b\[[0-9;]*m|\x1b\]8;;[^\x1b]*\x1b\\`)

func TestRender(t *testing.T) {
	src := "## Summary\n\nFixes the **flaky** test in `ci.yml`, see [the run](https://example.com/run).\n\n" +
		"- [x] wrote it\n- [ ] tested it\n\n1. one\n2. two\n\n> quoted\n\n```go\nfunc f() {}\n```\n\n" +
		"<!-- a template's note -->\n<details><summary>Logs</summary>\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n---\n\n![shot](https://x/y.png)\n" +
		"\n> [!WARNING]\n> Breaks the API.\n\n## Testing\n\n### Unit\n\n#### notes\n\nsee https://github.com/o/r/pull/45\n"
	// The styles aside (plain has no Off to end them), the shape.
	got := reSGRTest.ReplaceAllString(Render(src, 40, plain), "")
	want := strings.Join([]string{
		strings.Repeat("─", 40),
		"01  SUMMARY",
		"",
		"Fixes the flaky test in `ci.yml`, see",
		"[ SRC: the run ].",
		"",
		"[x] wrote it",
		"[ ] tested it",
		"",
		"1. one",
		"2. two",
		"",
		"│ quoted",
		"",
		"┌" + strings.Repeat(" ", 37) + "GO",
		"  func f() {}",
		strings.Repeat(" ", 39) + "┘",
		"",
		"Logs",
		"",
		"A   B",
		"─────",
		"1   2",
		"",
		strings.Repeat("─", 40),
		"",
		"[ IMG: shot ]",
		"",
		strings.Repeat("━", 40),
		"[ WARNING ]",
		"Breaks the API.",
		"",
		strings.Repeat("─", 40),
		"02  TESTING",
		"",
		"Unit",
		"",
		"NOTES",
		"",
		"see [ SRC: github.com/o/r/pull/45 ]",
	}, "\n")
	// Links' brackets hold to their words with no-break spaces.
	got = strings.ReplaceAll(got, "\u00a0", " ")
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

// A link's every word (and its brackets) opens it on a click, however the
// lines break.
func TestRenderLinks(t *testing.T) {
	got := Render("see [the long run](https://example.com/run) and https://x.io/a", 12, Style{Off: "\x1b[0m"})
	open := "\x1b]8;;https://example.com/run\x1b\\"
	for _, w := range []string{"the", "long", "run"} {
		if !strings.Contains(got, open+w) {
			t.Errorf("%q is not a link: %q", w, got)
		}
	}
	if !strings.Contains(got, "\x1b]8;;https://x.io/a\x1b\\") {
		t.Errorf("a bare URL is not a link: %q", got)
	}
	if strings.Count(got, "\x1b]8;;\x1b\\") != strings.Count(got, "\x1b]8;;h") {
		t.Errorf("a link left open: %q", got)
	}
}

// A description is anyone's: no control sequence in it reaches the
// terminal, from its text or its code (an OSC 52 sets the clipboard, an
// OSC 8 makes a link), and one left open takes nothing after it.
func TestRenderInert(t *testing.T) {
	src := "Hi \x1b]52;c;ZXZpbA==\x07there \x1b[31mred\x1b[0m \x1b]8;;https://evil\x1b\\x\x1b]8;;\x1b\\ \u009b2J\x1bc\n\n" +
		"```\n\x1b[2Jcode\x07\x1b]0;title\n```\n\nafter \x1b]8;;open\n"
	got := Render(src, 60, Style{})
	if strings.ContainsAny(got, "\x1b\x07\u009b") {
		t.Errorf("a control sequence came through: %q", got)
	}
	for _, want := range []string{"Hi there red x 2J", "  code\n", "after"} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %q: %q", want, got)
		}
	}
}
