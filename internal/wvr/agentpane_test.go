package wvr

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/items"
	"github.com/robbowland/weaver/internal/jsonx"
	"github.com/robbowland/weaver/internal/store"
)

// A thread's agent is told what its thread keeps for it as Claude Code's
// --append-system-prompt, which starts no turn: before the arguments
// that start or resume it, its mode held. With nothing to tell, no option.
func TestAgentArgv(t *testing.T) {
	got := agentArgv("claude --dangerously-skip-permissions", agent.ModeReview, "the item", []string{"--resume", "c1"})
	want := []string{"claude", "--permission-mode", "auto", "--append-system-prompt", "the item", "--resume", "c1"}
	if !slices.Equal(got, want) {
		t.Errorf("review, told: %q\nwant %q", got, want)
	}
	if got := agentArgv("claude", agent.ModeWork, "", []string{"--session-id", "s"}); !slices.Equal(got, []string{"claude", "--session-id", "s"}) {
		t.Errorf("nothing to tell: %q", got)
	}
	if got := agentArgv("", agent.ModeWork, "x", nil); got != nil {
		t.Errorf("no command: %q", got)
	}
}

// What a new thread on an item with no task is told (its context in the
// meeting place) moves to the thread's own directory at its first start,
// and every later start finds it there: resumed in another pane, renamed.
func TestThreadContextFollowsTheThread(t *testing.T) {
	state, d := t.TempDir(), t.TempDir()
	about := items.Context(items.PullRequest{Repo: "o/r", Num: 45, Head: "h", Base: "main", Text: items.Text{Title: "T"}}, agent.ModeReview, "origin/main")
	if err := os.WriteFile(d+"/context", []byte(about), 0o644); err != nil {
		t.Fatal(err)
	}
	th := store.Thread{Name: "pr-45", Worktree: "/w/pr-45", Created: 1700000000, Mode: agent.ModeReview}
	if got := takeContext(d, state, th, true); got != about {
		t.Fatalf("first start told %q", got)
	}
	later := th
	p := jsonx.Int(9)
	later.Pane, later.Title, later.Name = &p, "renamed", "other"
	if got := threadContext(state, later); got != about || !strings.Contains(got, "change no files and make no commits") {
		t.Errorf("a later start told %q", got)
	}
	if got := threadContext(state, store.Thread{Worktree: "/w/other", Created: 1700000000}); got != "" {
		t.Errorf("another thread told %q", got)
	}
	// No record read: this start is told, none kept.
	d2 := t.TempDir()
	if err := os.WriteFile(d2+"/context", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	none := store.Thread{Worktree: "/w/none", Created: 5}
	if got := takeContext(d2, state, none, false); got != "x" || threadContext(state, none) != "" {
		t.Errorf("unkept: told %q, kept %q", got, threadContext(state, none))
	}
	if got := takeContext(t.TempDir(), state, th, true); got != "" {
		t.Errorf("no context: %q", got)
	}
}
