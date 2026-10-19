package wvr

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/jsonx"
	"github.com/robbowland/weaver/internal/oneline"
	"github.com/robbowland/weaver/internal/store"
)

// Claude Code's conversations, read from their transcripts
// (<claude dir>/projects/<its directory>/<session id>.jsonl), so one weaver
// did not start can be resumed as a thread (`wvr adopt`).

// conversation is a Claude Code conversation as its transcript tells it.
type conversation struct {
	ID, File string
	Start    string // the directory it started in (its first cwd)
	Cwd      string // the directory it was in last
	Branch   string // the git branch it was on last
	Title    string // its title (yours, else Claude Code's), else its first prompt
	Mod      int64  // when it last changed (epoch s)
}

// transcriptLine is what weaver reads of a transcript's line.
type transcriptLine struct {
	Type        string `json:"type"`
	Cwd         string `json:"cwd"`
	GitBranch   string `json:"gitBranch"`
	CustomTitle string `json:"customTitle"`
	AiTitle     string `json:"aiTitle"`
	AgentName   string `json:"agentName"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// prompt is the text of a prompt you typed, "" for any other line (a tool's
// result, a command's output, a reminder Claude Code added).
func (l transcriptLine) prompt() string {
	if l.Type != "user" || l.IsMeta || l.IsSidechain {
		return ""
	}
	var s string
	if json.Unmarshal(l.Message.Content, &s) != nil {
		var blocks []struct{ Type, Text string }
		if json.Unmarshal(l.Message.Content, &blocks) != nil || len(blocks) == 0 || blocks[0].Type != "text" {
			return ""
		}
		s = blocks[0].Text
	}
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "<") {
		return ""
	}
	return s
}

// The ends of a transcript read for what it says of itself: titles and
// directories are rewritten as it goes, so its last lines say where it is
// now; it can be hundreds of megabytes.
const (
	transcriptHead = 64 << 10
	transcriptTail = 256 << 10
)

// readConversation is the conversation in transcript f.
func readConversation(f string) (conversation, error) {
	c := conversation{ID: strings.TrimSuffix(filepath.Base(f), ".jsonl"), File: f}
	fh, err := os.Open(f)
	if err != nil {
		return c, err
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil {
		return c, err
	}
	c.Mod = st.ModTime().Unix()
	head := make([]byte, min(st.Size(), transcriptHead))
	if _, err := io.ReadFull(fh, head); err != nil {
		return c, err
	}
	var tail []byte
	if st.Size() > transcriptHead {
		tail = make([]byte, min(st.Size()-transcriptHead, transcriptTail))
		if _, err := fh.ReadAt(tail, st.Size()-int64(len(tail))); err != nil {
			return c, err
		}
	}
	var title, ai, name, first string
	// A read that begins or ends inside the file cuts a line there: not one.
	read := func(b []byte, cutStart, cutEnd bool) {
		ls := bytes.Split(b, []byte("\n"))
		if cutStart {
			ls = ls[1:]
		}
		if cutEnd {
			ls = ls[:len(ls)-1]
		}
		for _, raw := range ls {
			var l transcriptLine
			if json.Unmarshal(raw, &l) != nil {
				continue
			}
			if l.Cwd != "" {
				c.Start = cmp.Or(c.Start, l.Cwd)
				c.Cwd = l.Cwd
			}
			if l.GitBranch != "" {
				c.Branch = l.GitBranch
			}
			title, ai, name = cmp.Or(l.CustomTitle, title), cmp.Or(l.AiTitle, ai), cmp.Or(l.AgentName, name)
			if first == "" {
				first = l.prompt()
			}
		}
	}
	read(head, false, tail != nil)
	if tail != nil {
		read(tail, true, false)
	}
	c.Title = oneline.Line(cmp.Or(title, ai, name, first))
	return c, nil
}

// isSessionID: is s shaped as Claude Code's session ids are (a UUID)?
func isSessionID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if c != '-' {
				return false
			}
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}

// transcripts are Claude Code's transcripts whose session id starts with
// prefix ("" for all), newest first.
func (e *Env) transcripts(prefix string) []string {
	fs, _ := filepath.Glob(filepath.Join(e.ClaudeDir, "projects", "*", prefix+"*.jsonl"))
	type t struct {
		f   string
		mod time.Time
	}
	var ts []t
	for _, f := range fs {
		if !isSessionID(strings.TrimSuffix(filepath.Base(f), ".jsonl")) {
			continue
		}
		if st, err := os.Stat(f); err == nil {
			ts = append(ts, t{f, st.ModTime()})
		}
	}
	sort.SliceStable(ts, func(i, j int) bool { return ts[i].mod.After(ts[j].mod) })
	out := make([]string, len(ts))
	for i, x := range ts {
		out[i] = x.f
	}
	return out
}

// findConversation is the one conversation whose session id is which, or
// starts with it.
func (e *Env) findConversation(which string) (conversation, error) {
	fs := e.transcripts(which)
	switch {
	case len(fs) == 0:
		return conversation{}, fmt.Errorf("no conversation %s in %s", which, tildify(e.ClaudeDir+"/projects"))
	case len(fs) > 1:
		return conversation{}, fmt.Errorf("%s names %d conversations: give more of its session id", which, len(fs))
	}
	return readConversation(fs[0])
}

// lost is the conversation as a lost thread of session key skey: what a
// thread resumed in its window starts from (threadReopen).
func (c conversation) lost(skey, name, repo string, now int64) store.Lost {
	return store.Lost{Lost: jsonx.Int(now), SKey: skey,
		State: agent.Record{Agent: string(agent.Claude), State: agent.StateIdle, SessionID: c.ID, Transcript: c.File,
			Title: c.Title, Cwd: c.Cwd, Root: c.Cwd, StartCwd: c.Start},
		Thread: store.Thread{Name: name, Repo: repo, Branch: c.Branch, Worktree: c.Start, Here: true,
			Agent: agent.Claude, Created: now, SessionID: c.ID}}
}
