package store

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/robbowland/weaver/internal/agent"
)

// A member of an unexpected type reads as its zero value; the rest reads.
func TestReadStateTolerant(t *testing.T) {
	f := filepath.Join(t.TempDir(), "s.json")
	for _, c := range []struct {
		json   string
		ok     bool
		prompt string
	}{
		{`{"state":"done","turn":"3","prompt":"hi","tasks":{"x":1}}`, true, "hi"},
		{`{"state":"done","prompt":"hi"}` + "\n" + `junk`, true, "hi"},
		{`["state","done"]`, false, ""},
		{`"done"`, false, ""},
	} {
		if err := os.WriteFile(f, []byte(c.json), 0o644); err != nil {
			t.Fatal(err)
		}
		s, err := ReadState(f)
		if (err == nil) != c.ok || s.Prompt != c.prompt {
			t.Errorf("%s: err %v prompt %q, want ok %v prompt %q", c.json, err, s.Prompt, c.ok, c.prompt)
		}
		if c.ok && (s.State != agent.StateDone || s.Turn != 0 || len(s.Tasks) != 0) {
			t.Errorf("%s: %+v", c.json, s)
		}
	}
}

// A thread is about its task; one from an item, told nothing, about the
// item.
func TestThreadAbout(t *testing.T) {
	for _, c := range []struct {
		th   Thread
		want string
	}{
		{Thread{Task: "fix it"}, "fix it"},
		{Thread{Task: "fix it", Item: &ThreadItem{Ref: "o/r#45", Title: "Add a flag"}}, "fix it"},
		{Thread{Item: &ThreadItem{Ref: "o/r#45", Title: "Add a flag"}}, "#45 Add a flag"},
		{Thread{Task: " ", Item: &ThreadItem{Ref: "ENG-42", Title: "Fix it"}}, "ENG-42 Fix it"},
		{Thread{}, ""},
	} {
		if got := c.th.About(); got != c.want {
			t.Errorf("%+v: %q, want %q", c.th, got, c.want)
		}
	}
}

// Changes at once each keep the other's: each reads the record under its
// lock.
func TestUpdate(t *testing.T) {
	f := filepath.Join(t.TempDir(), "cache", "c.json")
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Go(func() {
			if err := Update(f, func(v *[]int) { *v = append(*v, i) }); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	var v []int
	if err := Read(f, &v); err != nil || len(v) != 10 {
		t.Errorf("%v %v: a change lost", v, err)
	}
}

// A thread is called by the name you gave it, else its agent's, else its
// conversation's title; not by the name weaver started that conversation
// with (claude -n), which is only its branch, whatever the branch became.
func TestThreadCalled(t *testing.T) {
	base := Thread{Name: "fix-it", Branch: "fix-it", ConvName: "fix-it"}
	renamed := Thread{Name: "fix/flaky-test", Branch: "fix/flaky-test", ConvName: "fix-it"}
	old := Thread{Name: "fix-it", Branch: "fix-it"} // from before ConvName
	for _, c := range []struct {
		th         Thread
		conv, want string
	}{
		{base, "fix-it", ""},
		{renamed, "fix-it", ""},
		{old, "fix-it", ""},
		{base, "Fix the flaky test", "Fix the flaky test"},
		{Thread{Name: "fix-it", AgentTitle: "Flaky test fix"}, "fix-it", "Flaky test fix"},
		{Thread{Name: "fix-it", AgentTitle: "Flaky test fix"}, "Claude's own", "Flaky test fix"},
		{Thread{Name: "fix-it", Title: "Mine", AgentTitle: "Flaky test fix"}, "Claude's own", "Mine"},
		{Thread{}, "a hand-started agent's title", "a hand-started agent's title"},
	} {
		if got := c.th.Called(c.conv); got != c.want {
			t.Errorf("%+v.Called(%q) = %q, want %q", c.th, c.conv, got, c.want)
		}
	}
}

// A thread's own directory stays its own through what changes its record
// (a rename, a pane), and is another thread's for another worktree or
// another start.
func TestThreadDir(t *testing.T) {
	th := Thread{Created: 100, Worktree: "/w/a", Name: "a"}
	moved := th
	moved.Name, moved.Title, moved.Num = "b", "B", 7
	if th.Dir("/s") != moved.Dir("/s") {
		t.Errorf("renamed: %s, was %s", moved.Dir("/s"), th.Dir("/s"))
	}
	for _, other := range []Thread{{Created: 100, Worktree: "/w/b"}, {Created: 101, Worktree: "/w/a"}} {
		if other.Dir("/s") == th.Dir("/s") {
			t.Errorf("%+v shares %s", other, th.Dir("/s"))
		}
	}
	if d := th.Dir("/s"); filepath.Dir(d) != "/s/threads" {
		t.Errorf("not under /s/threads: %s", d)
	}
}
