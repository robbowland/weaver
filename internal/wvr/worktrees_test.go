package wvr

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

// The panes being closed are the session's: a pane of the same number on
// another tmux server, or on tmux when the session is Rex's, is another
// pane, and still uses the worktree. Weaver's own server listed twice (the
// one we run in) counts each pane once.
func TestWorktreeUsersOwnPanes(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux")
	}
	tmp := t.TempDir()
	dir, _ := filepath.EvalSymlinks(tmp)
	w := dir + "/wt"
	if err := os.MkdirAll(w, 0o755); err != nil {
		t.Fatal(err)
	}
	// A server with pane %0, and with more %1, in the worktree: its pid.
	server := func(name string, more bool) (string, string) {
		t.Helper()
		sock := tmp + "/" + name + ".sock"
		tm := func(args ...string) string {
			t.Helper()
			out, err := exec.Command("tmux", append([]string{"-S", sock, "-f", "/dev/null"}, args...)...).Output()
			if err != nil {
				t.Fatalf("tmux %v: %v", args, err)
			}
			return strings.TrimSpace(string(out))
		}
		tm("new-session", "-d", "-s", name, "-c", w, "/bin/sleep 600")
		t.Cleanup(func() { _ = exec.Command("tmux", "-S", sock, "kill-server").Run() })
		want := w
		if more {
			tm("new-window", "-d", "-c", w, "/bin/sleep 600")
			want += "\n" + w
		}
		for i := 0; tm("list-panes", "-a", "-F", "#{pane_current_path}") != want; i++ {
			if i == 50 {
				t.Fatal("the panes never ran in the worktree")
			}
			time.Sleep(100 * time.Millisecond)
		}
		return sock, tm("display-message", "-p", "#{pid}")
	}
	weaver, wpid := server("weaver", true)
	outer, opid := server("outer", false)
	t.Setenv("REX_SERVER", "unix://"+tmp+"/no-rex.sock")
	env := func(rexID string, inTmux bool, srv string) *Env {
		e := &session.Env{StateDir: dir + "/state", LostDir: dir + "/lost", SKey: "tmux-weaver", RexID: rexID, InTmux: inTmux,
			TSock: []string{"-S", weaver}}
		e.Where.Server, _ = strconv.Atoi(srv)
		return &Env{e}
	}
	users := func(e *Env) string { return strings.Join(e.worktreeUsers(w, "0"), ", ") }

	// Outside tmux, in weaver's own session: its %0 closing.
	if got := users(env("", false, "")); got != "pane 1" {
		t.Errorf("weaver's session: %q, want pane 1", got)
	}
	// Inside it, weaver's server listed twice.
	t.Setenv("TMUX", weaver+","+wpid+",0")
	if got := users(env("", true, wpid)); got != "pane 1" {
		t.Errorf("inside weaver's session: %q, want pane 1", got)
	}
	// In another server's session, its %0 closing: weaver's %0 is in use.
	t.Setenv("TMUX", outer+","+opid+",0")
	if got := users(env("", true, opid)); got != "pane 0, pane 1" {
		t.Errorf("another server's session: %q, want weaver's pane 0 and pane 1", got)
	}
	// On Rex, its pane 0 closing: tmux's %0 is another pane.
	t.Setenv("TMUX", "")
	if got := users(env("session:x", false, "")); got != "pane 0, pane 1" {
		t.Errorf("a Rex session: %q, want tmux's pane 0 and pane 1", got)
	}
}
