package session

import (
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/cli"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/process"
)

// Peeked is a state file's state, pid, start and time, read without
// parsing it.
type Peeked struct {
	State      agent.State
	Pid, Start string
	TS         int64
}

var (
	rePkState = regexp.MustCompile(`"state":"([a-z-]+)"`)
	rePkPid   = regexp.MustCompile(`"pid":([0-9]+)`)
	rePkStart = regexp.MustCompile(`"proc_start":"([^"]*)"`)
	rePkTs    = regexp.MustCompile(`"ts":([0-9]+)`)
)

// Peek is the stored state's state, pid, start and time, read from its
// first line without parsing it: what a hook and a sweep need of every
// state file, fast.
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
	if m := rePkTs.FindStringSubmatch(l); m != nil {
		p.TS, _ = strconv.ParseInt(m[1], 10, 64)
	}
	return p
}

// PanelPane is the agent's review panel, while it runs (its record
// without a pid, when it has not recorded itself yet: its pane).
func (e *Env) PanelPane(pane mux.PaneID) mux.PaneID {
	b, err := os.ReadFile(e.PaneFile(pane, "panel"))
	if err != nil {
		return ""
	}
	ls := strings.SplitN(string(b), "\n", 3)
	nv, pid := ls[0], ""
	if len(ls) > 1 {
		pid = ls[1]
	}
	if !cli.Digits(nv) && nv != "" {
		return ""
	}
	if pid != "" && !cli.Digits(pid) {
		pid = ""
	}
	if pid != "" {
		if n, _ := strconv.Atoi(pid); !process.Alive(n) {
			return ""
		}
	}
	return mux.PaneID(nv)
}
