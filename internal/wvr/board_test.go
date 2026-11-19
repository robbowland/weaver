package wvr

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
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

// A refresh whose rows changed asks for a reload that takes the rows it
// made, by its token, rather than looking at the fleet again; a reload
// asked for otherwise, or by another token, makes its own.
func TestRefreshKeepsItsRows(t *testing.T) {
	d := t.TempDir()
	t.Setenv("FZF_SOCK", d+"/b.sock")
	t.Setenv("FZF_INPUT_LABEL", "")
	t.Setenv("WEAVER_VIEWS", "")
	e := &Env{&session.Env{StateDir: d + "/state", SKey: "rex-w", Self: "/bin/wvr"}}
	act := e.Refresh()
	tok, ok := strings.CutPrefix(strings.TrimSpace(act), "reload-sync(COLOR=1 /bin/wvr _rows ")
	tok, ok2 := strings.CutSuffix(tok, ")")
	if !ok || !ok2 || tok == "" || strings.ContainsAny(tok, " '\"$;") {
		t.Fatalf("refresh: %q, want a reload of _rows with a token", act)
	}
	// The rows it kept (stood in for, to tell them from rows made anew).
	if err := os.WriteFile(d+"/b.next", []byte(tok+"\nKEPT\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := e.Rows("other"); got == "KEPT\n" {
		t.Error("another token took the kept rows")
	}
	if got := e.Rows(tok); got != "KEPT\n" {
		t.Errorf("the refresh's reload: %q, want the rows it kept", got)
	}
	if got, _ := os.ReadFile(d + "/b.rows"); string(got) != "KEPT\n" {
		t.Errorf(".rows %q: the rows loaded are kept for the next refresh", got)
	}
	if files.Exists(d + "/b.next") {
		t.Error("kept rows not taken")
	}
	if got := e.Rows(tok); got == "KEPT\n" {
		t.Error("kept rows taken twice")
	}
	if act := e.Refresh(); strings.Contains(act, "reload") {
		t.Errorf("refresh with rows unchanged: %q", act)
	}
}

// checkout is a work tree of repository repo on branch, under d: the
// repository's own checkout on main, else a worktree of it, as git leaves
// them (WhereOf reads them without git).
func checkout(t *testing.T, d, repo, branch string) string {
	t.Helper()
	main := filepath.Join(d, repo)
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(main+"/.git", 0o755))
	must(os.WriteFile(main+"/.git/HEAD", []byte("ref: refs/heads/main\n"), 0o644))
	if branch == "main" {
		return main
	}
	wt, gd := main+"-"+branch, main+"/.git/worktrees/"+branch
	must(os.MkdirAll(gd, 0o755))
	must(os.MkdirAll(wt, 0o755))
	must(os.WriteFile(wt+"/.git", []byte("gitdir: "+gd+"\n"), 0o644))
	must(os.WriteFile(gd+"/commondir", []byte("../..\n"), 0o644))
	must(os.WriteFile(gd+"/HEAD", []byte("ref: refs/heads/"+branch+"\n"), 0o644))
	return wt
}

