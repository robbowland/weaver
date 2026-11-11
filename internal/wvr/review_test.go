package wvr

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
)

// Where alt+v says the agent is (its review panel opens there): its
// state's root, else its directory, else its thread's worktree, and the
// file it was at under it; never a guess (weaver's own directory) when none
// of those is there.
func TestAgentWhere(t *testing.T) {
	td, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &Env{&session.Env{StateDir: td + "/state", SKey: "rex-s"}}
	wt := td + "/wt"
	if err := os.MkdirAll(wt+"/src", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wt+"/src/a.go", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(e.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name       string
		state      *agent.Record
		thread     *store.Thread
		root, file string
		known      bool
	}{
		{"its state's root and file", &agent.Record{Root: wt, Cwd: "/", File: wt + "/src/a.go"}, nil, wt, wt + "/src/a.go", true},
		{"its directory, while it has no root", &agent.Record{Cwd: wt}, nil, wt, "", true},
		{"a file outside its root, dropped", &agent.Record{Root: wt + "/src", File: td + "/elsewhere.go"}, nil, wt + "/src", "", true},
		{"its thread's worktree, before its first hook", nil, &store.Thread{Worktree: wt}, wt, "", true},
		{"a root that has gone", &agent.Record{Root: td + "/gone"}, nil, "", "", false},
		{"nothing known", nil, nil, "", "", false},
	} {
		os.Remove(e.StateFile("5"))
		os.Remove(e.ThreadFile("5"))
		if c.state != nil {
			if err := store.Write(e.StateFile("5"), c.state); err != nil {
				t.Fatal(err)
			}
		}
		if c.thread != nil {
			if err := store.Write(e.ThreadFile("5"), c.thread); err != nil {
				t.Fatal(err)
			}
		}
		root, file, known := e.agentWhere("5")
		if root != c.root || file != c.file || known != c.known {
			t.Errorf("%s: %q %q %v, want %q %q %v", c.name, root, file, known, c.root, c.file, c.known)
		}
	}
}

// Rex's init.lua loads weaver's keys however it requires them, but not
// from a comment.
func TestLoadsWeaver(t *testing.T) {
	for init, want := range map[string]bool{
		`require("weaver")`:                  true,
		`require "weaver"`:                   true,
		`pcall(require, "weaver")`:           true,
		`local ok = pcall(require,'weaver')`: true,
		`-- pcall(require, "weaver")`:        false,
		`require("weaver_extra")`:            false,
		`rex.bind("cmd+d", "pane.split")`:    false,
	} {
		if got := loadsWeaver("x = 1\n" + init + "\n"); got != want {
			t.Errorf("loadsWeaver(%q) = %v, want %v", init, got, want)
		}
	}
}

// weaver's keys on Rex carry the settings a key's wvr reads that are set
// where wvr writes them (Rex's server has an app's environment): as Lua
// fields, escaped; set but empty kept, unset left out.
func TestLuaSettings(t *testing.T) {
	for _, k := range rexSettings {
		t.Setenv(k, "")
		if err := os.Unsetenv(k); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("XDG_STATE_HOME", `/a "b"\c`)
	t.Setenv("WEAVER_VIEWS", "")
	lua := func(s string) string { return "<" + s + ">" }
	want := `XDG_STATE_HOME = "</a "b"\c>", WEAVER_VIEWS = "<>"`
	if got := luaSettings(lua); got != want {
		t.Errorf("luaSettings = %s, want %s", got, want)
	}
}

// What the panel runs tuicr on, round after round: nothing on a branch
// with nothing of its own (tuicr refuses an empty range), what is not
// committed once there is some (an edit, or a file git does not track
// yet), then the branch's commits since its fork point, by SHA, with what
// is not committed, also once the base has moved on.
func TestReviewArgs(t *testing.T) {
	d := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", d, "-c", "user.email=t@e", "-c", "user.name=t"}, args...)...).Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	look := " --theme micrographics --no-update-check"
	got := func() string { return strings.Join(reviewArgs(d, ""), " ") }
	git("init", "-q", "-b", "main")
	if err := os.WriteFile(d+"/a.txt", []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "base")
	fork := git("rev-parse", "HEAD")
	git("switch", "-q", "-c", "feat")
	if a := got(); a != "" {
		t.Errorf("a new branch: %q, want nothing", a)
	}
	if err := os.WriteFile(d+"/new.txt", []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if a := got(); a != "-w"+look {
		t.Errorf("an untracked file: %q", a)
	}
	git("add", ".")
	git("commit", "-q", "-m", "mine")
	git("switch", "-q", "main")
	git("commit", "-q", "--allow-empty", "-m", "moved on")
	git("switch", "-q", "feat")
	if a := got(); a != "-r "+fork+"..HEAD -w"+look {
		t.Errorf("a commit of its own: %q", a)
	}
	// Commits with no change in all (tuicr refuses a range whose diff is
	// empty): reverted, then the change back, untracked.
	git("revert", "--no-edit", "HEAD")
	if a := got(); a != "" {
		t.Errorf("a commit and its revert: %q, want nothing", a)
	}
	if err := os.WriteFile(d+"/new.txt", []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if a := got(); a != "-r "+fork+"..HEAD -w"+look {
		t.Errorf("reverted, with a file git does not track: %q", a)
	}
	// Its change in the base too (squashed in there), and the base merged
	// back: commits of its own, nothing it changes.
	git("add", ".")
	git("commit", "-q", "-m", "new again")
	git("switch", "-q", "main")
	if err := os.WriteFile(d+"/new.txt", []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "squashed")
	git("switch", "-q", "feat")
	git("merge", "-q", "--no-edit", "main")
	if a := got(); a != "" {
		t.Errorf("its change in the base, merged back: %q, want nothing", a)
	}
}

// A thread is reviewed against its own base, as its record says now: one
// off a release branch from where it left that branch, not main.
func TestReviewAgainstThreadBase(t *testing.T) {
	d := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", d, "-c", "user.email=t@e", "-c", "user.name=t"}, args...)...).Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "base")
	git("switch", "-q", "-c", "release/2.0")
	if err := os.WriteFile(d+"/rel.txt", []byte("rel\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "release's")
	fork := git("rev-parse", "HEAD")
	git("switch", "-q", "-c", "fix")
	if err := os.WriteFile(d+"/fix.txt", []byte("fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "fix")
	e := &Env{&session.Env{StateDir: t.TempDir(), SKey: "rex-s"}}
	if err := store.Write(e.ThreadFile("5"), store.Thread{Num: 1, Base: baseBranch(d, "release/2.0")}); err != nil {
		t.Fatal(err)
	}
	if a := strings.Join(reviewArgs(d, e.reviewBase("5")), " "); !strings.HasPrefix(a, "-r "+fork+"..HEAD ") {
		t.Errorf("a thread off release/2.0: %q, want from %s", a, fork)
	}
}
