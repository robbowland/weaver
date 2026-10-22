package agent

import (
	"encoding/json"
	"strconv"
	"strings"
)

// The agent state machine (docs/design.md, State file): what one hook event
// makes of an agent's stored state. Pure: the hook reads the event and the
// state, works out where the agent is (its cwd, root and turns, which need
// the file system), and writes what this returns.

// Event is a hook event as the state machine needs it.
type Event struct {
	Name   string // hook_event_name
	Async  bool   // run by the async hook (only its PreToolUse means a command)
	Source string // SessionStart's: startup, resume, clear, compact
	// Notification's notification_type.
	Ntype string
	// The tool call's key (its name and input), what a dialog waits on,
	// and the Bash command, with its tool_use_id.
	Key, Waiting, Cmd, ToolUseID string
	// Esc on a tool (is_interrupt); a rejected dialog.
	Interrupt, Rejected bool
	// Stop's background subagents: how many, and what they do.
	Background      int
	BackgroundTasks string
	// What the event carries: your prompt (UserPromptSubmit), the file an
	// edit wrote, its turn's prompt_id, the session's title and transcript,
	// the reply (Stop), why the session ended (SessionEnd).
	Prompt, File, PromptID, Title, Transcript, LastMessage, EndReason string
	Now                                                               int64
}

// Change is the next state, and the bell it rings: 10 needs you, 11
// done, 0 none.
type Change struct {
	Next Record
	Bell int
}

