package wvr

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robbowland/weaver/internal/git"
	"github.com/robbowland/weaver/internal/items"
	"github.com/robbowland/weaver/internal/session"
)

// A pull request's thread is on its head as it is now: the branch made
// there, or moved up to it when behind; one with commits of its own kept,
// and said; none at all when its head cannot be fetched and the branch is
// not here.
func TestPRHead(t *testing.T) {
	d := t.TempDir()
	run := func(dir string, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@e", "-c", "user.name=t"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	up, repo := filepath.Join(d, "up.git"), filepath.Join(d, "repo")
	run(d, "init", "-q", "--bare", "-b", "main", up)
	run(d, "clone", "-q", up, repo)
	run(repo, "commit", "-q", "--allow-empty", "-m", "base")
	run(repo, "push", "-q", "origin", "main")
	run(repo, "checkout", "-q", "-b", "add-flag")
	run(repo, "commit", "-q", "--allow-empty", "-m", "flag 1")
	run(repo, "push", "-q", "origin", "add-flag")
	old := run(repo, "rev-parse", "HEAD")
	run(repo, "checkout", "-q", "main")
	run(repo, "branch", "-q", "-D", "add-flag")
	// The author pushes again; GitHub keeps refs/pull/N/head.
	tip := run(up, "commit-tree", "-p", old, "-m", "flag 2", run(up, "rev-parse", old+"^{tree}"))
	run(up, "update-ref", "refs/heads/add-flag", tip)
	run(up, "update-ref", "refs/pull/45/head", tip)
	run(up, "update-ref", "refs/pull/46/head", tip)
	e := &Env{&session.Env{}}
	pr := items.PullRequest{Repo: "o/r", Num: 45, Head: "add-flag", Base: "main"}
	at := func(b string) string { return run(repo, "rev-parse", "refs/heads/"+b) }

	if note, ok := e.prHead(repo, pr, "add-flag"); !ok || note != "" || at("add-flag") != tip {
		t.Errorf("not here: %q %v, at %s", note, ok, at("add-flag"))
	}
	if up, _ := git.Line(repo, "rev-parse", "--abbrev-ref", "add-flag@{upstream}"); up != "origin/add-flag" {
		t.Errorf("its upstream: %q", up)
	}
	run(repo, "branch", "-q", "-f", "add-flag", old)
	if note, ok := e.prHead(repo, pr, "add-flag"); !ok || note != "" || at("add-flag") != tip {
		t.Errorf("behind: %q %v, at %s", note, ok, at("add-flag"))
	}
	run(repo, "checkout", "-q", "add-flag")
	run(repo, "commit", "-q", "--allow-empty", "-m", "mine")
	mine := run(repo, "rev-parse", "HEAD")
	run(repo, "checkout", "-q", "main")
	if note, ok := e.prHead(repo, pr, "add-flag"); !ok || !strings.Contains(note, "has commits") || at("add-flag") != mine {
		t.Errorf("its own commits: %q %v, at %s", note, ok, at("add-flag"))
	}
	fork := items.PullRequest{Repo: "o/r", Num: 46, Head: "main", Base: "main", Fork: true}
	if note, ok := e.prHead(repo, fork, fork.BranchName()); !ok || note != "" || at("pr-46") != tip {
		t.Errorf("a fork's: %q %v", note, ok)
	}
	run(repo, "remote", "set-url", "origin", filepath.Join(d, "gone.git"))
	gone := items.PullRequest{Repo: "o/r", Num: 47, Head: "nowhere", Base: "main"}
	if _, ok := e.prHead(repo, gone, "nowhere"); ok || git.HasBranch(repo, "nowhere") {
		t.Error("not fetched and not here: a branch made from somewhere")
	}
	if note, ok := e.prHead(repo, pr, "add-flag"); !ok || !strings.Contains(note, "could not fetch") {
		t.Errorf("not fetched, its branch here: %q %v", note, ok)
	}
}
