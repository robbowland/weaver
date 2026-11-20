package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// Events as Claude Code sends them, through the hook: what the state then
// says. me is the agent's pid (CLAUDE_PID).
func hookEnv(t *testing.T) *session.Env {
	t.Helper()
	rextest.NoRex(t)
	dir := t.TempDir()
	return &session.Env{StateDir: dir + "/state", ClaudeDir: dir + "/claude", SockDir: dir + "/sock",
		Session: "s", SKey: "rex-s", Self: "/usr/bin/true"}
}

func hookJSON(t *testing.T, e *session.Env, mode, js string) {
	t.Helper()
	Hook(e, []byte(js), mode, "5", strconv.Itoa(os.Getpid()))
}

// A batch that resolved without the call a dialog waits on (a batch
// before it, landing late) leaves the dialog waiting; its own answers it.
func TestHookBatchAnswersItsDialog(t *testing.T) {
	e := hookEnv(t)
	hookJSON(t, e, "sync", `{"hook_event_name":"SessionStart","session_id":"c","cwd":"/","source":"startup"}`)
	hookJSON(t, e, "sync", `{"hook_event_name":"UserPromptSubmit","session_id":"c","cwd":"/","prompt":"go"}`)
	hookJSON(t, e, "sync", `{"hook_event_name":"PermissionRequest","session_id":"c","cwd":"/","tool_name":"Bash","tool_input":{"command":"rm -rf build"}}`)
	hookJSON(t, e, "async", `{"hook_event_name":"PostToolBatch","session_id":"c","cwd":"/","tool_calls":[{"tool_name":"Read","tool_input":{"file_path":"/a"},"tool_use_id":"t1","tool_response":"x"}]}`)
	if st, _ := store.ReadState(e.StateFile("5")); st.State != "needs-you" {
		t.Errorf("after another batch: %s, want needs-you", st.State)
	}
	hookJSON(t, e, "async", `{"hook_event_name":"PostToolBatch","session_id":"c","cwd":"/","tool_calls":[{"tool_name":"Bash","tool_input":{"command": "rm -rf build"},"tool_use_id":"t2","tool_response":"ok"}]}`)
	if st, _ := store.ReadState(e.StateFile("5")); st.State != "working" {
		t.Errorf("after its own batch: %s, want working", st.State)
	}
}

// After /clear, a late async event of the conversation before (a
// background subagent of it) changes nothing of the new one's state.
func TestHookLateEventOfTheConversationBefore(t *testing.T) {
	e := hookEnv(t)
	hookJSON(t, e, "sync", `{"hook_event_name":"SessionStart","session_id":"old","cwd":"/","source":"startup"}`)
	hookJSON(t, e, "sync", `{"hook_event_name":"UserPromptSubmit","session_id":"old","cwd":"/","prompt":"go"}`)
	hookJSON(t, e, "sync", `{"hook_event_name":"SessionEnd","session_id":"old","cwd":"/","reason":"clear"}`)
	hookJSON(t, e, "sync", `{"hook_event_name":"SessionStart","session_id":"new","cwd":"/","source":"clear"}`)
	hookJSON(t, e, "async", `{"hook_event_name":"PostToolUse","session_id":"old","cwd":"/","tool_name":"Bash","tool_input":{"command":"ls"},"tool_use_id":"t1"}`)
	st, _ := store.ReadState(e.StateFile("5"))
	if st.SessionID != "new" || st.State != "idle" {
		t.Errorf("after a late event of the old conversation: %s on %q, want idle on new", st.State, st.SessionID)
	}
	// The new conversation's own events go on as ever.
	hookJSON(t, e, "sync", `{"hook_event_name":"UserPromptSubmit","session_id":"new","cwd":"/","prompt":"again"}`)
	if st, _ := store.ReadState(e.StateFile("5")); st.State != "working" || st.Turn != 1 {
		t.Errorf("its own prompt: %s turn %d, want working turn 1", st.State, st.Turn)
	}
}