// Transition is what ev makes of old, the stored state (sess: old when it
// is this session's, else empty). Only the members the state machine owns
// are set: state, what it waits on, background work, the command it runs,
// why it ended, since and seen, its last reply, prompt, file, title, turn's
// prompt_id, transcript and Worktrunk marker.
func Transition(old, sess Record, ev Event) Change {
	n := Record{
		Waiting: old.Waiting, Pending: old.Pending, Background: old.Background, BackgroundTasks: old.BackgroundTasks,
		Doing: sess.Doing, DoingID: sess.DoingID, DoneIDs: sess.DoneIDs, State: old.State,
	}
	if n.State == "" {
		n.State = StateWorking
	}
	switch ev.Name {
	case "SessionStart":
		// A compaction is not a new start: the agent carries on as it was.
		if ev.Source == "compact" {
			n.State = old.State
			if n.State == "" {
				n.State = StateIdle
			}
		} else {
			n.State, n.Background, n.BackgroundTasks = StateIdle, 0, ""
		}
	case "UserPromptSubmit":
		n.State, n.Background, n.BackgroundTasks = StateWorking, 0, ""
	case "PreToolUse":
		if ev.Async {
			// A command starting (the async PreToolUse on Bash): what the
			// agent is doing, while its turn goes on, unless it has already
			// finished.
			if (old.State == StateWorking || old.State == StateNeedsYou) && ev.Cmd != "" &&
				(ev.ToolUseID == "" || !strings.Contains(" "+n.DoneIDs+" ", " "+ev.ToolUseID+" ")) {
				n.Doing, n.DoingID = ev.Cmd, ev.ToolUseID
			}
			break
		}
		// (PreToolUse on AskUserQuestion) a question.
		n.State, n.Waiting, n.Pending = StateNeedsYou, ev.Waiting, ev.Key
	case "PermissionRequest":
		// A permission dialog.
		n.State, n.Waiting, n.Pending = StateNeedsYou, ev.Waiting, ev.Key
	case "Notification":
		switch ev.Ntype {
		case "permission_prompt", "elicitation_dialog", "elicitation_url_dialog", "agent_needs_input",
			"worker_permission_prompt", "quota_auto_resume_stale", "":
			// A dialog: keep the more specific text of the request that opened it,
			// and the call it is for when the event says (another agent's).
			n.State = StateNeedsYou
			if old.State != StateNeedsYou || old.Waiting == "" {
				n.Waiting = ev.Waiting
			}
			if ev.Key != "" {
				n.Pending = ev.Key
			}
		case "elicitation_complete", "elicitation_response":
			if old.State == StateNeedsYou {
				n.State = StateWorking
			}
		case "quota_auto_resume_fired":
			n.State = StateWorking
		case "idle_prompt":
			// The reminder 60s after the agent went quiet: still working
			// means Esc stopped it (no Stop fires), unless background work is
			// pending.
			switch old.State {
			case StateWorking:
				if old.Background <= 0 {
					n.State = StateInterrupted
				}
			case "":
				n.State = StateDone
			case StateNeedsYou, StateDone, StateIdle, StateInterrupted, StateEnded, StateStarting:
			}
		}
	case "PostToolUse", "PostToolUseFailure":
		if ev.ToolUseID != "" {
			if ev.ToolUseID == n.DoingID {
				n.Doing, n.DoingID = "", ""
			}
			ids := append(strings.Fields(n.DoneIDs), ev.ToolUseID)
			if len(ids) > 8 {
				ids = ids[len(ids)-8:]
			}
			n.DoneIDs = strings.Join(ids, " ")
		}
		n.State = StateWorking
		if ev.Name == "PostToolUseFailure" && ev.Interrupt {
			n.State = StateInterrupted
		} else if old.State == StateNeedsYou && old.Pending != "" && ev.Key != old.Pending {
			// Another tool finished while one still waits for you.
			n.State = StateNeedsYou
		} else {
			// A late async hook must not undo a Stop or an interrupt: one of
			// the turn that ended (its prompt_id) never does; without a
			// prompt_id, one within 3 s of it.
			switch old.State {
			case StateDone, StateIdle, StateInterrupted:
				recent := false
				if old.Since == "" {
					recent = ev.Now < 3
				} else if s, err := strconv.ParseInt(old.Since.String(), 10, 64); err == nil {
					recent = ev.Now-s < 3
				}
				if ev.PromptID != "" && ev.PromptID == sess.PromptID || recent {
					n.State = old.State
				}
			case StateEnded:
				n.State = StateEnded
			case StateWorking, StateNeedsYou, StateStarting:
			}
		}
	case "PostToolBatch":
		// Every call in the batch has resolved: the dialog has been answered.
		if old.State == StateNeedsYou {
			if ev.Rejected {
				n.State = StateInterrupted
			} else {
				n.State = StateWorking
			}
		}
	case "StopFailure":
		n.State, n.Waiting, n.Pending = StateNeedsYou, ev.Waiting, ""
	case "Stop":
		// Not done while background subagents work on: their reports wake it.
		n.Background, n.BackgroundTasks = ev.Background, ev.BackgroundTasks
		n.State = StateDone
		if n.Background > 0 {
			n.State = StateWorking
		}
	case "SessionEnd":
		n.State, n.Background, n.BackgroundTasks = StateEnded, 0, ""
	}
	// A turn's commands end with it, or with the agent's work.
	if ev.Name == "UserPromptSubmit" || ev.Name == "SessionStart" && ev.Source != "compact" {
		n.Doing, n.DoingID, n.DoneIDs = "", "", ""
	}
	if n.State != StateWorking && n.State != StateNeedsYou {
		n.Doing, n.DoingID = "", ""
	}
	// Why the session ended: /exit, /clear or a logout ends it on purpose; a
	// hangup (its session going) is "other".
	n.EndReason = sess.EndReason
	if ev.Name == "SessionEnd" {
		n.EndReason = ev.EndReason
	}
	if n.State != StateNeedsYou {
		n.Waiting, n.Pending = "", ""
	}
	bell := 0
	if n.State != old.State {
		switch n.State {
		case StateNeedsYou:
			bell = 10
		case StateDone:
			bell = 11
		case StateWorking, StateIdle, StateInterrupted, StateEnded, StateStarting:
		}
	}
	if n.State != old.State || old.Since == "" {
		n.Since, n.Seen = numberOf(ev.Now), false
	} else {
		n.Since, n.Seen = old.Since, old.Seen
	}
	n.LastMessage = old.LastMessage
	if (ev.Name == "Stop" || ev.Name == "StopFailure") && ev.LastMessage != "" {
		n.LastMessage = ev.LastMessage
	}
	n.Prompt = ev.Prompt
	if n.Prompt == "" {
		n.Prompt = old.Prompt
	}
	n.File = ev.File
	if n.File == "" {
		n.File = old.File
	}
	n.Title = ev.Title
	if n.Title == "" {
		n.Title = sess.Title
	}
	n.PromptID = ev.PromptID
	if n.PromptID == "" {
		n.PromptID = sess.PromptID
	}
	n.Transcript = ev.Transcript
	if n.Transcript == "" {
		n.Transcript = sess.Transcript
	}
	// Worktrunk's branch marker: the state's cell, none once it ended.
	if n.State != StateEnded {
		n.Marker = n.State.Cell()
	}
	return Change{n, bell}
}

// numberOf is epoch seconds as a JSON number.
func numberOf(n int64) json.Number { return json.Number(strconv.FormatInt(n, 10)) }
