package session

import (
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/process"
)

// Peeked is a state file's state, pid and start, read without parsing it.
type Peeked struct {
	State      agent.State
	Pid, Start string
}

var (
	rePkState = regexp.MustCompile(`"state":"([a-z-]+)"`)
	rePkPid   = regexp.MustCompile(`"pid":([0-9]+)`)
	rePkStart = regexp.MustCompile(`"proc_start":"([^"]*)"`)
)

// Peek is the stored state's state, pid and start, read from its first
// line without parsing it.
func Peek(sf string) Peeked {
	l, _ := files.FirstLine(sf)
	var p Peeked
	if m := rePkState.FindStringSubmatch(l); m != nil {
		p.State = agent.State(m[1])
	}
	if m := rePkPid.FindStringSubmatch(l); m != nil {
		p.Pid = m[1]
	}
	if m := rePkStart.FindStringSubmatch(l); m != nil {
		p.Start = m[1]
	}
	return p
}

// PanelPane is the agent's review panel, while it runs (its record
// without a pid, when it has not recorded itself yet: its pane).
func (e *Env) PanelPane(pane mux.PaneID) mux.PaneID { return e.boundPane(pane, "panel") }

// NvimPane is the agent's nvim pane, when that nvim still runs, as
// `wvr nvim` binds one; the hooks tell it what the agent does.
func (e *Env) NvimPane(pane mux.PaneID) mux.PaneID { return e.boundPane(pane, "nvim") }

// boundPane is the pane bound to the agent in pane in its file of kind
// ext (the pane, then its process's pid), while that process runs.
func (e *Env) boundPane(pane mux.PaneID, ext string) mux.PaneID {
	b, err := os.ReadFile(e.PaneFile(pane, ext))
	if err != nil {
		return ""
	}
	ls := strings.SplitN(string(b), "\n", 3)
	nv, pid := ls[0], ""
	if len(ls) > 1 {
		pid = ls[1]
	}
	if !digits(nv) && nv != "" {
		return ""
	}
	if pid != "" && !digits(pid) {
		pid = ""
	}
	if pid != "" {
		if n, _ := strconv.Atoi(pid); !process.Alive(n) {
			return ""
		}
	}
	return mux.PaneID(nv)
}
