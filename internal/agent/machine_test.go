package agent

import (
	"strings"
	"testing"
)

// The state machine, event by event: the next state, what it waits on,
// the bell it rings, and since/seen.
func TestTransition(t *testing.T) {
	const now = 1000
	st := func(s State, f ...func(*Record)) Record {
		a := Record{State: s, Since: "900", Seen: true}
		for _, x := range f {
			x(&a)
		}
		return a
	}
	waiting := func(w, p string) func(*Record) {
		return func(a *Record) { a.Waiting, a.Pending = w, p }
	}
	for _, c := range []struct {
		name      string
		old       Record
		sameSess  bool
		ev        Event
		state     State
		bell      int
		waiting   string
		keepSince bool
	}{
		{"a prompt: working", st(StateIdle), true, Event{Name: "UserPromptSubmit", Prompt: "go"}, StateWorking, 0, "", false},
		{"a dialog: needs you, and rings", st(StateWorking), true,
			Event{Name: "PermissionRequest", Key: "Bash:{}", Waiting: "Bash: rm"}, StateNeedsYou, 10, "Bash: rm", false},
		{"a question (sync PreToolUse)", st(StateWorking), true,
			Event{Name: "PreToolUse", Key: "AskUserQuestion:{}", Waiting: "question: which?"}, StateNeedsYou, 10, "question: which?", false},
		{"a dialog's notification keeps the request's text", st(StateNeedsYou, waiting("Bash: rm", "k")), true,
			Event{Name: "Notification", Ntype: "permission_prompt", Waiting: "Claude needs your permission"}, StateNeedsYou, 0, "Bash: rm", true},
		{"another agent's dialog, naming its call: needs you", st(StateWorking), true,
			Event{Name: "Notification", Ntype: "permission_prompt", Waiting: "shell: git push", Key: "shell:push"}, StateNeedsYou, 10, "shell: git push", false},
		{"stop: done, and rings", st(StateWorking), true, Event{Name: "Stop", LastMessage: "Done."}, StateDone, 11, "", false},
		{"stop with background subagents: still working", st(StateWorking), true,
			Event{Name: "Stop", Background: 2, BackgroundTasks: "a; b"}, StateWorking, 0, "", true},
		{"stop failing: needs you", st(StateWorking), true, Event{Name: "StopFailure", Waiting: "error: overloaded"}, StateNeedsYou, 10, "error: overloaded", false},
		{"...and no late tool of its turn ends that", Transition(st(StateWorking), st(StateWorking), Event{Name: "StopFailure", Waiting: "error: overloaded", Now: 990}).Next, true,
			Event{Name: "PostToolUse", Key: "Read:x"}, StateNeedsYou, 0, "error: overloaded", true},
		{"quiet a minute while working: Esc stopped it", st(StateWorking), true,
			Event{Name: "Notification", Ntype: "idle_prompt"}, StateInterrupted, 0, "", false},
		{"...unless background work is pending", st(StateWorking, func(a *Record) { a.Background = 1 }), true,
			Event{Name: "Notification", Ntype: "idle_prompt"}, StateWorking, 0, "", true},
		{"...and with no state yet: done", Record{}, false, Event{Name: "Notification", Ntype: "idle_prompt"}, StateDone, 11, "", false},
		{"another tool finishes while one waits on you", st(StateNeedsYou, waiting("Bash: rm", "Bash:rm")), true,
			Event{Name: "PostToolUse", Key: "Read:x"}, StateNeedsYou, 0, "Bash: rm", true},
		{"a tool finishes while an MCP server's dialog waits: still needs you", st(StateNeedsYou, waiting("input wanted", "")), true,
			Event{Name: "PostToolUse", Key: "Read:x"}, StateNeedsYou, 0, "input wanted", true},
		{"...its answer: working", st(StateNeedsYou, waiting("input wanted", "")), true,
			Event{Name: "Notification", Ntype: "elicitation_response"}, StateWorking, 0, "", false},
		{"the dialog's own tool finishes: working", st(StateNeedsYou, waiting("Bash: rm", "Bash:rm")), true,
			Event{Name: "PostToolUse", Key: "Bash:rm"}, StateWorking, 0, "", false},
		{"Esc on a tool: interrupted", st(StateWorking), true,
			Event{Name: "PostToolUseFailure", Interrupt: true}, StateInterrupted, 0, "", false},
		{"a late tool event of the turn that ended: still done", st(StateDone, func(a *Record) { a.PromptID = "p1"; a.Since = "995" }), true,
			Event{Name: "PostToolUse", PromptID: "p1", Now: now}, StateDone, 0, "", true},
		{"...but long after it ended, the turn carries on (a Stop hook blocked its end)", st(StateDone, func(a *Record) { a.PromptID = "p1" }), true,
			Event{Name: "PostToolUse", PromptID: "p1", Now: now}, StateWorking, 0, "", false},
		{"...or within 3 s of the stop", st(StateDone, func(a *Record) { a.Since = "999" }), true,
			Event{Name: "PostToolUse", Now: now}, StateDone, 0, "", true},
		{"a tool event of a new turn: working", st(StateDone, func(a *Record) { a.PromptID = "p1" }), true,
			Event{Name: "PostToolUse", PromptID: "p2", Now: now}, StateWorking, 0, "", false},
		{"the batch answered", st(StateNeedsYou, waiting("w", "k")), true, Event{Name: "PostToolBatch"}, StateWorking, 0, "", false},
		{"the batch with the dialog's call: answered", st(StateNeedsYou, waiting("Bash: rm", "Bash:rm")), true,
			Event{Name: "PostToolBatch", Batch: []string{"Read:x", "Bash:rm"}}, StateWorking, 0, "", false},
		{"a batch without the dialog's call (one before it, late; another agent's): still needs you", st(StateNeedsYou, waiting("Bash: rm", "Bash:rm")), true,
			Event{Name: "PostToolBatch", Batch: []string{"Read:x"}}, StateNeedsYou, 0, "Bash: rm", true},
		{"an API error's wait outlasts a late tool of its turn", st(StateNeedsYou, waiting("error: overloaded", PendingStopFailure)), true,
			Event{Name: "PostToolUse", Key: "Read:x"}, StateNeedsYou, 0, "error: overloaded", true},
		{"...and a late batch of it", st(StateNeedsYou, waiting("error: overloaded", PendingStopFailure)), true,
			Event{Name: "PostToolBatch", Batch: []string{"Read:x"}}, StateNeedsYou, 0, "error: overloaded", true},
		{"...till you prompt again", st(StateNeedsYou, waiting("error: overloaded", PendingStopFailure)), true,
			Event{Name: "UserPromptSubmit", Prompt: "retry"}, StateWorking, 0, "", false},
		{"a late interrupt of the turn before does not stop the next", st(StateWorking, func(a *Record) { a.PromptID = "p2" }), true,
			Event{Name: "PostToolUseFailure", Interrupt: true, PromptID: "p1"}, StateWorking, 0, "", true},
		{"the batch rejected", st(StateNeedsYou, waiting("w", "k")), true, Event{Name: "PostToolBatch", Rejected: true}, StateInterrupted, 0, "", false},
		{"compaction carries on as it was", st(StateWorking), true, Event{Name: "SessionStart", Source: "compact"}, StateWorking, 0, "", true},
		{"a new start: idle", st(StateDone), true, Event{Name: "SessionStart", Source: "startup"}, StateIdle, 0, "", false},
		{"the end", st(StateIdle), true, Event{Name: "SessionEnd", EndReason: "clear"}, StateEnded, 0, "", false},
	} {
		sess := Record{}
		if c.sameSess {
			sess = c.old
		}
		if c.ev.Now == 0 {
			c.ev.Now = now
		}
		got := Transition(c.old, sess, c.ev)
		n := got.Next
		if n.State != c.state || got.Bell != c.bell || n.Waiting != c.waiting {
			t.Errorf("%s: got %s bell %d waiting %q, want %s bell %d waiting %q", c.name, n.State, got.Bell, n.Waiting, c.state, c.bell, c.waiting)
			continue
		}
		if kept := n.Since == c.old.Since && n.Since != ""; kept != c.keepSince {
			t.Errorf("%s: since %q (was %q), kept %v, want kept %v", c.name, n.Since, c.old.Since, kept, c.keepSince)
		}
		if !c.keepSince && (n.Since != "1000" || n.Seen) {
			t.Errorf("%s: a new state starts now, unseen: since %q seen %v", c.name, n.Since, n.Seen)
		}
		if (n.State == StateEnded) != (n.Marker == "") {
			t.Errorf("%s: marker %q for %s", c.name, n.Marker, n.State)
		}
	}
}

