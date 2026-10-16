package wvr

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The records weaver keeps under $STATE_DIR (docs/design.md, Files), as
// types: read whole, written whole, atomically.

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
	Agent           string      `json:"agent"`
	Created         int64       `json:"created"`
	Base            string      `json:"base"`
	Item            *ThreadItem `json:"item,omitempty"`
	Mode            string      `json:"mode,omitempty"`
	Pane            *jsonInt    `json:"pane,omitempty"`
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
	Lost   jsonInt         `json:"lost"`
	SKey   string          `json:"skey"`
	State  json.RawMessage `json:"state"`
	Thread Thread          `json:"thread"`
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

// AgentState is its agent's state as it was, as an object.
func (l Lost) AgentState() obj {
	o, _ := readObj(l.State)
	return o
}

// Dir is where it resumes: where its agent started, else its worktree,
// else where its agent last was.
func (l Lost) Dir() string {
	s := l.AgentState()
	for _, v := range []any{s["start_cwd"], l.Thread.Worktree, s["root"], s["cwd"]} {
		if present(v) {
			return tostring(v)
		}
	}
	return ""
}

// Task is what it was asked: its thread's task, else its agent's prompt.
func (l Lost) Task() string {
	if l.Thread.Task != "" {
		return l.Thread.Task
	}
	return str(l.AgentState().get("prompt", ""))
}

func readLost(f string) (Lost, error) {
	var l Lost
	err := readRecord(f, &l)
	return l, err
}

// jsonInt is a number that older files may hold as a string ("12"), or
// not at all.
type jsonInt int

func (n *jsonInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return err
	}
	*n = jsonInt(f)
	return nil
}

// readRecord reads the record in file f into v.
func readRecord(f string, v any) error {
	b, err := os.ReadFile(f)
	if err != nil {
		return err
	}
	return json.Unmarshal(firstJSON(b), v)
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
func (e *Env) threadFile(p string) string {
	return e.StateDir + "/" + e.SKey + "_" + p + ".thread"
}
