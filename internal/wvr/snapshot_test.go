package wvr

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Old session chains and the private indexes nothing has used since go; a
// recent chain, its index, and the reviewed mark stay.
func TestPruneSnapshots(t *testing.T) {
	top := t.TempDir()
	git := func(env []string, args ...string) string {
		c := exec.Command("git", append([]string{"-C", top}, args...)...)
		c.Env = append(os.Environ(), env...)
		out, err := c.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimRight(string(out), "\n")
	}
	git(nil, "init", "-q")
	tree := git(nil, "write-tree")
	id := []string{"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e"}
	commit := func(at time.Time) string {
		d := at.Format(time.RFC3339)
		return git(append(id, "GIT_COMMITTER_DATE="+d, "GIT_AUTHOR_DATE="+d), "commit-tree", tree, "-m", "s")
	}
	now := time.Now()
	git(nil, "update-ref", "refs/worktree/weaver/sessions/old", commit(now.Add(-40*24*time.Hour)))
	git(nil, "update-ref", "refs/worktree/weaver/sessions/new", commit(now.Add(-time.Hour)))
	git(nil, "update-ref", "refs/worktree/weaver/reviewed", commit(now.Add(-90*24*time.Hour)))
	gd := filepath.Join(top, ".git", "weaver")
	os.MkdirAll(gd, 0o755)
	stale := now.Add(-40 * 24 * time.Hour)
	for _, f := range []string{"index-old", "index-new", "index-reviewed"} {
		os.WriteFile(filepath.Join(gd, f), []byte("x"), 0o644)
	}
	os.Chtimes(filepath.Join(gd, "index-old"), stale, stale)
	os.Chtimes(filepath.Join(gd, "index-reviewed"), stale, stale)

	pruneSnapshots(top, gd, now.Add(-snapKeep))

	refs := git(nil, "for-each-ref", "--format=%(refname)", "refs/worktree/weaver/")
	if refs != "refs/worktree/weaver/reviewed\nrefs/worktree/weaver/sessions/new" {
		t.Errorf("refs left:\n%s", refs)
	}
	for f, want := range map[string]bool{"index-old": false, "index-new": true, "index-reviewed": true} {
		if got := exists(filepath.Join(gd, f)); got != want {
			t.Errorf("%s: exists %v, want %v", f, got, want)
		}
	}
}
