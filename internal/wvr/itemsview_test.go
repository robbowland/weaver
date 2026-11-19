package wvr

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/robbowland/weaver/internal/github"
	"github.com/robbowland/weaver/internal/linear"
)

// A pull request from a fork works on pr-N, as its thread does
// (items.PullRequest): never on the fork's own branch, which names a
// branch of this repository too (its main, say).
func TestForkPullRequestThread(t *testing.T) {
	it := ghItem(github.Item{Kind: github.PRs, Repo: "o/r", Number: 45, Head: "main", Base: "main", HeadInFork: true})
	if it.branch != "pr-45" || it.startOn != "pr-45" {
		t.Errorf("branch %q, starts on %q; want pr-45", it.branch, it.startOn)
	}
	main := viewThread{num: 1, key: threadKey{repo: "o/r", branch: "main"}}
	if th, ok := threadOf([]viewThread{main}, it); ok {
		t.Errorf("joined thread %02d on main", th.num)
	}
	if th, ok := threadOf([]viewThread{main, {num: 2, key: threadKey{repo: "o/r", branch: "pr-45"}}}, it); !ok || th.num != 2 {
		t.Errorf("thread on pr-45: %v %v", th, ok)
	}
}

// A repository is GitHub's owner/name in any case, as the form finds its
// checkout: a remote's O/R is the item's o/r.
func TestThreadOfRepoCase(t *testing.T) {
	it := ghItem(github.Item{Kind: github.PRs, Repo: "o/r", Number: 45, Head: "add-flag"})
	if th, ok := threadOf([]viewThread{{num: 3, key: threadKey{repo: "O/R", branch: "add-flag"}}}, it); !ok || th.num != 3 {
		t.Errorf("thread in O/R: %v %v", th, ok)
	}
	if _, ok := threadOf([]viewThread{{num: 3, key: threadKey{repo: "o/s", branch: "add-flag"}}}, it); ok {
		t.Error("joined a thread of another repository")
	}
}

// A thread is its item's in any case: GitHub takes owner/name in any
// case, and a thread keeps the one you typed (wvr new --pr O/R#45).
func TestThreadOfRefCase(t *testing.T) {
	it := ghItem(github.Item{Kind: github.PRs, Repo: "o/r", Number: 45, Head: "add-flag"})
	if th, ok := threadOf([]viewThread{{num: 4, key: threadKey{ref: "O/R#45", branch: "review-45"}}}, it); !ok || th.num != 4 {
		t.Errorf("thread from O/R#45: %v %v", th, ok)
	}
}

// A pull request's fact in the GITHUB view is the one thing to know,
// coloured by its signal: changes requested or a check failing red,
// approved green; its card's review and checks lines alike.
func TestGHFact(t *testing.T) {
	pr := func(f func(*github.Item)) github.Item {
		it := github.Item{Kind: github.PRs, Repo: "o/r", Number: 45, Author: "amy"}
		f(&it)
		return it
	}
	for _, c := range []struct {
		it   github.Item
		want string
	}{
		{pr(func(it *github.Item) { it.Draft, it.Review = true, "CHANGES_REQUESTED" }), CDim + "draft" + COff},
		{pr(func(it *github.Item) { it.Review, it.FailingChecks = "CHANGES_REQUESTED", []string{"ci"} }), CRed + "changes requested" + COff},
		{pr(func(it *github.Item) { it.Review, it.FailingChecks = "APPROVED", []string{"ci"} }), CRed + "ci failing" + COff},
		{pr(func(it *github.Item) { it.FailingChecks = []string{"ci", "lint"} }), CRed + "2 checks failing" + COff},
		{pr(func(it *github.Item) { it.Review, it.RunningChecks = "APPROVED", 2 }), CGreen + "approved" + COff},
		{pr(func(it *github.Item) { it.RunningChecks, it.Requested = 2, []string{"bob"} }), CGrey + "checks running" + COff},
		{pr(func(it *github.Item) { it.PassingChecks, it.Review = 3, "REVIEW_REQUIRED" }), CDim + "@amy" + COff},
		{github.Item{Kind: github.Issues, Author: "amy", Labels: []string{"bug"}}, CGrey + "bug" + COff},
	} {
		if got := ghFact(c.it); got != c.want {
			t.Errorf("ghFact(%+v) = %q, want %q", c.it, got, c.want)
		}
	}
	field := func(v viewItem, label string) cardField {
		for _, f := range v.fields {
			if f.label == label {
				return f
			}
		}
		return cardField{}
	}
	v := ghItem(pr(func(it *github.Item) { it.Review, it.FailingChecks = "APPROVED", []string{"ci"} }))
	if f := field(v, "review"); f.text != "approved" || f.colour != CGreen {
		t.Errorf("review line: %+v", f)
	}
	if f := field(v, "checks"); f.text != "1 failing: ci" || f.colour != CRed {
		t.Errorf("checks line: %+v", f)
	}
	if f := field(ghItem(pr(func(it *github.Item) { it.Review = "CHANGES_REQUESTED" })), "review"); f.text != "changes requested" || f.colour != CRed {
		t.Errorf("review line, changes requested: %+v", f)
	}
}

// What GitHub and Linear say reaches a view's rows and cards on one line:
// a TAB or newline in a label or a check's name would split the row (fzf's
// fields, so keys act on another item), an ESC reach the terminal.
func TestItemTextOnOneLine(t *testing.T) {
	bad := "x\ty\nz\x1b]52;c;aGk=\x07\u009b31m"
	gh := ghItem(github.Item{Kind: github.PRs, Repo: "o/r", Number: 1, Title: "t", Author: bad,
		Labels: []string{bad}, FailingChecks: []string{bad}, Requested: []string{bad}, Assignees: []string{bad}})
	is := ghItem(github.Item{Kind: github.Issues, Repo: "o/r", Number: 2, Title: "t", Labels: []string{bad}})
	lin := linItem(linear.Issue{Identifier: "ENG-1", Title: "t", State: linear.State{Name: bad, Type: "started"},
		Team: linear.Team{Name: bad}, Labels: []string{bad}, Project: bad, Assignee: &linear.Person{Name: bad}})
	for _, v := range []viewItem{gh, is, lin} {
		texts := []string{v.fact, v.sub}
		for _, f := range v.fields {
			texts = append(texts, f.text)
		}
		for _, s := range texts {
			// weaver's own colours aside, nothing but text.
			plain := ansi.Strip(s)
			if strings.ContainsAny(plain, "\t\n\x1b\x07\u009b") {
				t.Errorf("%s: %q", v.ref, s)
			}
		}
	}
}
