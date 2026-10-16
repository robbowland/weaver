package wvr

// State is what an agent is doing, as far as you are concerned
// (docs/design.md, State file).
type State string

const (
	StateWorking     State = "working"
	StateNeedsYou    State = "needs-you"
	StateDone        State = "done"
	StateIdle        State = "idle"
	StateInterrupted State = "interrupted"
	StateEnded       State = "ended"
	// StateStarting is a thread whose agent has not reported yet: on the
	// board only, never in a state file.
	StateStarting State = "starting"
)

// agentStates are the states a state file can hold, in the order the
// design doc lists them (StateStarting is the board's own).
var agentStates = []State{StateWorking, StateNeedsYou, StateDone, StateIdle, StateInterrupted, StateEnded}

// AgentKind is which coding agent runs in a pane.
type AgentKind string

const (
	AgentClaude AgentKind = "claude"
	AgentCodex  AgentKind = "codex"
)

// parseAgentKind is name as an AgentKind: claude when empty, false when it
// names no agent weaver knows.
func parseAgentKind(name string) (AgentKind, bool) {
	switch k := AgentKind(name); k {
	case "":
		return AgentClaude, true
	case AgentClaude, AgentCodex:
		return k, true
	}
	return "", false
}

// Where is where a thread works: a new worktree, or the checkout you are
// in.
type Where string

const (
	WhereWorktree Where = "worktree"
	WhereHere     Where = "here"
)
