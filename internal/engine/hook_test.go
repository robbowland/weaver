package engine

import (
	"encoding/json"
	"os"
	"reflect"
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
		hook(e, hooks.Input{Event: "SessionStart", SessionID: "conv", Cwd: dir, Source: "startup"}, "sync", "5", c.pid)
		st, err := store.ReadState(e.StateFile("5"))
		if err != nil {
			t.Fatalf("pid %s: no state: %v", c.pid, err)
		}
		if string(st.Pid) != c.want {
			t.Errorf("pid %s: recorded %q, want %q", c.pid, st.Pid, c.want)
		}
	}
}

// What a sync hook hands its background run reaches it: every exported
// field, the stored state's lists left behind. What stays unexported is
// the foreground's alone: one more is a choice to make here.
func TestBackgroundRunGetsWhatItNeeds(t *testing.T) {
	h := &hk{Pane: "5", Session: "s", Mode: "sync"}
	h.Old.Files = []string{"/w/old.go"}
	if !h.parse(hooks.Input{Event: "PostToolUse", SessionID: "conv", Cwd: "/w", ToolName: "Edit", ToolInput: []byte(`{"file_path":"/w/a.go"}`)}, nil) {
		t.Fatal("not parsed")
	}
	b, err := bgRun(h)
	if err != nil {
		t.Fatal(err)
	}
	got := &hk{}
	if err := json.Unmarshal(b, got); err != nil {
		t.Fatal(err)
	}
	if got.Old.Files != nil {
		t.Errorf("the stored state's lists went too: %q", got.Old.Files)
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
