// Package store is the records weaver keeps under $STATE_DIR
// (docs/design.md, Files), as types: read whole, written whole, atomically,
// and a state file changed under its lock.
package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/files"
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
	Mode            agent.Mode  `json:"mode,omitempty"`
	Pane            *jsonx.Int  `json:"pane,omitempty"`
	SessionID       string      `json:"session_id,omitempty"`
	// Title is the name you gave it (wvr new --name, the form's name,
	// wvr rename), shown in place of the title its agent gives it.
	Title string `json:"title,omitempty"`
}

// About is what the thread is about, for its label: its task, else (one
// from an item, told nothing) the item, as "#45 Add a flag" or "ENG-42 Fix
// it".
func (t Thread) About() string {
	if strings.TrimSpace(t.Task) != "" || t.Item == nil {
		return t.Task
	}
	return t.Item.Name()
}

// ThreadItem is the work item a thread started from.
type ThreadItem struct {
	Kind  string `json:"kind"`
	Ref   string `json:"ref"`
	URL   string `json:"url"`
	Title string `json:"title"`
}

// Name is the item as a thread's name: "#45 Add a flag", "ENG-42 Fix it".
func (it ThreadItem) Name() string {
	ref := it.Ref
	if i := strings.LastIndexByte(ref, '#'); i >= 0 {
		ref = ref[i:]
	}
	return strings.TrimSpace(ref + " " + it.Title)
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
// changed, written back whole. False when there was none to change.
func UpdateState(sf string, change func(*agent.Record)) bool {
	defer Lock(sf)()
	st, err := ReadState(sf)
	if err != nil {
		return false
	}
	change(&st)
	return Write(sf, st) == nil
}

// UpdateThread changes the thread record in file f under its lock, as
// UpdateState does a state.
func UpdateThread(f string, change func(*Thread)) bool {
	defer Lock(f)()
	th, err := ReadThread(f)
	if err != nil {
		return false
	}
	change(&th)
	return Write(f, th) == nil
}

// Update changes the record in file f under its lock: read (the zero
// record when there is none, or none that reads), changed, written back
// whole. Two changes at once each keep the other's.
func Update[T any](f string, change func(*T)) error {
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		return err
	}
	defer Lock(f)()
	var v T
	if Read(f, &v) != nil {
		v = *new(T)
	}
	change(&v)
	return Write(f, v)
}

// lockStale is how old a lock is that no holder can still mean: one whose
// pid a process started since has taken.
const lockStale = 10 * time.Second

// Lock serialises read-modify-write of file f: f.lock, created
// exclusively, holding our pid. A lock whose holder is gone, or older than
// any holder holds one, is taken over (renamed away first, so of two that
// would take it one does); after ~2s of a live holder, none (on we go).
// What it returns releases it: removes the lock if it is still ours.
func Lock(f string) (release func()) {
	l := f + ".lock"
	me := strconv.Itoa(os.Getpid())
	for i := 1; ; i++ {
		fh, err := os.OpenFile(l, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			_, err = fh.WriteString(me + "\n")
			if errors.Join(err, fh.Close()) == nil {
				return func() {
					if owner, _ := files.FirstLine(l); owner == me {
						os.Remove(l)
					}
				}
			}
			os.Remove(l)
		}
		if st, err := os.Stat(l); err == nil {
			owner, _ := files.FirstLine(l)
			n, perr := strconv.Atoi(owner)
			if perr != nil || n <= 0 || syscall.Kill(n, 0) != nil || time.Since(st.ModTime()) > lockStale {
				// Not a writer's yet (half made), or its holder is gone.
				if perr != nil && time.Since(st.ModTime()) < time.Second {
					time.Sleep(20 * time.Millisecond)
					continue
				}
				away := l + "." + me
				if os.Rename(l, away) == nil {
					if again, _ := files.FirstLine(away); again != owner {
						// Not the one we judged: put it back, unless one is
						// there already.
						_ = os.Link(away, l)
					}
					os.Remove(away)
				}
				continue
			}
		}
		if i >= 100 {
			return func() {}
		}
		time.Sleep(20 * time.Millisecond)
	}
}