// boardScene is a board of threads in four repositories: two that need
// you (one a review) and one done and not yet looked at, each in another
// repository; weaver's working, idle, and idle for days (thread 03 among
// them, whose key then unfolds it); morrowind-modding's working and idle;
// a review at rest; a held thread.
func boardScene(t *testing.T) (*Env, []session.Row, []session.LostRow, int64) {
	t.Helper()
	d := t.TempDir()
	now := int64(2_000_000_000)
	at := func(repo, branch string) string { return checkout(t, d, repo, branch) }
	agents := []session.Row{
		{Pane: "11", Tnum: 1, Title: "Add a CLI flag", State: agent.StateNeedsYou, Since: now - 660, Waiting: "Bash: python app.py", Root: at("app", "flag"), Turn: 3},
		{Pane: "14", Tnum: 4, Title: "Tidy fish config", State: agent.StateDone, Since: now - 480, Last: "Tidied.", Root: at("dotfiles", "main"), Turn: 1},
		{Pane: "15", Tnum: 5, Title: "Background start", State: agent.StateWorking, Since: now - 120, Doing: "tests", Root: at("weaver", "main"), Turn: 2},
		{Pane: "17", Tnum: 7, Title: "Board sections", State: agent.StateIdle, Since: now - 2400, Root: at("weaver", "board"), Turn: 1},
		{Pane: "13", Tnum: 3, Title: "Older", State: agent.StateDone, Seen: true, Since: now - 2*86400, Last: "Done long ago.", Root: at("weaver", "older"), Turn: 1},
		{Pane: "18", Tnum: 8, Title: "Oldest", State: agent.StateIdle, Since: now - 3*86400, Root: at("weaver", "oldest"), Turn: 1},
		{Pane: "12", Tnum: 2, Title: "Wildlife spawns", State: agent.StateWorking, Since: now - 540, Nsub: 2, Root: at("morrowind-modding", "main"), Turn: 4},
		{Pane: "16", Tnum: 6, Title: "Combat AI tuning", State: agent.StateIdle, Since: now - 3*3600, Root: at("morrowind-modding", "ai"), Turn: 1},
		{Pane: "19", Tnum: 9, Title: "Review the docs", State: agent.StateNeedsYou, Since: now - 60, Waiting: "a question", Root: at("app", "docs"), Review: true, Turn: 1},
		{Pane: "20", Tnum: 10, Title: "Review the API", State: agent.StateIdle, Since: now - 600, Root: at("app", "api"), Review: true, Turn: 1},
	}
	lost := []session.LostRow{{Sid: "sid-held", Title: "Held one", Dir: at("weaver", "held"), Held: strconv.FormatInt(now-7200, 10), Note: "next: the docs"}}
	sd := t.TempDir()
	e := &Env{&session.Env{StateDir: sd + "/state", SKey: "rex-w", Self: "/bin/wvr", Session: "w"}}
	return e, agents, lost, now
}

// spaced is a board's text with each run of spaces as one " | ": its
// columns, whatever their widths.
func spaced(s string) string {
	return regexp.MustCompile(`  +`).ReplaceAllString(strings.TrimSpace(s), " | ")
}

// The board as text lists what needs you first, from every repository
// (needs you, then done and not yet looked at, each with its repository
// and branch); then a section per repository, the busiest first, under its
// name, its rows by branch alone, working first and every idle one
// (folding is the board's); then reviews at rest, and what is held.
func TestBoardByProjectText(t *testing.T) {
	t.Setenv("COLUMNS", "120")
	t.Setenv("COLOR", "")
	e, agents, lost, now := boardScene(t)
	got := e.board(true, agents, lost, now)
	_, body, _ := strings.Cut(got, "\n\n")
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		lines = append(lines, spaced(l))
	}
	want := `NEEDS YOU · 3
[!] | Add a CLI flag | Bash: python app.py | app/flag | 11m
[!] | Review the docs | a question | app/docs | 1m
[x] | Tidy fish config | Tidied. | dotfiles/main | 8m

WEAVER · 4
[/] | Background start | running tests | main | 2m
[ ] | Board sections | board | 40m
[x] | Older | Done long ago. | older | 2d
[ ] | Oldest | oldest | 3d

MORROWIND-MODDING · 2
[/] | Wildlife spawns | 2 subagents | main | 9m
[ ] | Combat AI tuning | ai | 3h

REVIEWS · 1
[ ] | Review the API | app/api | 10m

HELD · 1
[=] | Held one | next: the docs | weaver/held | 2h`
	if g := strings.Join(lines, "\n"); g != want {
		t.Errorf("the board as text:\n%s\nwant:\n%s", g, want)
	}
}

