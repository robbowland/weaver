package session

import (
	"os"
	"path/filepath"
	"testing"
)

// weaver's socket directory is a directory of yours, for you alone: made
// so, tightened when loose, refused when it is a link (one another user
// could have made, to point elsewhere).
func TestSockDirReady(t *testing.T) {
	d := t.TempDir()
	e := &Env{SockDir: filepath.Join(d, "weaver-1")}
	if err := e.SockDirReady(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(e.SockDir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := e.SockDirReady(); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(e.SockDir); st.Mode().Perm() != 0o700 {
		t.Errorf("mode %v, want 0700", st.Mode().Perm())
	}
	l := filepath.Join(d, "weaver-2")
	if err := os.Symlink(e.SockDir, l); err != nil {
		t.Fatal(err)
	}
	if err := (&Env{SockDir: l}).SockDirReady(); err == nil {
		t.Error("a link taken for weaver's socket directory")
	}
}
