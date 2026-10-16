package wvr

import (
	"strings"
	"testing"
)

// The state machine, event by event: the next state, what it waits on,
// the bell it rings, and since/seen.
func TestTransition(t *testing.T) {
	const now = 1000
	st := func(s State, f ...func(*AgentState)) AgentState {
		a := AgentState{State: s, Since: "900", Seen: true}
		for _, x := range f {
			x(&a)
		}
		return a
	}
	waiting := func(w, p string) func(*AgentState) {
		return func(a *AgentState) { a.Waiting, a.Pending = w, p }
	}
	for _, c := range []struct {
		name      string
		old       AgentState
		sameSess  bool
		ev        hookEvent
		state     State
		bell      int
		waiting   string
		keepSince bool
	}{
		{"a prompt: working", st(StateIdle), true, hookEvent{Name: "UserPromptSubmit", Prompt: "go"}, StateWorking, 0, "", false},
		{"a dialog: needs you, and rings", st(StateWorking), true,
			hookEvent{Name: "PermissionRequest", Key: "Bash:{}", Waiting: "Bash: rm"}, StateNeedsYou, 10, "Bash: rm", false},
		{"a question (sync PreToolUse)", st(StateWorking), true,
			hookEvent{Name: "PreToolUse", Key: "AskUserQuestion:{}", Waiting: "question: which?"}, StateNeedsYou, 10, "question: which?", false},
		{"a dialog's notification keeps the request's text", st(StateNeedsYou, waiting("Bash: rm", "k")), true,
			hookEvent{Name: "Notification", Ntype: "permission_prompt", Waiting: "Claude needs your permission"}, StateNeedsYou, 0, "Bash: rm", true},
		{"stop: done, and rings", st(StateWorking), true, hookEvent{Name: "Stop", LastMessage: "Done."}, StateDone, 11, "", false},
		{"stop with background subagents: still working", st(StateWorking), true,
			hookEvent{Name: "Stop", Background: 2, BackgroundTasks: "a; b"}, StateWorking, 0, "", true},
		{"stop failing: needs you", st(StateWorking), true, hookEvent{Name: "StopFailure", Waiting: "error: overloaded"}, StateNeedsYou, 10, "error: overloaded", false},
		{"quiet a minute while working: Esc stopped it", st(StateWorking), true,
			hookEvent{Name: "Notification", Ntype: "idle_prompt"}, StateInterrupted, 0, "", false},
		{"...unless background work is pending", st(StateWorking, func(a *AgentState) { a.Background = 1 }), true,
			hookEvent{Name: "Notification", Ntype: "idle_prompt"}, StateWorking, 0, "", true},
		{"...and with no state yet: done", AgentState{}, false, hookEvent{Name: "Notification", Ntype: "idle_prompt"}, StateDone, 11, "", false},
		{"another tool finishes while one waits on you", st(StateNeedsYou, waiting("Bash: rm", "Bash:rm")), true,
			hookEvent{Name: "PostToolUse", Key: "Read:x"}, StateNeedsYou, 0, "Bash: rm", true},
		{"the dialog's own tool finishes: working", st(StateNeedsYou, waiting("Bash: rm", "Bash:rm")), true,
			hookEvent{Name: "PostToolUse", Key: "Bash:rm"}, StateWorking, 0, "", false},
		{"Esc on a tool: interrupted", st(StateWorking), true,
			hookEvent{Name: "PostToolUseFailure", Interrupt: true}, StateInterrupted, 0, "", false},
		{"a late tool event of the turn that ended: still done", st(StateDone, func(a *AgentState) { a.PromptID = "p1" }), true,
			hookEvent{Name: "PostToolUse", PromptID: "p1", Now: now}, StateDone, 0, "", true},
		{"...or within 3 s of the stop", st(StateDone, func(a *AgentState) { a.Since = "999" }), true,
			hookEvent{Name: "PostToolUse", Now: now}, StateDone, 0, "", true},
		{"a tool event of a new turn: working", st(StateDone, func(a *AgentState) { a.PromptID = "p1" }), true,
			hookEvent{Name: "PostToolUse", PromptID: "p2", Now: now}, StateWorking, 0, "", false},
		{"the batch answered", st(StateNeedsYou, waiting("w", "k")), true, hookEvent{Name: "PostToolBatch"}, StateWorking, 0, "", false},
		{"the batch rejected", st(StateNeedsYou, waiting("w", "k")), true, hookEvent{Name: "PostToolBatch", Rejected: true}, StateInterrupted, 0, "", false},
		{"compaction carries on as it was", st(StateWorking), true, hookEvent{Name: "SessionStart", Source: "compact"}, StateWorking, 0, "", true},
		{"a new start: idle", st(StateDone), true, hookEvent{Name: "SessionStart", Source: "startup"}, StateIdle, 0, "", false},
		{"the end", st(StateIdle), true, hookEvent{Name: "SessionEnd", EndReason: "clear"}, StateEnded, 0, "", false},
	} {
		sess := AgentState{}
		if c.sameSess {
			sess = c.old
		}
		if c.ev.Now == 0 {
			c.ev.Now = now
		}
		got := transition(c.old, sess, c.ev)
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

// What the agent is doing: a command from the async PreToolUse, until its
// PostToolUse; never one that already finished; never once it stopped.
func TestTransitionDoing(t *testing.T) {
	working := AgentState{State: StateWorking, Since: "900"}
	pre := func(old AgentState, id string) AgentState {
		return transition(old, old, hookEvent{Name: "PreToolUse", Async: true, Cmd: "npm test", ToolUseID: id, Now: 1000}).Next
	}
	n := pre(working, "t1")
	if n.Doing != "npm test" || n.DoingID != "t1" || n.State != StateWorking {
		t.Fatalf("a command starting: %+v", n)
	}
	n = transition(n, n, hookEvent{Name: "PostToolUse", ToolUseID: "t1", Now: 1001}).Next
	if n.Doing != "" || n.DoneIDs != "t1" {
		t.Errorf("its PostToolUse: doing %q done %q", n.Doing, n.DoneIDs)
	}
	if late := pre(n, "t1"); late.Doing != "" {
		t.Errorf("a PreToolUse landing after its PostToolUse: doing %q", late.Doing)
	}
	if idle := pre(AgentState{State: StateIdle}, "t2"); idle.Doing != "" {
		t.Errorf("an idle agent: doing %q", idle.Doing)
	}
	for i := 0; i < 10; i++ {
		n = transition(n, n, hookEvent{Name: "PostToolUse", ToolUseID: "x" + string(rune('a'+i)), Now: 1002}).Next
	}
	if ids := strings.Fields(n.DoneIDs); len(ids) != 8 || ids[7] != "xj" {
		t.Errorf("done_ids keeps the last 8: %q", n.DoneIDs)
	}
	n = pre(working, "t3")
	if done := transition(n, n, hookEvent{Name: "Stop", Now: 1003}).Next; done.Doing != "" {
		t.Errorf("stopped, still doing %q", done.Doing)
	}
}