// boardItems is the board's items for fzf (FZF_SOCK in d): each item's id,
// and the text of its fold rows.
func boardItems(t *testing.T, e *Env, agents []session.Row, lost []session.LostRow, now int64) (ids []string, folds []string) {
	t.Helper()
	rows := e.board(false, agents, lost, now)
	keepRows(sockBase(), rows)
	for _, l := range strings.Split(strings.TrimRight(rows, "\n"), "\n") {
		f := strings.Split(l, "\t")
		ids = append(ids, f[0])
		if strings.HasPrefix(f[0], "m") {
			folds = append(folds, spaced(ansi.Strip(strings.Join(f[6:], ""))))
		}
	}
	return ids, folds
}

// On the board, a project's threads idle for more than a day fold into one
// row, whose enter (wvr _fold) shows them and folds them again; HELD's
// fold is that one. A 1-9 key whose thread is folded away unfolds it, and
// the reload that shows it puts the cursor there and jumps (Chrome).
func TestBoardByProjectFolds(t *testing.T) {
	d := t.TempDir()
	t.Setenv("FZF_SOCK", d+"/b.sock")
	t.Setenv("FZF_COLUMNS", "116")
	t.Setenv("FZF_LINES", "60")
	t.Setenv("COLOR", "1")
	t.Setenv("LS_JUMP", "")
	t.Setenv("LS_HOME", "")
	e, agents, lost, now := boardScene(t)
	ids, folds := boardItems(t, e, agents, lost, now)
	want := "hneeds a11 a19 a14 gpweaver hpweaver a15 a17 mpweaver gpmorrowind-modding hpmorrowind-modding a12 a16 " +
		"greviews hreviews a20 gheld hheld lsid-held gz"
	if g := strings.Join(ids, " "); g != want {
		t.Errorf("items:\n%s\nwant\n%s", g, want)
	}
	if len(folds) != 1 || folds[0] != "+ 2 older · ⏎ shows them" {
		t.Errorf("fold rows: %q, want weaver's two idle for days", folds)
	}
	keys, _ := os.ReadFile(d + "/b.keys")
	kl := strings.Split(string(keys), "\n")
	if len(kl) < 9 || kl[4] != "pos(7)+print(jump)+accept" || kl[2] != "execute-silent(/bin/wvr _fold mpweaver 13 jump)+reload-sync(/bin/wvr _rows)" {
		t.Errorf("keys:\n%s\nwant 5 on its row, 3 (folded) unfolding it", keys)
	}

	Fold([]string{"mpweaver"})
	ids, folds = boardItems(t, e, agents, lost, now)
	if g := strings.Join(ids[5:11], " "); g != "hpweaver a15 a17 a13 a18 mpweaver" || len(folds) != 1 || folds[0] != "- 2 older · ⏎ folds them again" {
		t.Errorf("unfolded: %s %q", g, folds)
	}
	Fold([]string{"mpweaver"})
	if ids, _ = boardItems(t, e, agents, lost, now); strings.Join(ids[5:9], " ") != "hpweaver a15 a17 mpweaver" {
		t.Errorf("folded again: %v", ids)
	}

	// The cursor put on a thread folded away (going home from it): shown,
	// then the cursor on it.
	if got := e.Pos("13"); got != "execute-silent(/bin/wvr _fold mpweaver 13)+reload-sync(/bin/wvr _rows)\n" {
		t.Errorf("_pos of a thread folded away: %q", got)
	}
	Fold([]string{"mpweaver", "13"})
	ids, _ = boardItems(t, e, agents, lost, now)
	at := slices.Index(ids, "a13") + 1
	if c := e.Chrome(); at < 1 || !strings.HasPrefix(c, "pos("+strconv.Itoa(at)+")+change-header") {
		t.Errorf("chrome after _pos on a folded thread: %q, want its row (%d) alone", c, at)
	}
	Fold([]string{"mpweaver"})
	boardItems(t, e, agents, lost, now)

	// Thread 03's key: unfolded, and on the load that shows it, the cursor
	// on its row and the jump.
	Fold([]string{"mpweaver", "13", "jump"})
	ids, _ = boardItems(t, e, agents, lost, now)
	at = slices.Index(ids, "a13") + 1
	if c := e.Chrome(); at < 1 || !strings.HasPrefix(c, "pos("+strconv.Itoa(at)+")+print(jump)+accept+") {
		t.Errorf("chrome after 3 on a folded thread: %q, want its row (%d) and the jump", c, at)
	}
	if c := e.Chrome(); strings.HasPrefix(c, "pos(") {
		t.Errorf("chrome again: %q, the jump made once", c)
	}

	// Short of lines, no gaps; the fold row is still there.
	t.Setenv("FZF_LINES", "18")
	if ids, _ = boardItems(t, e, agents, lost, now); slices.Contains(ids, "gpweaver") || !slices.Contains(ids, "mpweaver") {
		t.Errorf("short of lines: %v", ids)
	}
}

