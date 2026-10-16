package agent

import (
	"encoding/json"

	"github.com/robbowland/weaver/internal/jsonx"
)

// Record is an agent's state file (docs/design.md, State file), as the
// hook writes it, in this order.
type Record struct {
	Pane            jsonx.Int    `json:"pane"`
	Agent           string       `json:"agent"`
	State           State        `json:"state"`
	TS              int64        `json:"ts"`
	Since           json.Number  `json:"since"`
	Seen            bool         `json:"seen"`
	SessionID       string       `json:"session_id"`
	Pid             json.Number  `json:"pid,omitempty"`
	ProcStart       string       `json:"proc_start"`
	Title           string       `json:"title"`
	Turn            int          `json:"turn"`
	TurnEnded       int          `json:"turn_ended"`
	PromptID        string       `json:"prompt_id"`
	Transcript      string       `json:"transcript"`
	Cwd             string       `json:"cwd"`
	Root            string       `json:"root"`
	StartCwd        string       `json:"start_cwd"`
	ProjectRoot     string       `json:"project_root"`
	File            string       `json:"file"`
	Prompt          string       `json:"prompt"`
	Waiting         string       `json:"waiting"`
	Pending         string       `json:"pending"`
	LastMessage     string       `json:"last_message"`
	MarkedRoot      string       `json:"marked_root"`
	Marker          string       `json:"marker"`
	Background      int          `json:"background"`
	BackgroundTasks string       `json:"background_tasks"`
	Doing           string       `json:"doing,omitempty"`
	DoingID         string       `json:"doing_id,omitempty"`
	DoneIDs         string       `json:"done_ids,omitempty"`
	NvimPane        *json.Number `json:"nvim_pane"`
	Server          int          `json:"server,omitempty"`
	EndReason       string       `json:"end_reason,omitempty"`
	Log             string       `json:"log,omitempty"`
	Subagents       []Subagent   `json:"subagents"`
	Tasks           []Task       `json:"tasks"`
	TurnRoots       []string     `json:"turn_roots"`
	Files           []string     `json:"files"`
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
