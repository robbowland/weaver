package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// The default branch, each place in turn: the upstream remote's (its HEAD,
// else main or master), origin's, a local main or master, else none. A
// remote HEAD pointing at a branch that has gone counts for nothing.
func TestDefaultBase(t *testing.T) {
	for _, c := range []struct {
		name  string
		refs  []string // refs to make at the first commit
		heads map[string]string
		want  string
	}{
		{name: "upstream's HEAD", refs: []string{"refs/remotes/upstream/dev", "refs/remotes/origin/main"},
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
			if got := defaultBase(d); got != c.want {
				t.Errorf("defaultBase = %q, want %q", got, c.want)
			}
		})
	}
}

// The fork point is the merge base with the thread's base, as a SHA, not
// the base's name, worked out afresh: a base that has moved on keeps the
// branch's own commits, and only them; a rebase onto the moved base, or
// the base merged in, moves it to the base's tip; a base deleted is the
// default branch; with no default either, none.
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
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "base")
	fork := git("rev-parse", "HEAD")
	git("switch", "-q", "-c", "rel")
	git("commit", "-q", "--allow-empty", "-m", "rel's")
	relFork := git("rev-parse", "HEAD")
	git("switch", "-q", "-c", "feat")
	git("commit", "-q", "--allow-empty", "-m", "mine")
	git("switch", "-q", "rel")
	git("commit", "-q", "--allow-empty", "-m", "rel moved on")
	relTip := git("rev-parse", "HEAD")
	git("switch", "-q", "feat")
	for _, c := range []struct{ name, base, want string }{
		{"its base, moved on", "rel", relFork},
		{"none recorded: the default branch", "", fork},
		{"a base that names nothing: the default branch", "gone", fork},
	} {
		if got, ok := ForkPoint(d, c.base); !ok || got != c.want {
			t.Errorf("%s: ForkPoint = %q %v, want %q", c.name, got, ok, c.want)
		}
	}
	git("rebase", "-q", "rel")
	if got, _ := ForkPoint(d, "rel"); got != relTip {
		t.Errorf("rebased onto its base: ForkPoint = %q, want its tip %q", got, relTip)
	}
	git("switch", "-q", "rel")
	git("commit", "-q", "--allow-empty", "-m", "rel again")
	relTip = git("rev-parse", "HEAD")
	git("switch", "-q", "feat")
	git("merge", "-q", "--no-edit", "rel")
	if got, _ := ForkPoint(d, "rel"); got != relTip {
		t.Errorf("its base merged in: ForkPoint = %q, want its tip %q", got, relTip)
	}
	git("branch", "-q", "-D", "rel")
	if got, ok := ForkPoint(d, "rel"); !ok || got != fork {
		t.Errorf("its base deleted: ForkPoint = %q %v, want the default branch's %q", got, ok, fork)
	}
	git("branch", "-q", "-D", "main")
	if got, ok := ForkPoint(d, "rel"); ok {
		t.Errorf("no base, no default: ForkPoint = %q, want none", got)
	}
}

// A branch's stamp moves with what its range is worked out from (its
// HEAD: a commit, an amend, a reset, a switch; its base: a fetch, refs
// packed) and with nothing else (an edit, a look), in a main checkout and
// a linked worktree alike.
func TestStamp(t *testing.T) {
	d, _ := filepath.EvalSymlinks(t.TempDir())
	run := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@e", "-c", "user.name=t"}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	repo, wt := filepath.Join(d, "repo"), filepath.Join(d, "wt")
	run(d, "init", "-q", "-b", "main", repo)
	run(repo, "commit", "-q", "--allow-empty", "-m", "init")
	run(repo, "update-ref", "refs/remotes/origin/main", "HEAD")
	run(repo, "worktree", "add", "-q", "-b", "feat", wt)
	for _, dir := range []string{repo, wt} {
		last := Stamp(dir)
		if last == "" {
			t.Fatalf("%s: no stamp", dir)
		}
		// Ref writes within a second of each other differ by more than a
		// mtime: give each its own.
		step := func(what string, moves bool, f func()) {
			t.Helper()
			time.Sleep(15 * time.Millisecond)
			f()
			now := Stamp(dir)
			if (now != last) != moves {
				t.Errorf("%s, %s: moved %v, want %v", filepath.Base(dir), what, now != last, moves)
			}
			last = now
		}
		step("an edit", false, func() { _ = os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644) })
		step("a look", false, func() { run(dir, "status", "--porcelain") })
		step("a commit", true, func() { run(dir, "commit", "-q", "--allow-empty", "-m", "c") })
		step("an amend", true, func() { run(dir, "commit", "-q", "--amend", "--allow-empty", "-m", "c2") })
		step("a reset", true, func() { run(dir, "reset", "-q", "--hard", "HEAD~1") })
		step("its base fetched", true, func() { run(dir, "update-ref", "refs/remotes/origin/main", "HEAD") })
		step("refs packed", true, func() { run(dir, "pack-refs", "--all") })
		step("a commit, refs packed", true, func() { run(dir, "commit", "-q", "--allow-empty", "-m", "c3") })
	}
	run(wt, "switch", "-q", "-c", "other")
	if Stamp(wt) == Stamp(repo) {
		t.Error("two branches, one stamp")
	}
	if Stamp(d) != "" {
		t.Error("a stamp outside a work tree")
	}
}
