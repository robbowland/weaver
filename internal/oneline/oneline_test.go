package oneline

import "testing"

func TestOneLine(t *testing.T) {
	if got := Squash("  a\n\tb  \x01c ", 4); got != "a b " {
		t.Errorf("Squash: %q", got)
	}
	if got := Line("a\n\nb\x7fc"); got != "a b c" {
		t.Errorf("Line: %q", got)
	}
	if got := Ctl("a\nb\x1ec\x1fd"); got != "a b\x1ec\x1fd" {
		t.Errorf("Ctl: %q", got)
	}
	if got := Cut("héllo", 2); got != "hé" {
		t.Errorf("Cut: %q", got)
	}
}
