package tmux

import "testing"

// Panes and windows as tmux, zellij and weaver's own files name them.
func TestIDs(t *testing.T) {
	for _, c := range []struct {
		in   string
		want PaneID
		ok   bool
	}{{"%3", "3", true}, {"terminal_3", "3", true}, {" 12\n", "12", true}, {"", "", false}, {"@3", "@3", false}} {
		if p, ok := ParsePane(c.in); p != c.want || ok != c.ok {
			t.Errorf("ParsePane(%q) = %q %v, want %q %v", c.in, p, ok, c.want, c.ok)
		}
	}
	if w, ok := ParseWindow("@12\n"); w != "12" || !ok || w.Target() != "@12" {
		t.Errorf("ParseWindow: %q %v", w, ok)
	}
}

func TestParsePanes(t *testing.T) {
	out := "%3\x1f@1\x1fhome\x1f0\x1f0\x1fwvr home\x1f80\x1f24\x1f0\x1f0\x1ftitle\x1f/tmp\x1f1\n" +
		"%x\x1f@1\x1fhome\x1f0\x1f0\x1fsh\x1f80\x1f24\x1f0\x1f0\x1ft\x1f/\x1f0\n"
	ps := ParsePanes([]byte(out))
	if len(ps) != 1 || ps[0].ID != "3" || ps[0].Tab != "1" || ps[0].W != 80 || !ps[0].Focused || ps[0].Cwd != "/tmp" {
		t.Errorf("%+v", ps)
	}
}
