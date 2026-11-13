// Package oneline makes text fit on one line, as weaver keeps it in its
// records and shows it: control characters as spaces, runs of white space
// as one, cut to a length.
package oneline

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// Squash is s with runs of white space and control characters as one
// space, trimmed, cut to n characters.
func Squash(s string, n int) string {
	var b strings.Builder
	sp := false
	for _, r := range s {
		if unicode.IsSpace(r) || r < 0x20 || r == 0x7f {
			if !sp {
				b.WriteByte(' ')
			}
			sp = true
			continue
		}
		sp = false
		b.WriteRune(r)
	}
	t := strings.TrimPrefix(b.String(), " ")
	t = strings.TrimSuffix(t, " ")
	return Cut(t, n)
}

// Cut is s cut to n characters.
func Cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

// Line is s with runs of control characters (C0, DEL and C1, which some
// terminals take as ESC's sequences) as one space.
func Line(s string) string {
	var b strings.Builder
	c := false
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r >= 0x80 && r < 0xa0 {
			if !c {
				b.WriteByte(' ')
			}
			c = true
			continue
		}
		c = false
		b.WriteRune(r)
	}
	return b.String()
}

// Ctl is s with each control character and DEL as a space, but RS and
// US, which separate the fields of weaver's own lists.
func Ctl(s string) string {
	if strings.IndexFunc(s, func(r rune) bool { return r < 0x1e || r == 0x7f }) < 0 {
		return s
	}
	return strings.Map(func(r rune) rune {
		if r < 0x1e || r == 0x7f {
			return ' '
		}
		return r
	}, s)
}

// Fit is s cut to n columns (… at the cut) and padded to n, its colours
// kept.
func Fit(s string, n int) string {
	s = ansi.Truncate(s, n, "…")
	return s + strings.Repeat(" ", max(n-ansi.StringWidth(s), 0))
}
