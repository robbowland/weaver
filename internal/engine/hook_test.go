package engine

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strconv"
	"testing"

	"github.com/robbowland/weaver/internal/hooks"
	"github.com/robbowland/weaver/internal/rex/rextest"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
)

// An agent's pid of 1 or less (launchd, the parent of an orphaned hook; 0,
// no process) is no agent: never recorded, where it would read as dead and
// have the board retire a running agent. A real one is.
func TestHookRecordsNoUnknownPid(t *testing.T) {
	rextest.NoRex(t)
	me := strconv.Itoa(os.Getpid())
	for _, c := range []struct{ pid, want string }{{"1", ""}, {"0", ""}, {me, me}} {
		dir := t.TempDir()
		e := &session.Env{StateDir: dir + "/state", ClaudeDir: dir + "/claude", SockDir: dir + "/sock",
			Session: "s", SKey: "rex-s", Self: "/usr/bin/true"}
		hook(e, hooks.Input{Event: "SessionStart", SessionID: "conv", Cwd: dir, Source: "startup"}, "sync", "5", "claude", c.pid)
		st, err := store.ReadState(e.StateFile("5"))
		if err != nil {
			t.Fatalf("pid %s: no state: %v", c.pid, err)
		}
		if string(st.Pid) != c.want {
			t.Errorf("pid %s: recorded %q, want %q", c.pid, st.Pid, c.want)
		}
	}
}

// What a sync hook (Codex's, which wait) hands its background run reaches
// it: every exported field, the stored state's lists left behind, and so
// the files a Bash call says it changed, which the background run follows.
// What stays unexported is the foreground's alone: one more is a choice to
// make here.
func TestBackgroundRunGetsWhatItFollows(t *testing.T) {
	resp, _ := json.Marshal(map[string]any{"bashEditDiff": map[string]any{"changedFiles": []string{"/w/a.go", "/w/b.go"}}})
	h := &hk{Pane: "5", Session: "s", Mode: "sync"}
	h.Old.Files = []string{"/w/old.go"}
	if !h.parse(hooks.Input{Event: "PostToolUse", SessionID: "conv", Cwd: "/w", ToolName: "Bash", ToolResponse: resp}, nil) {
		t.Fatal("not parsed")
	}
	h.FollowBash = "1"
	b, err := bgRun(h)
	if err != nil {
		t.Fatal(err)
	}
	got := &hk{}
	if err := json.Unmarshal(b, got); err != nil {
		t.Fatal(err)
	}
	if !got.ChangedKnown || !slices.Equal(got.Changed, []string{"/w/a.go", "/w/b.go"}) || got.FollowBash != "1" {
		t.Errorf("the files the call changed: %v %q, follow %q", got.ChangedKnown, got.Changed, got.FollowBash)
	}
	if got.Old.Files != nil {
		t.Errorf("the stored state's lists went too: %q", got.Old.Files)
	}
	// Not followed, or more than the environment should carry: not handed
	// on, and the background run asks git.
	for _, c := range []struct {
		follow string
		n      int
	}{{"", 2}, {"1", maxChangedHandedOn + 1}} {
		x := *h
		x.FollowBash, x.Changed = c.follow, make([]string, c.n)
		b, err := bgRun(&x)
		var y hk
		if err != nil || json.Unmarshal(b, &y) != nil || y.ChangedKnown || y.Changed != nil {
			t.Errorf("follow %q, %d files: handed on %v %d", c.follow, c.n, y.ChangedKnown, len(y.Changed))
		}
	}
	want := map[string]bool{"sf": true, "rc": true}
	rt := reflect.TypeOf(hk{})
	gv, hv := reflect.ValueOf(got).Elem(), reflect.ValueOf(h).Elem()
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		switch {
		case !f.IsExported():
			if !want[f.Name] {
				t.Errorf("%s is unexported, so the background run never has it", f.Name)
			}
		case f.Tag.Get("json") == "-", f.Name == "Old":
		case !reflect.DeepEqual(gv.Field(i).Interface(), hv.Field(i).Interface()):
			t.Errorf("%s: %v reached the background run as %v", f.Name, hv.Field(i).Interface(), gv.Field(i).Interface())
		}
	}
}
