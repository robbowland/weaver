package wvr

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
)

// A worktree weaver made goes once nothing uses it and nothing in it would
// be lost; one with work in it, a lost thread's, or one just made stays,
// and one already gone leaves the list.
func TestSweepWorktrees(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	repo := dir + "/repo"
	git("init", "-q", "-b", "main", repo)
	git("-C", repo, "-c", "user.email=t@e", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init")
	wt := func(b string) string { return dir + "/wt-" + b }
	for _, b := range []string{"done", "dirty", "lost", "young"} {
		git("-C", repo, "worktree", "add", "-q", "-b", b, wt(b))
	}
	if err := os.WriteFile(wt("dirty")+"/notes.txt", []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WEAVER_WORKTRUNK", "0")
	e := &Env{&session.Env{StateDir: dir + "/state", LostDir: dir + "/state/lost", TSock: []string{"-S", dir + "/no.sock"}}}
	if err := os.MkdirAll(e.LostDir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(store.Lost{Thread: store.Thread{Worktree: wt("lost")}})
	if err := os.WriteFile(e.LostDir+"/x.json", b, 0o644); err != nil {
		t.Fatal(err)
	}
	old, now := time.Now().Add(-2*time.Hour).Unix(), time.Now().Unix()
	var list strings.Builder
	for _, w := range []struct {
		dir string
		at  int64
	}{{wt("done"), old}, {wt("dirty"), old}, {wt("lost"), old}, {wt("young"), now}, {dir + "/wt-gone", old}} {
		fmt.Fprintf(&list, "%s\t%s\t%d\n", w.dir, repo, w.at)
	}
	if err := os.WriteFile(e.worktreesFile(), []byte(list.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	removed := e.SweepWorktrees(io.Discard)
	if len(removed) != 1 || removed[0] != wt("done") || files.Exists(wt("done")) {
		t.Fatalf("removed %q, want only %s (gone from disk)", removed, wt("done"))
	}
	for _, b := range []string{"dirty", "lost", "young"} {
		if !files.Exists(wt(b)) {
			t.Errorf("%s was removed", b)
		}
	}
	var left []string
	for _, w := range e.worktreesMade() {
		left = append(left, filepath.Base(w.dir))
	}
	if got := strings.Join(left, " "); got != "wt-dirty wt-lost wt-young" {
		t.Errorf("the list keeps %q, want wt-dirty wt-lost wt-young", got)
	}
	e.forgetWorktree(wt("young"))
	if n := len(e.worktreesMade()); n != 2 {
		t.Errorf("kept on purpose: %d left on the list, want 2", n)
	}
}
