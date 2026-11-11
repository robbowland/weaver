package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkTree(t *testing.T) {
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", "-b", "main", d).CombinedOutput(); err != nil {
		t.Skipf("git init: %v %s", err, out)
	}
	sub := filepath.Join(d, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if w, ok := WorkTree(sub); !ok || w != d {
		t.Errorf("WorkTree(%s) = %q %v, want %q", sub, w, ok, d)
	}
	if w, ok := FileWorkTree(filepath.Join(d, "new", "dir", "f.go")); !ok || w != d {
		t.Errorf("FileWorkTree: %q %v", w, ok)
	}
	if repo, checkout, ok := RepoOf(sub); !ok || repo != d || checkout != d {
		t.Errorf("RepoOf: %q %q %v", repo, checkout, ok)
	}
	if _, ok := WorkTree("/"); ok {
		t.Error("/ is in no work tree")
	}
}

// A clone of a repository with main and a pushed feature branch: what its
// refs say, from the form's one call and from git directly, with and
// without the remote's HEAD.
func TestRefs(t *testing.T) {
	d := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@e", "-c", "user.name=t"}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v %s", args, err, out)
		}
	}
	up, repo := filepath.Join(d, "up"), filepath.Join(d, "repo")
	run(d, "init", "-q", "-b", "main", up)
	run(up, "commit", "-q", "--allow-empty", "-m", "init")
	run(up, "branch", "feat")
	run(d, "clone", "-q", up, repo)
	m := RefFacts(repo, "feat")
	if !m.HasBranch("main") || m.HasBranch("feat") || !m.HasRemoteBranch("origin", "feat") {
		t.Errorf("RefFacts: %v", m)
	}
	if got := m.RemoteDefault("origin"); got != "origin/main" {
		t.Errorf("Refs.RemoteDefault = %q", got)
	}
	if got := m.LocalDefault("origin"); got != "main" {
		t.Errorf("Refs.LocalDefault = %q", got)
	}
	if got := RemoteDefault(repo, "origin"); got != "origin/main" {
		t.Errorf("RemoteDefault = %q", got)
	}
	if got := LocalDefault(repo); got != "main" {
		t.Errorf("LocalDefault = %q", got)
	}
	if !HasBranch(repo, "main") || HasBranch(repo, "feat") || !HasRemoteBranch(repo, "origin", "feat") {
		t.Error("HasBranch / HasRemoteBranch")
	}
	// No HEAD on the remote: main, as the fallback finds it.
	run(repo, "remote", "set-head", "origin", "-d")
	if got := RemoteDefault(repo, "origin"); got != "origin/main" {
		t.Errorf("RemoteDefault without origin/HEAD = %q", got)
	}
	if got := RefFacts(repo, "").RemoteDefault("origin"); got != "origin/main" {
		t.Errorf("Refs.RemoteDefault without origin/HEAD = %q", got)
	}
}

// What a branch is reviewed against, each source in turn falling through
// to the next when it names no commit: $WEAVER_BASE, the branch's
// weaverbase, the upstream remote's default branch (its HEAD, else main or
// master), origin's, a local main or master, else none. A remote HEAD
// pointing at a branch that has gone counts for nothing.
func TestReviewBase(t *testing.T) {
	for _, c := range []struct {
		name, env, weaverbase string
		refs                  []string // refs to make at the first commit
		heads                 map[string]string
		want                  string
	}{
		{name: "WEAVER_BASE", env: "dev", weaverbase: "rel", refs: []string{"refs/heads/dev", "refs/heads/rel"}, want: "dev"},
		{name: "weaverbase", env: "nope", weaverbase: "rel", refs: []string{"refs/heads/rel", "refs/remotes/origin/main"}, want: "rel"},
		{name: "upstream's HEAD", weaverbase: "nope", refs: []string{"refs/remotes/upstream/dev", "refs/remotes/origin/main"},
			heads: map[string]string{"upstream": "dev"}, want: "upstream/dev"},
		{name: "upstream's HEAD gone, its main", refs: []string{"refs/remotes/upstream/main", "refs/remotes/origin/main"},
			heads: map[string]string{"upstream": "gone"}, want: "upstream/main"},
		{name: "origin's HEAD", refs: []string{"refs/remotes/origin/trunk", "refs/remotes/origin/main"},
			heads: map[string]string{"origin": "trunk"}, want: "origin/trunk"},
		{name: "origin's master", refs: []string{"refs/remotes/origin/master"}, want: "origin/master"},
		{name: "a local master", refs: []string{"refs/heads/master"}, want: "master"},
		{name: "none", want: ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := t.TempDir()
			git := func(args ...string) {
				t.Helper()
				cmd := exec.Command("git", append([]string{"-C", d, "-c", "user.email=t@e", "-c", "user.name=t"}, args...)...)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v %s", args, err, out)
				}
			}
			git("init", "-q", "-b", "feat")
			git("commit", "-q", "--allow-empty", "-m", "init")
			for _, r := range c.refs {
				git("update-ref", r, "HEAD")
			}
			for rm, b := range c.heads {
				git("symbolic-ref", "refs/remotes/"+rm+"/HEAD", "refs/remotes/"+rm+"/"+b)
			}
			if c.weaverbase != "" {
				git("config", "branch.feat.weaverbase", c.weaverbase)
			}
			t.Setenv("WEAVER_BASE", c.env)
			if got := ReviewBase(d); got != c.want {
				t.Errorf("ReviewBase = %q, want %q", got, c.want)
			}
		})
	}
}

// The fork point is the merge base as a SHA, not the base's name: a base
// that has moved on since the branch left it keeps the branch's own
// commits, and only them, in base..HEAD.
func TestForkPoint(t *testing.T) {
	d := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", d, "-c", "user.email=t@e", "-c", "user.name=t"}, args...)...)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	t.Setenv("WEAVER_BASE", "")
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "base")
	fork := git("rev-parse", "HEAD")
	git("switch", "-q", "-c", "feat")
	git("commit", "-q", "--allow-empty", "-m", "mine")
	git("switch", "-q", "main")
	git("commit", "-q", "--allow-empty", "-m", "moved on")
	git("switch", "-q", "feat")
	if got, ok := ForkPoint(d); !ok || got != fork {
		t.Errorf("ForkPoint = %q %v, want %q", got, ok, fork)
	}
	git("branch", "-q", "-D", "main")
	if got, ok := ForkPoint(d); ok {
		t.Errorf("no base: ForkPoint = %q, want none", got)
	}
}
