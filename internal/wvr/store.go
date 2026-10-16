package wvr

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"

	"github.com/robbowland/weaver/internal/jsonx"
)

// The records weaver keeps under $STATE_DIR (docs/design.md, Files), as
// types: read whole, written whole, atomically.

// AgentState is an agent's state file (docs/design.md, State file), as the
// hook writes it, in this order.
type AgentState struct {
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

// Thread is a thread's record (docs/design.md, Thread record): what `wvr
// new` made, and where; its agent's pane and conversation once it has them.
type Thread struct {
	Num             int         `json:"num"`
	Cmd             string      `json:"cmd"`
	Name            string      `json:"name"`
	Task            string      `json:"task"`
	Repo            string      `json:"repo"`
	Branch          string      `json:"branch"`
	Worktree        string      `json:"worktree"`
	Here            bool        `json:"here"`
	WorktreeCreated bool        `json:"worktree_created"`
	Agent           AgentKind   `json:"agent"`
	Created         int64       `json:"created"`
	Base            string      `json:"base"`
	Item            *ThreadItem `json:"item,omitempty"`
	Mode            string      `json:"mode,omitempty"`
	Pane            *jsonx.Int  `json:"pane,omitempty"`
	SessionID       string      `json:"session_id,omitempty"`
}

// ThreadItem is the work item a thread started from.
type ThreadItem struct {
	Kind  string `json:"kind"`
	Ref   string `json:"ref"`
	URL   string `json:"url"`
	Title string `json:"title"`
}

// Lost is a thread lost with its session (docs/design.md, Lost threads):
// when, which session, and its agent's state and thread record as they
// were.
type Lost struct {
	Lost   jsonx.Int  `json:"lost"`
	SKey   string     `json:"skey"`
	State  AgentState `json:"state"`
	Thread Thread     `json:"thread"`
	// Session is older records' name for SKey.
	Session string `json:"session,omitempty"`
}

// Key is the session key the thread was lost from.
func (l Lost) Key() string {
	if l.SKey != "" {
		return l.SKey
	}
	return l.Session
}

// Dir is where it resumes: where its agent started, else its worktree,
// else where its agent last was.
func (l Lost) Dir() string {
	for _, d := range []string{l.State.StartCwd, l.Thread.Worktree, l.State.Root, l.State.Cwd} {
		if d != "" {
			return d
		}
	}
	return ""
}

// Task is what it was asked: its thread's task, else its agent's prompt.
func (l Lost) Task() string {
	if l.Thread.Task != "" {
		return l.Thread.Task
	}
	return clean(l.State.Prompt)
}

func readLost(f string) (Lost, error) {
	var l Lost
	err := readRecord(f, &l)
	return l, err
}

// readRecord reads the record in file f into v.
func readRecord(f string, v any) error {
	b, err := os.ReadFile(f)
	if err != nil {
		return err
	}
	return jsonx.Decode(jsonx.First(b), v)
}

// readThread is the thread record in file f.
func readThread(f string) (Thread, error) {
	var t Thread
	err := readRecord(f, &t)
	return t, err
}

// writeRecord writes v to file f as one line of JSON, whole or not at all:
// a temporary file beside it, renamed over it.
func writeRecord(f string, v any) error {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	return writeAtomic(f, b.Bytes())
}

// writeAtomic writes b to file f through a temporary file beside it, so a
// reader sees the old content or the new, never half.
func writeAtomic(f string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		return err
	}
	tmp := f + "." + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, f); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// threadFile is the record of the thread whose agent is in pane p.
func (e *Env) threadFile(p PaneID) string {
	return e.paneFile(p, "thread")
}

// bestEffort marks an error left unhandled on purpose: what failed (a
// marker, a cache, a view started early) costs nothing that matters.
func bestEffort(error) {}

// readState is the agent state in file sf.
func readState(sf string) (AgentState, error) {
	var st AgentState
	err := readRecord(sf, &st)
	return st, err
}

// updateState changes the agent state in file sf under its lock: read,
// changed, written back whole (the rename releases the lock). False when
// there was none to change.
func updateState(sf string, change func(*AgentState)) bool {
	lk := lock(sf)
	st, err := readState(sf)
	if err != nil {
		os.Remove(lk)
		return false
	}
	change(&st)
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if enc.Encode(st) != nil || os.WriteFile(lk, b.Bytes(), 0o644) != nil {
		os.Remove(lk)
		return false
	}
	return os.Rename(lk, sf) == nil
}
