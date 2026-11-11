// Package agent is what weaver knows of a coding agent: the states it can
// be in, the record of its state file, and how each of its hook events
// changes that record.
package agent

// The cells of the states (State.Cell).
const (
	CellNeedsYou    = "[!]"
	CellDone        = "[x]"
	CellWorking     = "[/]"
	CellIdle        = "[ ]"
	CellInterrupted = "[-]"
	CellEnded       = "[-]"
)

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

// States are the states a state file can hold, in the order the
// design doc lists them (StateStarting is the board's own).
var States = []State{StateWorking, StateNeedsYou, StateDone, StateIdle, StateInterrupted, StateEnded}

// Claude is the agent weaver runs and follows, as its state names it.
const Claude = "claude"

// Cell is the state's cell: a mark between brackets, as the board, its
// counts and Worktrunk's branch markers show it.
func (s State) Cell() string {
	switch s {
	case StateNeedsYou:
		return CellNeedsYou
	case StateDone:
		return CellDone
	case StateWorking, StateStarting:
		return CellWorking
	case StateInterrupted:
		return CellInterrupted
	case StateEnded:
		return CellEnded
	case StateIdle:
		return CellIdle
	}
	return CellIdle
}