// A turn's prompt_id is its prompt's: a late event of the turn before
// never takes it back.
func TestTransitionPromptID(t *testing.T) {
	n := Transition(Record{State: StateWorking, PromptID: "p1"}, Record{State: StateWorking, PromptID: "p1"},
		Event{Name: "UserPromptSubmit", Prompt: "next", PromptID: "p2", Now: 1000}).Next
	if n.PromptID != "p2" {
		t.Fatalf("a prompt's prompt_id: %q", n.PromptID)
	}
	if late := Transition(n, n, Event{Name: "PostToolUse", PromptID: "p1", Now: 1001}).Next; late.PromptID != "p2" {
		t.Errorf("a late tool event of the turn before: prompt_id %q, want p2", late.PromptID)
	}
	if first := Transition(Record{}, Record{}, Event{Name: "PostToolUse", PromptID: "p9", Now: 1001}).Next; first.PromptID != "p9" {
		t.Errorf("none known: the event's, %q", first.PromptID)
	}
}

// What the agent is doing: a command from the async PreToolUse, until its
// PostToolUse; never one that already finished; never once it stopped.
func TestTransitionDoing(t *testing.T) {
	working := Record{State: StateWorking, Since: "900"}
	pre := func(old Record, id string) Record {
		return Transition(old, old, Event{Name: "PreToolUse", Async: true, Cmd: "npm test", ToolUseID: id, Now: 1000}).Next
	}
	n := pre(working, "t1")
	if n.Doing != "npm test" || n.DoingID != "t1" || n.State != StateWorking {
		t.Fatalf("a command starting: %+v", n)
	}
	n = Transition(n, n, Event{Name: "PostToolUse", ToolUseID: "t1", Now: 1001}).Next
	if n.Doing != "" || n.DoneIDs != "t1" {
		t.Errorf("its PostToolUse: doing %q done %q", n.Doing, n.DoneIDs)
	}
	if late := pre(n, "t1"); late.Doing != "" {
		t.Errorf("a PreToolUse landing after its PostToolUse: doing %q", late.Doing)
	}
	if idle := pre(Record{State: StateIdle}, "t2"); idle.Doing != "" {
		t.Errorf("an idle agent: doing %q", idle.Doing)
	}
	for i := 0; i < 10; i++ {
		n = Transition(n, n, Event{Name: "PostToolUse", ToolUseID: "x" + string(rune('a'+i)), Now: 1002}).Next
	}
	if ids := strings.Fields(n.DoneIDs); len(ids) != 8 || ids[7] != "xj" {
		t.Errorf("done_ids keeps the last 8: %q", n.DoneIDs)
	}
	n = pre(working, "t3")
	if done := Transition(n, n, Event{Name: "Stop", Now: 1003}).Next; done.Doing != "" {
		t.Errorf("stopped, still doing %q", done.Doing)
	}
}