// A hook of an agent whose pane has since started another (wvr restart:
// a block in its place, weaver's number moved to it) writes nothing over
// the new agent's state.
func TestHookOfAReplacedBlock(t *testing.T) {
	e := hookEnv(t)
	rd := e.RexDir()
	for _, d := range []string{rd + "/n", rd + "/u"} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Pane 5 names the new block.
	if err := os.WriteFile(rd+"/n/5", []byte("block:new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REX_BLOCK", "block:new")
	hookJSON(t, e, "sync", `{"hook_event_name":"SessionStart","session_id":"c","cwd":"/","source":"resume"}`)
	before, _ := os.ReadFile(e.StateFile("5"))
	// The old agent's late hook, from its old block.
	t.Setenv("REX_BLOCK", "block:old")
	hookJSON(t, e, "sync", `{"hook_event_name":"UserPromptSubmit","session_id":"old","cwd":"/","prompt":"late"}`)
	if after, _ := os.ReadFile(e.StateFile("5")); string(after) != string(before) {
		t.Errorf("a replaced block's hook wrote over the state:\n%s\nwas\n%s", after, before)
	}
}

// Where an agent works, its root (which its window's label, its review
// panel and its Worktrunk marker follow), as its cwd and edits move it. A
// cd that sticks moves it into another work tree, and back into its own
// checkout; a cd the harness undid (one outside the working directories:
// its CwdChanged's cwd is back where it was) moves nothing; an edit puts
// it in the edited file's work tree, which a cd back about its own
// checkout leaves; the worktree tools always move it, and where it
// starts.
func TestHookRoot(t *testing.T) {
	td, _ := filepath.EvalSymlinks(t.TempDir())
	start, other, wt, notes := td+"/repo", td+"/demo", td+"/repo-wt", td+"/notes"
	for _, d := range []string{start + "/src", other, notes} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"init", "-q", start}, {"-C", start, "commit", "-q", "--allow-empty", "-m", "init"},
		{"init", "-q", other}, {"-C", start, "worktree", "add", "-q", wt},
	} {
		if out, err := exec.Command("git", append([]string{"-c", "user.email=t@e", "-c", "user.name=t"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if err := os.WriteFile(wt+"/a.txt", []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	// Events as Claude Code sends them: a Bash command's PostToolUse, then
	// the CwdChanged of a cd it ran (old_cwd to new_cwd, its cwd where the
	// agent is now).
	bash := func(cwd string) string {
		return fmt.Sprintf(`{"hook_event_name":"PostToolUse","session_id":"c","cwd":%q,"tool_name":"Bash","tool_input":{"command":"ls"},"tool_response":{}}`, cwd)
	}
	cd := func(cwd, from, to string) string {
		return fmt.Sprintf(`{"hook_event_name":"CwdChanged","session_id":"c","cwd":%q,"old_cwd":%q,"new_cwd":%q}`, cwd, from, to)
	}
	edit := func(f string) string {
		return fmt.Sprintf(`{"hook_event_name":"PostToolUse","session_id":"c","cwd":%q,"tool_name":"Edit","tool_input":{"file_path":%q},"tool_response":{}}`, start, f)
	}
	tool := func(name, cwd string) string {
		return fmt.Sprintf(`{"hook_event_name":"PostToolUse","session_id":"c","cwd":%q,"tool_name":%q,"tool_input":{},"tool_response":{}}`, cwd, name)
	}
	for _, c := range []struct {
		name           string
		events         []string
		root, startCwd string
	}{
		{"a cd the harness undid", []string{bash(start), cd(start, start, other)}, start, start},
		{"into another repository", []string{bash(other), cd(other, start, other)}, other, start},
		{"into another repository and back to the start",
			[]string{bash(other), cd(other, start, other), bash(start), cd(start, other, start)}, start, start},
		{"an edit in another worktree", []string{edit(wt + "/a.txt"), bash(start)}, wt, start},
		{"an edit in another worktree, a cd into it and back to the start",
			[]string{edit(wt + "/a.txt"), bash(wt), cd(wt, start, wt), bash(start), cd(start, wt, start)}, wt, start},
		{"into another repository, a directory outside any work tree, and back to the start",
			[]string{bash(other), bash(notes), bash(start)}, start, start},
		{"EnterWorktree", []string{tool("EnterWorktree", wt)}, wt, wt},
		{"EnterWorktree, then ExitWorktree", []string{tool("EnterWorktree", wt), tool("ExitWorktree", start)}, start, start},
		{"a subdirectory of the start worktree", []string{bash(start + "/src"), cd(start+"/src", start, start+"/src")}, start, start},
		{"a subdirectory of the start worktree, after an edit in another",
			[]string{edit(wt + "/a.txt"), bash(start + "/src"), cd(start+"/src", start, start+"/src")}, wt, start},
		{"outside any work tree", []string{bash(notes), cd(notes, start, notes)}, start, start},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := hookEnv(t)
			hookJSON(t, e, "sync", fmt.Sprintf(`{"hook_event_name":"SessionStart","session_id":"c","cwd":%q,"source":"startup"}`, start))
			hookJSON(t, e, "sync", fmt.Sprintf(`{"hook_event_name":"UserPromptSubmit","session_id":"c","cwd":%q,"prompt":"go"}`, start))
			for _, ev := range c.events {
				hookJSON(t, e, "async", ev)
			}
			st, _ := store.ReadState(e.StateFile("5"))
			if st.Root != c.root || st.StartCwd != c.startCwd {
				t.Errorf("root %q, start %q; want %q, %q", st.Root, st.StartCwd, c.root, c.startCwd)
			}
		})
	}
}
