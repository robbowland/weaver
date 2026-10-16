// Package store is the records weaver keeps under $STATE_DIR
// (docs/design.md, Files), as types: read whole, written whole, atomically,
// and a state file changed under its lock.
package store

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/jsonx"
	"github.com/robbowland/weaver/internal/oneline"
)

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
	Agent           agent.Kind  `json:"agent"`
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
	Lost   jsonx.Int    `json:"lost"`
	SKey   string       `json:"skey"`
	State  agent.Record `json:"state"`
	Thread Thread       `json:"thread"`
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
	return oneline.Line(l.State.Prompt)
}

func ReadLost(f string) (Lost, error) {
	var l Lost
	err := Read(f, &l)
	return l, err
}

// Read reads the record in file f into v.
func Read(f string, v any) error {
	b, err := os.ReadFile(f)
	if err != nil {
		return err
	}
	return jsonx.Decode(jsonx.First(b), v)
}

// ReadThread is the thread record in file f.
func ReadThread(f string) (Thread, error) {
	var t Thread
	err := Read(f, &t)
	return t, err
}

// Write writes v to file f as one line of JSON, whole or not at all:
// a temporary file beside it, renamed over it.
func Write(f string, v any) error {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	return WriteAtomic(f, b.Bytes())
}

// WriteAtomic writes b to file f through a temporary file beside it, so a
// reader sees the old content or the new, never half.
func WriteAtomic(f string, b []byte) error {
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

// ReadState is the agent state in file sf.
func ReadState(sf string) (agent.Record, error) {
	var st agent.Record
	err := Read(sf, &st)
	return st, err
}

// UpdateState changes the agent state in file sf under its lock: read,
// changed, written back whole (the rename releases the lock). False when
// there was none to change.
func UpdateState(sf string, change func(*agent.Record)) bool {
	lk := Lock(sf)
	st, err := ReadState(sf)
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

// Lock serialises read-modify-write of state file sf:
// sf.lock created exclusively holding our pid; a lock whose owner is gone is
// removed; after ~2s, a private file instead. Its path.
func Lock(sf string) string {
	l := sf + ".lock"
	me := strconv.Itoa(os.Getpid())
	for i := 1; ; i++ {
		fh, err := os.OpenFile(l, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			// Without its owner, others wait the lock out.
			_, _ = fh.WriteString(me + "\n")
			_ = fh.Close()
			return l
		}
		owner, _ := firstLine(l)
		if n, err := strconv.Atoi(owner); err == nil && n > 0 {
			if syscall.Kill(n, 0) != nil {
				if again, _ := firstLine(l); again == owner {
					os.Remove(l)
				}
				continue
			}
		}
		if i >= 100 {
			return sf + "." + me + ".tmp"
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// firstLine is the first line of file f.
func firstLine(f string) (string, error) {
	b, err := os.ReadFile(f)
	if err != nil {
		return "", err
	}
	if i := strings.IndexByte(string(b), '\n'); i >= 0 {
		b = b[:i]
	}
	return string(b), nil
}
