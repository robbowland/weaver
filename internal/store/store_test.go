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
