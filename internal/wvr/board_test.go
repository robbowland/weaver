package wvr

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/git"
	"github.com/robbowland/weaver/internal/oneline"
	"github.com/robbowland/weaver/internal/session"
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
	if !files.Exists(d + "/x.tight") {
		t.Error("a hidden card leaves .tight")
	}
	if pw, _ := files.FirstLine(d + "/x.room"); pw != "down,50%,border-top,wrap,hidden" {
		t.Errorf(".room = %q", pw)
	}
	cardRoom(12, 72, 30)
	if files.Exists(d + "/x.tight") {
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
		{"漢字漢字", 5, "漢字…"},
		{"👍 ok", 4, "👍 …"},
		{"\x1b[31mabcdef\x1b[0m", 4, "\x1b[31mabc…\x1b[0m"},
		{"é⏎x", 3, "é⏎x"},
		{"anything", 0, ""},
	} {
		if got := oneline.Fit(c.s, c.n); got != c.want {
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
	if w := git.WhereOf(main); w.Repo != "repo" || w.Branch != "main" {
		t.Errorf("main checkout: %+v", w)
	}
	if w := git.WhereOf(wt); w.Repo != "repo" || w.Branch != "0123456" {
		t.Errorf("worktree, detached: %+v", w)
	}
	if w := git.WhereOf(filepath.Join(d, "plain")); w.Repo != "plain" || w.Branch != "" || w.GitDir != "" {
		t.Errorf("no repository: %+v", w)
	}
}

// The board of every thread leaves helpers out and shows each thread as
// it shows (session.Row.Shown); a thread's own board lists the agents in
// its session, helpers by their windows' labels.
func TestOnBoard(t *testing.T) {
	helper := session.Row{Pane: "3", Session: "session:t", Thread: 1, WindowName: "tests", State: agent.StateNeedsYou, Since: 7}
	thread := session.Row{Pane: "1", Session: "session:t", Tnum: 1, Thread: 1, State: agent.StateDone, Via: &helper}
	other := session.Row{Pane: "2", Session: "session:u", Tnum: 2, Thread: 2, State: agent.StateWorking}
	home := session.Row{Pane: "4", Session: "session:home", State: agent.StateNeedsYou}
	all := []session.Row{thread, other, helper, home}

	got := onBoard(all, "")
	if len(got) != 3 || got[0].Pane != "1" || got[1].Pane != "2" || got[2].Pane != "4" || got[0].State != agent.StateNeedsYou {
		t.Fatalf("every thread's board: %+v, want threads 1 (needing you) and 2 and the agent in home", got)
	}
	got = onBoard(all, "session:t")
	if len(got) != 2 || got[0].Pane != "1" || got[1].Pane != "3" || got[0].State != agent.StateDone || got[1].Called() != "tests" {
		t.Errorf("thread 1's own board: %+v, want its agent as it is and its helper by name", got)
	}
}

// Every card says a state by one rule (stateWord), as the board groups it:
// done only while not looked at, not reporting and exited whatever the
// state says.
func TestStateWord(t *testing.T) {
	now := int64(1000)
	for _, c := range []struct {
		r    session.Row
		want string
	}{
		{session.Row{State: agent.StateDone}, CGreen + "done" + COff},
		{session.Row{State: agent.StateDone, Seen: true}, "done"},
		{session.Row{State: agent.StateNeedsYou}, CRed + "needs you" + COff},
		{session.Row{State: agent.StateNeedsYou, Exited: true}, "exited"},
		{session.Row{State: agent.StateStarting, Since: 1}, "not reporting"},
		{session.Row{State: agent.StateWorking}, "working"},
	} {
		if got := stateWord(c.r, now); got != c.want {
			t.Errorf("stateWord(%+v) = %q, want %q", c.r, got, c.want)
		}
	}
}
