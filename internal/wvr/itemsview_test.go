package wvr

import (
	"testing"

	"github.com/robbowland/weaver/internal/github"
)

// A pull request from a fork works on pr-N, as its thread does
// (items.PullRequest): never on the fork's own branch, which names a
// branch of this repository too (its main, say).
func TestForkPullRequestThread(t *testing.T) {
	it := ghItem(github.Item{Kind: github.PRs, Repo: "o/r", Number: 45, Head: "main", Base: "main", HeadInFork: true})
	if it.branch != "pr-45" || it.startOn != "pr-45" {
		t.Errorf("branch %q, starts on %q; want pr-45", it.branch, it.startOn)
	}
	main := viewThread{num: 1, repo: "o/r", branch: "main"}
	if th, ok := threadOf([]viewThread{main}, it); ok {
		t.Errorf("joined thread %02d on main", th.num)
	}
	if th, ok := threadOf([]viewThread{main, {num: 2, repo: "o/r", branch: "pr-45"}}, it); !ok || th.num != 2 {
		t.Errorf("thread on pr-45: %v %v", th, ok)
	}
}
