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
// be lost; one with work in it (untracked files status is set to hide
// too), a lost thread's, or one just made stays, and one already gone
// leaves the list. The ignored files of one that goes are kept aside (not
// whole ignored directories, nor .DS_Store).
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
	for _, b := range []string{"done", "dirty", "lost", "young", "hidden"} {
		git("-C", repo, "worktree", "add", "-q", "-b", b, wt(b))
	}
	git("-C", repo, "config", "status.showUntrackedFiles", "no")
	write := func(f, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(wt("dirty")+"/notes.txt", "work\n")
	write(wt("hidden")+"/new/draft.txt", "work\n")
	write(repo+"/.git/info/exclude", ".env\nnode_modules/\n.DS_Store\n")
	write(wt("done")+"/.env", "TOKEN=x\n")
	write(wt("done")+"/node_modules/pkg.js", "x\n")
	write(wt("done")+"/.DS_Store", "x\n")
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
	}{{wt("done"), old}, {wt("dirty"), old}, {wt("lost"), old}, {wt("young"), now}, {wt("hidden"), old}, {dir + "/wt-gone", old}} {
		fmt.Fprintf(&list, "%s\t%s\t%d\n", w.dir, repo, w.at)
	}
	if err := os.WriteFile(e.worktreesFile(), []byte(list.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	removed := e.SweepWorktrees(io.Discard)
	if len(removed) != 1 || removed[0] != wt("done") || files.Exists(wt("done")) {
		t.Fatalf("removed %q, want only %s (gone from disk)", removed, wt("done"))
	}
	for _, b := range []string{"dirty", "lost", "young", "hidden"} {
		if !files.Exists(wt(b)) {
			t.Errorf("%s was removed", b)
		}
	}
	kept, _ := filepath.Glob(e.StateDir + "/trash/wt-done-*")
	if len(kept) != 1 || !files.Exists(kept[0]+"/.env") || files.Exists(kept[0]+"/node_modules") || files.Exists(kept[0]+"/.DS_Store") {
		t.Errorf("its ignored files kept: %q", kept)
	}
	var left []string
	for _, w := range e.worktreesMade() {
		left = append(left, filepath.Base(w.dir))
	}
	if got := strings.Join(left, " "); got != "wt-dirty wt-lost wt-young wt-hidden" {
		t.Errorf("the list keeps %q, want wt-dirty wt-lost wt-young wt-hidden", got)
	}
	e.forgetWorktree(wt("young"))
	if n := len(e.worktreesMade()); n != 3 {
		t.Errorf("kept on purpose: %d left on the list, want 3", n)
	}
}

// What uses a worktree, as close and the sweep both ask: a thread of any
// session working there, a lost one, but not the thread being closed.
func TestWorktreeUsers(t *testing.T) {
	dir := t.TempDir()
	e := &Env{&session.Env{StateDir: dir, LostDir: dir + "/lost", SKey: "tmux-a", TSock: []string{"-S", dir + "/no.sock"}}}
	w := dir + "/wt"
	put := func(f string, v any) {
		t.Helper()
		b, _ := json.Marshal(v)
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put(dir+"/tmux-a_4.thread", store.Thread{Num: 1, Worktree: w})
	put(dir+"/tmux-b_9.thread", store.Thread{Num: 3, Worktree: w})
	put(dir+"/tmux-a_5.thread", store.Thread{Num: 2, Worktree: dir + "/other"})
	put(e.LostDir+"/tmux-a_s.json", store.Lost{Thread: store.Thread{Num: 7, Worktree: w}})
	if got := strings.Join(e.worktreeUsers(w, "4"), ", "); got != "thread 03, lost thread 07" {
		t.Errorf("users: %q", got)
	}
}
