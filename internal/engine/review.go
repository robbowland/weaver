package engine

import (
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
	"github.com/robbowland/weaver/internal/tuicr"
)

// Notes is what a prompt of yours carries to a thread's agent
// (reviewNotes): Text, and the comments in it to record as given once it
// has reached the agent (Given, which reply calls when the hook's output
// is written): output lost, they come again with the next prompt.
type Notes struct {
	Text     string
	given    []tuicr.Comment
	stateDir string
	th       store.Thread
}

// Given records the comments n carries as given to the thread's agent.
func (n Notes) Given() {
	bestEffort(tuicr.ThreadGive(n.stateDir, n.th, n.given)) // unrecorded, they come again: better twice than never
}

// reviewNotes is what a prompt of yours carries to a thread's agent: the
// review comments you wrote in tuicr on the thread's worktree, or on root,
// the one its agent works in now (its review panel's), that it has not been
// given yet (tuicr.Context: as many as fit, the rest with the next prompt).
// None for none, and for an agent that is no thread's: a helper beside it
// works for the thread's agent, not for you.
func reviewNotes(e *session.Env, pane mux.PaneID, root string) Notes {
	th, err := store.ReadThread(e.ThreadFile(pane))
	if err != nil {
		return Notes{}
	}
	cs := tuicr.ThreadUnread(e.StateDir, th, root)
	s, n := tuicr.Context(cs, th.Worktree)
	if n == 0 {
		return Notes{}
	}
	return Notes{Text: s, given: cs[:n], stateDir: e.StateDir, th: th}
}
