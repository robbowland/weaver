package session

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/tmux"
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

// NvimPane is the agent's nvim pane, when that nvim still runs.
func (e *Env) NvimPane(pane tmux.PaneID) tmux.PaneID {
	b, err := os.ReadFile(e.PaneFile(pane, "nvim"))
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
		if n, _ := strconv.Atoi(pid); syscall.Kill(n, 0) != nil {
			return ""
		}
	}
	return tmux.PaneID(nv)
}

// RenamePane titles a pane (the nvim beside an agent), bounded.
func (e *Env) RenamePane(p tmux.PaneID, title string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	args := []string{"select-pane", "-t", p.Target(), "-T", title}
	if !e.InTmux {
		args = append(append([]string{}, e.TSock...), args...)
	}
	exec.CommandContext(ctx, "tmux", args...).Run()
}
