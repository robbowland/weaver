package engine

import (
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
	"github.com/robbowland/weaver/internal/tuicr"
)

// reviewNotes is what a prompt of yours carries to a thread's agent: the
// review comments you wrote in tuicr on the thread's worktree, or on root,
// the one its agent works in now (its review panel's), that it has not been
// given yet, recorded as given as they go (tuicr.Context: as many as fit,
// the rest with the next prompt). "" for none, and for an agent that is no
// thread's: a helper beside it works for the thread's agent, not for you.
func reviewNotes(e *session.Env, pane mux.PaneID, root string) string {
	th, err := store.ReadThread(e.ThreadFile(pane))
	if err != nil {
		return ""
	}
	cs := tuicr.ThreadUnread(e.StateDir, th, root)
	s, n := tuicr.Context(cs, th.Worktree)
	if n == 0 {
		return ""
	}
	// Unrecorded, they come again with the next prompt: better twice
	// than never.
	bestEffort(tuicr.Give(tuicr.GivenFile(e.StateDir, th), cs[:n]))
	return s
}
