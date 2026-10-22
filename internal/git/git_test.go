package git

import (
	"os"
	"os/exec"
	"path/filepath"
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
