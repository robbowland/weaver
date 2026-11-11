package engine

import (
	"errors"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/robbowland/weaver/internal/cli"
	"github.com/robbowland/weaver/internal/hooks"
	"github.com/robbowland/weaver/internal/jsonx"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/session"
	"golang.org/x/sys/unix"
)

// `wvr event` takes one event of any agent as normalized JSON (docs/
// design.md, Events) and turns it into the Claude Code hook input the state
// machine understands; `wvr adapt codex` translates Codex's own hook
// input into such an event first.

// Event is `wvr event [--bell]` and `wvr adapt codex [--bell]`.
func Event(e *session.Env, adapter string, bell bool) error {
	if cli.IsTTY(0) {
		return errors.New("usage: wvr event [--bell] < event.json")
	}
	in, _ := io.ReadAll(os.Stdin)
	var n hooks.Normalized
	if adapter == "codex" {
		var c hooks.CodexInput
		if jsonx.Decode(jsonx.First(in), &c) != nil {
			return nil
		}
		n = c.Normalized()
	} else if jsonx.Decode(jsonx.First(in), &n) != nil {
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
	// Codex runs every session's hooks in its shared app server, with the
	// environment of whichever session started it, not the hook's own
	// pane's: weaver cannot place them in a Rex block yet, so it drops them
	// (and opens no Codex thread: followable).
	if adapter == "codex" {
		return nil
	}
	if pane == "" || e.Session == "" {
		return nil
	}
	// How the agent runs its hook: Codex runs every one in turn, waiting
	// (config/codex-hooks.json); other agents' tool and subagent events
	// are taken as Claude Code's async ones.
	mode := "sync"
	if adapter != "codex" && (h.Event == "PostToolUse" || strings.HasPrefix(h.Event, "Subagent")) {
		mode = "async"
	}
	if pid == "" {
		pid = os.Getenv("WEAVER_AGENT_PID")
	}
	// The agent process, once per session: the nearest ancestor that is
	// not a shell (an agent may run its hooks through one).
	if pid == "" && adapter != "" && h.Event == "SessionStart" {
		pid = agentAncestor()
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

// agentAncestor is the nearest ancestor of this process that is not a
// shell; "" when there is none short of launchd (an orphaned hook's).
func agentAncestor() string {
	p := os.Getppid()
	for i := 0; i < 4 && p > 1; i++ {
		k, err := unix.SysctlKinfoProc("kern.proc.pid", p)
		if err != nil {
			return ""
		}
		comm := unix.ByteSliceToString(k.Proc.P_comm[:])
		switch comm {
		case "sh", "bash", "zsh", "dash", "fish", "-sh", "-bash", "-zsh", "-fish", "env":
			p = int(k.Eproc.Ppid)
		default:
			return strconv.Itoa(p)
		}
	}
	return ""
}
