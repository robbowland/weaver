package wvr

import (
	"os"
	"testing"

	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
)

// A thread from a Linear issue starts in the repository its team's issues'
// threads last started in; a team new to weaver, or its repository gone,
// in the one used last.
func TestLinearRepo(t *testing.T) {
	e := &Env{&session.Env{StateDir: t.TempDir()}}
	eng, ops := t.TempDir(), t.TempDir()
	if r := e.linearRepo("ENG-42"); r != "" {
		t.Errorf("nothing used yet: %q", r)
	}
	start := func(ref, repo string) {
		e.rememberRepo(repo)
		e.rememberTeamRepo(&store.ThreadItem{Kind: "linear", Ref: ref}, repo)
	}
	start("ENG-1", eng)
	start("OPS-7", ops)
	// A thread from no Linear issue changes no team's.
	e.rememberRepo(ops)
	e.rememberTeamRepo(&store.ThreadItem{Kind: "issue", Ref: "o/r#3"}, eng)
	for id, want := range map[string]string{"ENG-42": eng, "eng-43": eng, "OPS-1": ops, "NEW-1": ops} {
		if got := e.linearRepo(id); got != want {
			t.Errorf("%s: %s, want %s", id, got, want)
		}
	}
	start("NEW-1", eng)
	if got := e.linearRepo("OPS-2"); got != ops {
		t.Errorf("OPS after another team's thread: %s, want %s", got, ops)
	}
	start("OPS-8", ops)
	if err := os.RemoveAll(eng); err != nil {
		t.Fatal(err)
	}
	if got := e.linearRepo("ENG-44"); got != ops {
		t.Errorf("ENG's repository gone: %s, want the last used, %s", got, ops)
	}
}
