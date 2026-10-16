package wvr

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCardRoom(t *testing.T) {
	t.Setenv("FZF_SOCK", "")
	for _, c := range []struct {
		items, cols, lines int
		want               string
	}{
		{12, 72, 30, "down,13,border-top,wrap"},
		{12, 72, 22, "down,50%,border-top,wrap,hidden"},
		{12, 160, 20, "right,74,border-left,wrap"},
		{0, 160, 20, "down,50%,border-top,wrap,hidden"},
	} {
		if got := cardRoom(c.items, c.cols, c.lines); got != c.want {
			t.Errorf("cardRoom(%d, %d, %d) = %q, want %q", c.items, c.cols, c.lines, got, c.want)
		}
	}
}

func TestCardRoomTight(t *testing.T) {
	d := t.TempDir()
	t.Setenv("FZF_SOCK", d+"/x.sock")
	cardRoom(12, 72, 22)
	if !exists(d + "/x.tight") {
		t.Error("a hidden card leaves .tight")
	}
	if pw, _ := firstLine(d + "/x.room"); pw != "down,50%,border-top,wrap,hidden" {
		t.Errorf(".room = %q", pw)
	}
	cardRoom(12, 72, 30)
	if exists(d + "/x.tight") {
		t.Error("a card with room removes .tight")
	}
}

func TestFzfAct(t *testing.T) {
	for _, c := range []struct{ text, want string }{
		{"plain", "change-header(plain)"},
		{"a (b) c", "change-header[a (b) c]"},
		{"x) y] z}", "change-header<x) y] z}>"},
		{"two\nlines", "change-header(two\nlines)"},
	} {
		if got, ok := fzfAct("change-header", c.text); !ok || got != c.want {
			t.Errorf("fzfAct(%q) = %q, %v; want %q", c.text, got, ok, c.want)
		}
	}
	if _, ok := fzfAct("x", ")]}>~!@#%^*;/|"); ok {
		t.Error("no delimiter fits: not ok")
	}
}

func TestFit(t *testing.T) {
	for _, c := range []struct {
		s    string
		n    int
		want string
	}{
		{"abc", 5, "abc  "},
		{"abcdef", 4, "abc…"},
		{"é⏎x", 3, "é⏎x"},
		{"anything", 0, ""},
	} {
		if got := fit(c.s, c.n); got != c.want {
			t.Errorf("fit(%q, %d) = %q, want %q", c.s, c.n, got, c.want)
		}
	}
}

func TestAge(t *testing.T) {
	for s, want := range map[int64]string{0: "0s", 75: "1m", 7200: "2h", 172800: "2d"} {
		if got := age(s); got != want {
			t.Errorf("age(%d) = %q, want %q", s, got, want)
		}
	}
}

func TestWhereOf(t *testing.T) {
	d := t.TempDir()
	main := filepath.Join(d, "repo")
	wt := filepath.Join(d, "repo-wt")
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(main+"/.git/worktrees/repo-wt", 0o755))
	must(os.WriteFile(main+"/.git/HEAD", []byte("ref: refs/heads/main\n"), 0o644))
	must(os.MkdirAll(wt, 0o755))
	must(os.WriteFile(wt+"/.git", []byte("gitdir: "+main+"/.git/worktrees/repo-wt\n"), 0o644))
	must(os.WriteFile(main+"/.git/worktrees/repo-wt/commondir", []byte("../..\n"), 0o644))
	must(os.WriteFile(main+"/.git/worktrees/repo-wt/HEAD", []byte("0123456789abcdef\n"), 0o644))
	if w := whereOf(main); w.repo != "repo" || w.branch != "main" {
		t.Errorf("main checkout: %+v", w)
	}
	if w := whereOf(wt); w.repo != "repo" || w.branch != "0123456" {
		t.Errorf("worktree, detached: %+v", w)
	}
	if w := whereOf(filepath.Join(d, "plain")); w.repo != "plain" || w.branch != "" || w.gitdir != "" {
		t.Errorf("no repository: %+v", w)
	}
}
