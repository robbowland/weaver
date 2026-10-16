package session

import "github.com/robbowland/weaver/internal/tmux"

// ThreadFile is the record of the thread whose agent is in pane p.
func (e *Env) ThreadFile(p tmux.PaneID) string {
	return e.PaneFile(p, "thread")
}

// bestEffort marks an error left unhandled on purpose: what failed (a
// marker, a cache, a view started early) costs nothing that matters.
func bestEffort(error) {}
