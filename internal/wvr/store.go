package wvr

import "github.com/robbowland/weaver/internal/tmux"

// threadFile is the record of the thread whose agent is in pane p.
func (e *Env) threadFile(p tmux.PaneID) string {
	return e.paneFile(p, "thread")
}

// bestEffort marks an error left unhandled on purpose: what failed (a
// marker, a cache, a view started early) costs nothing that matters.
func bestEffort(error) {}
