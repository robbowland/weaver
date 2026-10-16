package hooks

import (
	"testing"

	"github.com/robbowland/weaver/internal/jsonx"
)

// What the hook reads of its input.
func TestHookInput(t *testing.T) {
	var in Input
	if err := jsonx.Decode([]byte(`{"hook_event_name":"PostToolUse","cwd":"/r","tool_name":"Edit",
		"tool_input":{"file_path":"a/b.go"},"error":{"code":1},"task_id":7,"is_interrupt":"yes"}`), &in); err != nil {
		t.Fatal(err)
	}
	if got := in.EditFile(); got != "/r/a/b.go" {
		t.Errorf("editFile: %q", got)
	}
	if in.Error != `{"code":1}` || in.TaskID != "7" || in.Interrupt {
		t.Errorf("error %q task_id %q interrupt %v", in.Error, in.TaskID, in.Interrupt)
	}
	for _, c := range []struct{ json, want string }{
		{`{"tool_name":"Bash","tool_input":{"command":"rm -rf x"}}`, "Bash: rm -rf x"},
		{`{"tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Which?"}]}}`, "question: Which?"},
		{`{"tool_name":"NotebookEdit","tool_input":{"notebook_path":"/n/x.ipynb"}}`, "NotebookEdit: x.ipynb"},
		{`{"tool_name":"WebFetch","tool_input":{"url":"u"}}`, "WebFetch"},
	} {
		var in Input
		_ = jsonx.Decode([]byte(c.json), &in)
		if got := in.Summary(); got != c.want {
			t.Errorf("summary %s: %q, want %q", c.json, got, c.want)
		}
	}
	var batch Input
	_ = jsonx.Decode([]byte(`{"hook_event_name":"PostToolBatch","tool_calls":[{"tool_response":{"text":"The user doesn't want to proceed with this tool use."}}]}`), &batch)
	if !batch.Rejected() {
		t.Error("a rejected call in the batch")
	}
}

// The lists the hook keeps: tasks by id, subagents by id, files last.
func TestHookLists(t *testing.T) {
	ev := func(name, extra string) Input {
		var in Input
		_ = jsonx.Decode([]byte(`{"hook_event_name":"`+name+`"`+extra+`}`), &in)
		return in
	}
	ts := Tasks(nil, ev("TaskCreated", `,"task_id":"1","task_subject":"one"`))
	ts = Tasks(ts, ev("TaskCreated", `,"task_id":"2","task_subject":"two"`))
	ts = Tasks(ts, ev("TaskCompleted", `,"task_id":"1"`))
	if len(ts) != 2 || !ts[0].Done || ts[1].Done || ts[0].Subject != "one" {
		t.Errorf("tasks: %+v", ts)
	}
	if ts := Tasks(ts, ev("SessionStart", `,"source":"resume"`)); len(ts) != 2 {
		t.Errorf("a resume keeps the tasks: %+v", ts)
	}
	if ts := Tasks(ts, ev("SessionStart", `,"source":"startup"`)); ts == nil || len(ts) != 0 {
		t.Errorf("a new start clears them: %+v", ts)
	}
	ss := Subagents(nil, ev("SubagentStart", `,"agent_type":"Explore"`), "a1", 5)
	ss = Subagents(ss, ev("SubagentStart", `,"agent_type":"Plan"`), "a2", 6)
	ss = Subagents(ss, ev("SubagentStop", ""), "a1", 7)
	if len(ss) != 1 || ss[0].ID != "a2" || ss[0].Type != "Plan" || ss[0].Since != "6" {
		t.Errorf("subagents: %+v", ss)
	}
	fs := Files(Files(Files(nil, "/a"), "/b"), "/a")
	if len(fs) != 2 || fs[0] != "/b" || fs[1] != "/a" {
		t.Errorf("files: %v", fs)
	}
}
