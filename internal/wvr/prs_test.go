package wvr

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/github"
	"github.com/robbowland/weaver/internal/jsonx"
	"github.com/robbowland/weaver/internal/page"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
)

// A thread's pull request is the one its item names, else the one whose
// branch (a fork's as pr-N) is the thread's in the same repository.
func TestPRFor(t *testing.T) {
	its := []github.Item{
		{Kind: github.Issues, Repo: "o/r", Number: 7, Head: "fix-tests"},
		{Kind: github.PRs, Repo: "o/r", Number: 45, Head: "fix-tests", FailingChecks: []string{"ci"}},
		{Kind: github.PRs, Repo: "o/other", Number: 46, Head: "docs"},
		{Kind: github.PRs, Repo: "o/r", Number: 47, Head: "main", HeadInFork: true, Review: "APPROVED", PassingChecks: 3},
		{Kind: github.PRs, Repo: "o/r", Number: 48, Head: "x", State: "MERGED"},
	}
	for _, c := range []struct {
		name string
		k    threadKey
		want threadPR
		ok   bool
	}{
		{"by branch", threadKey{repo: "O/R", branch: "fix-tests"}, threadPR{Num: 45, Repo: "o/r", State: prOpen, Checks: "failing"}, true},
		{"a fork's, as pr-N", threadKey{repo: "o/r", branch: "pr-47"}, threadPR{Num: 47, Repo: "o/r", State: prOpen, Checks: "passing", Review: "approved"}, true},
		{"by its item", threadKey{ref: "o/r#48"}, threadPR{Num: 48, Repo: "o/r", State: prMerged}, true},
		{"by its item, in any case", threadKey{ref: "O/R#48", repo: "o/r", branch: "fix-tests"}, threadPR{Num: 48, Repo: "o/r", State: prMerged}, true},
		{"another repository's branch", threadKey{repo: "o/r", branch: "docs"}, threadPR{}, false},
		{"its repository not known", threadKey{branch: "fix-tests"}, threadPR{}, false},
		{"no pull request", threadKey{repo: "o/r", branch: "nothing"}, threadPR{}, false},
	} {
		got, ok := prFor(c.k, its)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: %+v %v, want %+v %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

// A thread is tied to its work item by the branch its worktree is on now,
// which its agent may have made or switched to since the thread was made;
// by the one it was made on when its worktree cannot be read.
func TestThreadKeyBranch(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	repo, wt := ghRepo(t)
	gitRun(t, wt, "switch", "-q", "-c", "fix-tests")
	th := store.Thread{Repo: repo, Branch: "feat", Worktree: wt, Item: &store.ThreadItem{Kind: "issue", Ref: "o/r#7"}}
	if got, want := (slugMemo{}).keyOf(th), (threadKey{ref: "o/r#7", repo: "o/r", branch: "fix-tests"}); got != want {
		t.Errorf("keyOf = %+v, want %+v", got, want)
	}
	th.Worktree = filepath.Join(t.TempDir(), "gone")
	if got := (slugMemo{}).keyOf(th).branch; got != "feat" {
		t.Errorf("its worktree gone: branch %q, want feat", got)
	}
	// The GITHUB view finds it by that branch, as the board does.
	it := ghItem(github.Item{Kind: github.PRs, Repo: "o/r", Number: 45, Head: "fix-tests"})
	th.Worktree = wt
	if v, ok := threadOf([]viewThread{{num: 7, key: (slugMemo{}).keyOf(th)}}, it); !ok || v.num != 7 {
		t.Errorf("the view's thread for its pull request: %v %v", v, ok)
	}
}

// The card's pr line: its number and how it stands, its signal coloured.
func TestPRLine(t *testing.T) {
	for _, c := range []struct {
		pr           threadPR
		line, colour string
	}{
		{threadPR{Num: 45, State: prOpen, Checks: "failing", Review: "requested"}, "#45 · checks failing · review requested", CRed},
		{threadPR{Num: 45, State: prOpen, Checks: "passing"}, "#45 · checks passing", CGrey},
		{threadPR{Num: 45, State: prOpen, Review: "requested"}, "#45 · review requested", CGrey},
		{threadPR{Num: 45, State: prOpen, Review: "approved"}, "#45 · approved", CGreen},
		{threadPR{Num: 45, State: prOpen, Draft: true}, "#45 · draft", CGrey},
		{threadPR{Num: 45, State: prOpen}, "#45 · open", CGrey},
		{threadPR{Num: 45, State: prMerged, Into: "origin/main"}, "#45 · merged", CGreen},
		{threadPR{State: prMerged, Into: "origin/main"}, "merged into origin/main", CGreen},
		{threadPR{Num: 45, State: prClosed}, "#45 · closed", CGrey},
		{threadPR{}, "", ""},
	} {
		if l, col := prLine(c.pr); l != c.line || col != c.colour {
			t.Errorf("prLine(%+v) = %q %q, want %q %q", c.pr, l, col, c.line, c.colour)
		}
	}
}

// A row says its pull request merged (x closes it) in place of the rest,
// unless it is mid-turn; its checks failing, while it rests.
func TestRowSignalsPR(t *testing.T) {
	now := int64(2_000_000_000)
	merged := threadPR{Num: 45, State: prMerged}
	failing := threadPR{Num: 45, State: prOpen, Checks: "failing"}
	for _, c := range []struct {
		r    session.Row
		fact string
		pr   threadPR
		want string
	}{
		{session.Row{State: agent.StateIdle, Turn: 1}, "", merged, "merged · x closes"},
		{session.Row{State: agent.StateDone}, "Pushed.", merged, "merged · x closes"},
		{session.Row{State: agent.StateIdle, Exited: true}, "exited · R starts it again, x closes it", merged, "merged · x closes"},
		{session.Row{State: agent.StateWorking}, "thinking", merged, "thinking"},
		{session.Row{State: agent.StateNeedsYou}, "input", merged, "input"},
		{session.Row{State: agent.StateIdle, Turn: 1}, "", failing, "checks failing"},
		{session.Row{State: agent.StateDone, Seen: true}, "Pushed.", failing, "checks failing · Pushed."},
		{session.Row{State: agent.StateDone}, "Pushed.", failing, "Pushed."},
		{session.Row{State: agent.StateWorking}, "thinking", failing, "thinking"},
		{session.Row{State: agent.StateIdle, Turn: 1}, "", threadPR{Num: 45, State: prOpen, Checks: "passing"}, ""},
	} {
		if got := rowSignals(c.fact, c.r, now, c.pr, false); got != c.want {
			t.Errorf("rowSignals(%q, %s, %+v) = %q, want %q", c.fact, c.r.State, c.pr, got, c.want)
		}
	}
	if got := rowSignals("", session.Row{State: agent.StateIdle, Turn: 1}, now, failing, true); got != "checks failing · update · R" {
		t.Errorf("failing and behind: %q", got)
	}
}

// gitRun runs git in dir, as a test's own author.
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@e", "-c", "user.name=t"}, args...)...)
	c.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// ghRepo is a repository on GitHub's o/r, its origin's main where its
// main is, and a worktree on branch feat from it; their paths.
func ghRepo(t *testing.T) (repo, wt string) {
	t.Helper()
	d := t.TempDir()
	repo, wt = filepath.Join(d, "r"), filepath.Join(d, "feat")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "init", "-q", "-b", "main")
	gitRun(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	gitRun(t, repo, "remote", "add", "origin", "https://github.com/o/r.git")
	gitRun(t, repo, "update-ref", "refs/remotes/origin/main", "HEAD")
	gitRun(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat", wt)
	return repo, wt
}

// Git says a thread's branch merged when its HEAD is in its remote's
// default branch and it took commits of its own: a branch never committed
// to, in it from the start, has not; nor has the default branch itself.
func TestMergedInto(t *testing.T) {
	repo, wt := ghRepo(t)
	m := newPRMemo()
	// Nothing of its own, and origin's main moved on: in it, not merged.
	gitRun(t, repo, "commit", "-q", "--allow-empty", "-m", "later")
	gitRun(t, repo, "update-ref", "refs/remotes/origin/main", "HEAD")
	if got := m.mergedInto(wt, "feat"); got != "" {
		t.Errorf("a branch never committed to: merged into %q", got)
	}
	gitRun(t, wt, "merge", "-q", "--ff-only", "origin/main")
	if got := m.mergedInto(wt, "feat"); got != "" {
		t.Errorf("a branch never committed to, brought up to main: merged into %q", got)
	}
	// Its own commit, not on main yet.
	gitRun(t, wt, "commit", "-q", "--allow-empty", "-m", "feat")
	if got := m.mergedInto(wt, "feat"); got != "" {
		t.Errorf("a commit not on main: merged into %q", got)
	}
	// Pushed to main.
	gitRun(t, wt, "update-ref", "refs/remotes/origin/main", "HEAD")
	if got := m.mergedInto(wt, "feat"); got != "origin/main" {
		t.Errorf("its commit on origin's main: merged into %q, want origin/main", got)
	}
	if got := m.mergedInto(repo, "main"); got != "" {
		t.Errorf("the default branch itself: merged into %q", got)
	}
	if got := m.mergedInto(wt, ""); got != "" {
		t.Errorf("no branch: merged into %q", got)
	}
}

// The tender reads each thread's pull request from the view's cache into
// the session's file, which the board's rows and the card read: a thread
// at rest whose checks fail says so; once its branch is on origin's main,
// it is merged, keeping its number.
func TestNotePRs(t *testing.T) {
	t.Setenv("COLUMNS", "120")
	t.Setenv("COLOR", "")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	repo, wt := ghRepo(t)
	e := &Env{&session.Env{StateDir: t.TempDir(), SKey: "rex-w", Session: "w"}}
	pane := jsonx.Int(17)
	if err := store.Write(e.ThreadFile("17"), store.Thread{Num: 7, Name: "feat", Repo: repo, Branch: "feat", Worktree: wt, Pane: &pane}); err != nil {
		t.Fatal(err)
	}
	c := ghCache{Results: map[github.Kind][]github.Result{github.PRs: {
		{Section: github.Section{Title: "Mine"}, List: page.List[github.Item]{Fetched: time.Now(), Items: []github.Item{
			{Kind: github.PRs, Repo: "o/r", Number: 45, Head: "feat", State: "OPEN", FailingChecks: []string{"ci"}, Requested: []string{"amy"}},
		}}},
	}}}
	if err := store.Write(ghCacheFile(e), c); err != nil {
		t.Fatal(err)
	}
	m := newPRMemo()
	e.notePRs(m)
	want := threadPR{Num: 45, Repo: "o/r", State: prOpen, Checks: "failing", Review: "requested"}
	if got := e.readPRs()["17"]; got != want {
		t.Fatalf("read %+v, want %+v", got, want)
	}
	row := session.Row{Pane: "17", Tnum: 7, Title: "feat", State: agent.StateIdle, Since: time.Now().Unix() - 60, Root: wt, Turn: 1}
	if b := e.board(true, []session.Row{row}, nil, time.Now().Unix()); !strings.Contains(spaced(b), "| feat | checks failing |") {
		t.Errorf("the board, its checks failing:\n%s", b)
	}
	if got := ansi.Strip(card(agent.Record{}, row, "", 80, "", "", 0, 0, 0, 0, e.readPRs()["17"])); !strings.Contains(got, "pr         #45 · checks failing · review requested") {
		t.Errorf("the card:\n%s", got)
	}
	// Merged on GitHub (gone from the view's open sections), its commit on
	// origin's main.
	gitRun(t, wt, "commit", "-q", "--allow-empty", "-m", "feat")
	gitRun(t, wt, "update-ref", "refs/remotes/origin/main", "HEAD")
	c.Results[github.PRs][0].Items = nil
	if err := store.Write(ghCacheFile(e), c); err != nil {
		t.Fatal(err)
	}
	e.notePRs(m)
	want = threadPR{Num: 45, Repo: "o/r", State: prMerged, Into: "origin/main"}
	if got := e.readPRs()["17"]; got != want {
		t.Fatalf("merged: read %+v, want %+v", got, want)
	}
	if b := e.board(true, []session.Row{row}, nil, time.Now().Unix()); !strings.Contains(spaced(b), "| feat | merged · x closes |") {
		t.Errorf("the board, merged:\n%s", b)
	}
	if got := ansi.Strip(card(agent.Record{}, row, "", 80, "", "", 0, 0, 0, 0, e.readPRs()["17"])); !strings.Contains(got, "pr         #45 · merged") {
		t.Errorf("the card, merged:\n%s", got)
	}
}
