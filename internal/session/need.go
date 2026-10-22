package session

import (
	"cmp"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/tmux"
)

// Need is what an agent asks of you, from its row: the board's groups, in
// this order, and what the status line counts (all but NeedNothing). Every
// view of an agent takes it from here, so none says otherwise.
type Need int

const (
	// NeedYou: it waits on you (a dialog, a question, an error).
	NeedYou Need = iota
	// NeedLook: it is done, and you have not looked yet.
	NeedLook
	// NeedWait: it works, or starts (its agent has not reported yet).
	NeedWait
	// NeedNothing: idle, seen, interrupted, exited, or not reporting.
	NeedNothing
)

// startWait is how long a thread's agent has (s) to report before it
// counts as not reporting: its hooks not in place (Codex runs them only
// once trusted in /hooks).
const startWait = 120

// Need is what row r asks of you at now (epoch s).
func (r Row) Need(now int64) Need {
	if r.Exited {
		return NeedNothing
	}
	switch r.State {
	case agent.StateNeedsYou:
		return NeedYou
	case agent.StateDone:
		if r.Seen {
			return NeedNothing
		}
		return NeedLook
	case agent.StateWorking:
		return NeedWait
	case agent.StateStarting:
		if now-r.Since < startWait {
			return NeedWait
		}
	case agent.StateIdle, agent.StateInterrupted, agent.StateEnded:
	}
	return NeedNothing
}

// MidTurn: is row r's agent under way at now, so that quitting or
// restarting it would cut a turn short (or leave you a dialog)?
func (r Row) MidTurn(now int64) bool {
	n := r.Need(now)
	return n == NeedYou || n == NeedWait
}

// NotReporting: has row r's thread waited on its agent's first report
// longer than an agent takes?
func (r Row) NotReporting(now int64) bool {
	return !r.Exited && r.State == agent.StateStarting && now-r.Since >= startWait
}

// RowOf is pane p's agent as the board shows it, with when that was;
// false when the session has none there.
func (e *Env) RowOf(p tmux.PaneID) (Row, int64, bool) {
	rows, now := e.AgentRows()
	for _, r := range rows {
		if r.Pane == p {
			return r, now, true
		}
	}
	return Row{}, now, false
}

// Called is what row r's agent is called everywhere it is shown: the name
// you gave its thread, else its agent's title, else what its thread is
// about (its task, or its item), else its prompt.
func (r Row) Called() string {
	return cmp.Or(r.Title, r.Ttask, r.Prompt, "new "+cmp.Or(r.Agent, string(agent.Claude))+" session")
}

// Called is what lost thread l is called, as it was when live: the name you
// gave it, else its agent's title, else its task, else its branch.
func (l LostRow) Called() string { return cmp.Or(l.Title, l.Task, l.Name) }
