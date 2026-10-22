package agent

import (
	"encoding/json"
	"strconv"
	"strings"

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
	Where
	EndReason string     `json:"end_reason,omitempty"`
	Log       string     `json:"log,omitempty"`
	Subagents []Subagent `json:"subagents"`
	Tasks     []Task     `json:"tasks"`
	TurnRoots []string   `json:"turn_roots"`
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

// Where is the life of the tmux session an agent runs in: its server's pid
// and the session's id on that server ("$3", as tmux writes it; from
// $TMUX's last two fields). A session made again, on another server or on
// the same one, is another life.
type Where struct {
	Server  int    `json:"server,omitempty"`
	Session string `json:"tmux_session,omitempty"`
}

// ParseWhere is $TMUX ("socket,pid,session") as a Where: zero outside tmux.
func ParseWhere(env string) Where {
	f := strings.Split(env, ",")
	if len(f) != 3 {
		return Where{}
	}
	srv, err1 := strconv.Atoi(f[1])
	ses, err2 := strconv.Atoi(f[2])
	if err1 != nil || err2 != nil || srv <= 0 || ses < 0 {
		return Where{}
	}
	return Where{Server: srv, Session: "$" + f[2]}
}

// Resumed is r as its conversation carries on in another process, in the
// tmux session life w, from now: what is the conversation's (its session,
// turns, title, where it works, its tasks and files) kept, what was its
// process's and pane's gone; idle, and seen.
func (r Record) Resumed(now int64, w Where) Record {
	return Record{Agent: r.Agent, State: StateIdle, TS: now, Since: json.Number(strconv.FormatInt(now, 10)), Seen: true,
		SessionID: r.SessionID, Title: r.Title, Turn: r.Turn, TurnEnded: r.TurnEnded, PromptID: r.PromptID,
		Transcript: r.Transcript, Cwd: r.Cwd, Root: r.Root, StartCwd: r.StartCwd, ProjectRoot: r.ProjectRoot,
		File: r.File, Prompt: r.Prompt, LastMessage: r.LastMessage, Log: r.Log, Where: w,
		Subagents: []Subagent{}, Tasks: r.Tasks, TurnRoots: r.TurnRoots, Files: r.Files}
}
