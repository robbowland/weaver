package session

import (
	"os"
	"strconv"
	"strings"

	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/store"
	"github.com/robbowland/weaver/internal/tmux"
)

// ThreadFile is the record of the thread whose agent is in pane p.
func (e *Env) ThreadFile(p tmux.PaneID) string {
	return e.PaneFile(p, "thread")
}

// LoseStaleThread keeps pane p's thread record as lost when it was written
// before this session was made: an earlier server's thread, whose agent
// never reported, that a new pane of its number p is not.
func (e *Env) LoseStaleThread(p tmux.PaneID) {
	f := e.ThreadFile(p)
	fi, err := os.Stat(f)
	if err != nil || e.Session == "" {
		return
	}
	out, err := e.Tmux("display-message", "-p", "-t", "="+e.Session+":", "#{session_created}")
	made, _ := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err == nil && made > fi.ModTime().Unix() {
		e.LoseThread(f)
	}
}

// ThreadLearns records conversation sid in pane p's thread record, when it
// has one that does not know it yet (Codex says its own at its first event;
// Claude Code's changes with /clear): so it can be resumed whether or not
// its agent's state survives.
func (e *Env) ThreadLearns(p tmux.PaneID, sid string) {
	f := e.ThreadFile(p)
	if sid == "" || !files.Exists(f) {
		return
	}
	if th, err := store.ReadThread(f); err != nil || th.SessionID == sid {
		return
	}
	store.UpdateThread(f, func(th *store.Thread) { th.SessionID = sid })
}

// bestEffort marks an error left unhandled on purpose: what failed (a
// marker, a cache, a view started early) costs nothing that matters.
func bestEffort(error) {}
