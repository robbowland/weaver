package session

import (
	"os"
	"strconv"
	"strings"

	"github.com/robbowland/weaver/internal/tmux"
)

// ThreadFile is the record of the thread whose agent is in pane p.
func (e *Env) ThreadFile(p tmux.PaneID) string {
	return e.PaneFile(p, "thread")
}

// DropStaleThread removes pane p's thread record when it was written
// before this session was made: an earlier server's, of the session's
// name, whose pane of that number p is not.
func (e *Env) DropStaleThread(p tmux.PaneID) {
	f := e.ThreadFile(p)
	fi, err := os.Stat(f)
	if err != nil || e.Session == "" {
		return
	}
	out, err := e.Tmux("display-message", "-p", "-t", "="+e.Session+":", "#{session_created}")
	made, _ := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err == nil && made > fi.ModTime().Unix() {
		os.Remove(f)
	}
}

// bestEffort marks an error left unhandled on purpose: what failed (a
// marker, a cache, a view started early) costs nothing that matters.
func bestEffort(error) {}
