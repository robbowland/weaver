package engine

import (
	"errors"
	"io"
	"os"
	"strings"

	"github.com/robbowland/weaver/internal/cli"
	"github.com/robbowland/weaver/internal/hooks"
	"github.com/robbowland/weaver/internal/jsonx"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/session"
)

// `wvr event` takes one event of any agent as normalized JSON (docs/
// design.md, Events) and turns it into the Claude Code hook input the state
// machine understands.

// Event is `wvr event [--bell]`.
func Event(e *session.Env, bell bool) error {
	if cli.IsTTY(0) {
		return errors.New("usage: wvr event [--bell] < event.json")
	}
	in, _ := io.ReadAll(os.Stdin)
	var n hooks.Normalized
	if jsonx.Decode(jsonx.First(in), &n) != nil {
		return nil
	}
	strip := func(t jsonx.Text) string {
		return strings.Map(func(r rune) rune {
			if r < 0x20 {
				return -1
			}
			return r
		}, string(t))
	}
	agent, pid, pane := strip(n.Agent), strip(n.Pid), strip(n.Pane)
	// An agent's name goes into the state file and nvim's events as it is.
	if agent == "" || agent[0] < 'a' || agent[0] > 'z' || strings.Trim(agent, "abcdefghijklmnopqrstuvwxyz0123456789_-") != "" {
		return nil
	}
	if p, ok := mux.ParsePane(pane); ok {
		pane = string(p)
	} else {
		pane = ""
	}
	pid = knownPid(pid)
	if pane == "" {
		pane = string(e.MyPane)
	}
	h := n.Hook()
	if h.Event == "" {
		return nil
	}
	// Where its bell rings: the terminal it runs in.
	tty := "/dev/tty"
	if pane == "" || e.Session == "" {
		return nil
	}
	// Other agents' tool and subagent events are taken as Claude Code's
	// async ones.
	mode := "sync"
	if h.Event == "PostToolUse" || strings.HasPrefix(h.Event, "Subagent") {
		mode = "async"
	}
	if pid == "" {
		pid = os.Getenv("WEAVER_AGENT_PID")
	}
	rc := hook(e, h, mode, mux.PaneID(pane), agent, pid)
	if bell {
		bl := os.Getenv("WEAVER_BELL")
		if bl == "" {
			bl = "1"
		}
		if bl == "1" && (rc == 10 || rc == 11) || bl == "needs-you" && rc == 10 {
			if t, err := os.OpenFile(tty, os.O_WRONLY, 0); err == nil {
				_, err := t.WriteString("\a")
				bestEffort(errors.Join(err, t.Close())) // a bell missed
			}
		}
	}
	return nil
}
