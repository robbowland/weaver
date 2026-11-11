package agent

import (
	"encoding/json"
	"strconv"

	"github.com/robbowland/weaver/internal/jsonx"
)

// Record is an agent's state file (docs/design.md, State file), as the
// hook writes it, in this order.
type Record struct {
	Pane            jsonx.Int   `json:"pane"`
	Agent           string      `json:"agent"`
	State           State       `json:"state"`
	TS              int64       `json:"ts"`
	Since           json.Number `json:"since"`
	Seen            bool        `json:"seen"`
	SessionID       string      `json:"session_id"`
	Pid             json.Number `json:"pid,omitempty"`
	ProcStart       string      `json:"proc_start"`
	Title           string      `json:"title"`
	Turn            int         `json:"turn"`
	PromptID        string      `json:"prompt_id"`
	Transcript      string      `json:"transcript"`
	Cwd             string      `json:"cwd"`
	Root            string      `json:"root"`
	StartCwd        string      `json:"start_cwd"`
	ProjectRoot     string      `json:"project_root"`
	File            string      `json:"file"`
	Prompt          string      `json:"prompt"`
	Waiting         string      `json:"waiting"`
	Pending         string      `json:"pending"`
	LastMessage     string      `json:"last_message"`
	MarkedRoot      string      `json:"marked_root"`
	Marker          string      `json:"marker"`
	Background      int         `json:"background"`
	BackgroundTasks string      `json:"background_tasks"`
	Doing           string      `json:"doing,omitempty"`
	DoingID         string      `json:"doing_id,omitempty"`
	DoneIDs         string      `json:"done_ids,omitempty"`
	Where
	EndReason string     `json:"end_reason,omitempty"`
	Subagents []Subagent `json:"subagents"`
	Tasks     []Task     `json:"tasks"`
	Files     []string   `json:"files"`
}

// Task is one of an agent's tasks (TaskCreated, TaskCompleted).
type Task struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`
	Done    bool   `json:"done"`
}

// Subagent is a subagent an agent runs now.
type Subagent struct {
	ID    string      `json:"id"`
	Type  string      `json:"type"`
	Since json.Number `json:"since"`
}

// Where is the life of the Rex session an agent runs in: its server's pid
// and the session's id. A session made again, on another server or on the
// same one, is another life.
type Where struct {
	Server  int    `json:"server,omitempty"`
	Session string `json:"rex_session,omitempty"`
}

// Resumed is r as its conversation carries on in another process, in the
// session life w, from now: what is the conversation's (its session,
// turns, title, where it works, its tasks and files) kept, what was its
// process's and pane's gone; idle, and seen.
func (r Record) Resumed(now int64, w Where) Record {
	return Record{Agent: r.Agent, State: StateIdle, TS: now, Since: json.Number(strconv.FormatInt(now, 10)), Seen: true,
		SessionID: r.SessionID, Title: r.Title, Turn: r.Turn, PromptID: r.PromptID,
		Transcript: r.Transcript, Cwd: r.Cwd, Root: r.Root, StartCwd: r.StartCwd, ProjectRoot: r.ProjectRoot,
		File: r.File, Prompt: r.Prompt, LastMessage: r.LastMessage, Where: w,
		Subagents: []Subagent{}, Tasks: r.Tasks, Files: r.Files}
}
