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

// Kind is which coding agent runs in a pane.
type Kind string

const (
	Claude Kind = "claude"
	Codex  Kind = "codex"
)

// ParseKind is name as a Kind: claude when empty, false when it
// names no agent weaver knows.
func ParseKind(name string) (Kind, bool) {
	switch k := Kind(name); k {
	case "":
		return Claude, true
	case Claude, Codex:
		return k, true
	}
	return "", false
}

// Cell is the state's cell: a mark between brackets, as the board, the
// status line, Neovim and Worktrunk's branch markers show it.
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