// A single repository still has its heading, and a thread's own board too.
func TestBoardOneProject(t *testing.T) {
	t.Setenv("COLUMNS", "120")
	t.Setenv("COLOR", "")
	e, agents, _, now := boardScene(t)
	got := e.board(true, agents[2:4], nil, now)
	if !strings.Contains(got, "\n\nWEAVER · 2\n") || strings.Contains(got, "NEEDS YOU") {
		t.Errorf("one repository:\n%s", got)
	}
}

// One thread idle for days shows as it is: a fold row in its place would
// save no line.
func TestBoardOneOldThreadUnfolded(t *testing.T) {
	d := t.TempDir()
	t.Setenv("FZF_SOCK", d+"/b.sock")
	t.Setenv("FZF_COLUMNS", "116")
	t.Setenv("FZF_LINES", "60")
	t.Setenv("COLOR", "1")
	t.Setenv("LS_JUMP", "")
	t.Setenv("LS_HOME", "")
	e, agents, _, now := boardScene(t)
	// weaver's working and idle threads, and one of its two idle for days.
	ids, folds := boardItems(t, e, []session.Row{agents[2], agents[3], agents[5]}, nil, now)
	if want := "hpweaver a15 a17 a18 gz"; strings.Join(ids, " ") != want || len(folds) != 0 {
		t.Errorf("items %q, folds %q: want %q, no fold", strings.Join(ids, " "), folds, want)
	}
}

// The footer offers tab only where it goes somewhere: not on Alt Y's board
// of one session (Peek's LS_TABS=0), nor with WEAVER_VIEWS=none; on home
// beside a view, and in Alt y's popover with one, it does.
func TestBoardHintsTab(t *testing.T) {
	e := &Env{&session.Env{StateDir: t.TempDir()}}
	t.Setenv("FZF_SOCK", "")
	t.Setenv("FZF_PROMPT", "")
	t.Setenv("FZF_COLUMNS", "200")
	t.Setenv("FZF_LINES", "40")
	t.Setenv("FZF_TOTAL_COUNT", "3")
	t.Setenv("LS_EMPTY", "")
	hasTab := func() bool {
		for _, h := range e.boardHints() {
			if h.key == "tab" {
				return true
			}
		}
		return false
	}
	for _, x := range []struct {
		tabs, views string // "-": unset
		want        bool
	}{
		{"-", "none", false},
		{"-", "github", true},
		{"0", "github", false},
		{"1", "github", true},
	} {
		for _, input := range []string{"hidden", "visible"} {
			t.Setenv("FZF_INPUT_STATE", input)
			t.Setenv("WEAVER_VIEWS", x.views)
			t.Setenv("LS_TABS", x.tabs)
			if x.tabs == "-" {
				os.Unsetenv("LS_TABS")
			}
			if got := hasTab(); got != x.want {
				t.Errorf("LS_TABS %s, WEAVER_VIEWS %s, input %s: tab %v, want %v: %v", x.tabs, x.views, input, got, x.want, e.boardHints())
			}
		}
	}
}
