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
