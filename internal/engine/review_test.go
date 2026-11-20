package engine

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/store"
	"github.com/robbowland/weaver/internal/tuicr"
)

// tuicrComment saves one review comment of yours on checkout dir, where
// tuicr 0.28 keeps it.
func tuicrComment(t *testing.T, dir, id, path, line, text string) {
	t.Helper()
	r := tuicr.Dir()
	if err := os.MkdirAll(r+"/sessions", 0o755); err != nil {
		t.Fatal(err)
	}
	canon, _ := filepath.EvalSymlinks(dir)
	c := map[string]any{"id": id, "content": text, "comment_type": "none", "created_at": time.Now().UTC().Format(time.RFC3339Nano),
		"side": "new", "line_range": nil, "author": "user", "lifecycle_state": "local_draft"}
	s := map[string]any{"id": "s", "version": "1.3", "repo_path": dir, "review_comments": []any{},
		"files": map[string]any{path: map[string]any{"path": path, "file_comments": []any{}, "line_comments": map[string]any{line: []any{c}}}}}
	b, _ := json.MarshalIndent(s, "", "  ")
	if err := os.WriteFile(r+"/sessions/"+id+".json", b, 0o644); err != nil {
		t.Fatal(err)
	}
	ix := map[string]any{}
	_ = store.Read(r+"/index.json", &ix)
	es, _ := ix["entries"].(map[string]any)
	if es == nil {
		es = map[string]any{}
	}
	es["o/r@b/"+id] = []any{map[string]any{"path": "sessions/" + id + ".json", "kind": map[string]any{"type": "local"},
		"canonical_repo_path": canon, "display": map[string]any{"comment_count": 1}}}
	b, _ = json.Marshal(map[string]any{"version": "2.0", "entries": es})
	if err := os.WriteFile(r+"/index.json", b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// A prompt of yours carries your review comments its thread's agent has
// not been given, once each; another agent's message, and a helper's
// prompt (a pane with no thread), carry none. A prompt whose hook output
// was not written gave none: they come with the next.
func TestHookPromptCarriesReviewComments(t *testing.T) {
	e := hookEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", home+"/data")
	t.Setenv("XDG_CONFIG_HOME", home+"/config")
	wt := t.TempDir()
	if err := os.MkdirAll(e.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	th := store.Thread{Name: "b", Worktree: wt, Created: time.Now().Add(-time.Minute).Unix()}
	if err := store.Write(e.ThreadFile("5"), th); err != nil {
		t.Fatal(err)
	}
	me := strconv.Itoa(os.Getpid())
	prompt := func(pane, text string) string {
		js, _ := json.Marshal(map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": "c", "cwd": wt, "prompt": text})
		pid := me
		if pane != "5" {
			pid = ""
		}
		rc, notes := Hook(e, js, "sync", mux.PaneID(pane), pid)
		var out bytes.Buffer
		if err := reply(&out, rc, notes); err != nil {
			t.Fatal(err)
		}
		return notes.Text
	}
	Hook(e, []byte(`{"hook_event_name":"SessionStart","session_id":"c","cwd":"`+wt+`","source":"startup"}`), "sync", "5", me)
	if n := prompt("5", "go"); n != "" {
		t.Errorf("no comments yet: %q", n)
	}
	tuicrComment(t, wt, "c1", "app.go", "12", "Name it --who.")
	if n := prompt("5", "<agent-message from=x>done</agent-message>"); n != "" {
		t.Errorf("another agent's message carried them:\n%s", n)
	}
	if n := prompt("6", "a helper's"); n != "" {
		t.Errorf("a helper's prompt carried them:\n%s", n)
	}
	// Its output not written (Claude Code gone, its pipe closed): not given.
	js, _ := json.Marshal(map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": "c", "cwd": wt, "prompt": "lost"})
	if rc, notes := Hook(e, js, "sync", "5", me); notes.Text == "" || reply(failing{}, rc, notes) == nil {
		t.Errorf("a prompt whose output failed: %q", notes.Text)
	}
	n := prompt("5", "read my review")
	if !strings.Contains(n, "review comments") || !strings.Contains(n, "app.go:12\nName it --who.") {
		t.Errorf("your prompt:\n%s", n)
	}
	if n := prompt("5", "and again"); n != "" {
		t.Errorf("given twice:\n%s", n)
	}
	tuicrComment(t, wt, "c2", "app.go", "40", "And this.")
	if n := prompt("5", "more"); !strings.Contains(n, "app.go:40\nAnd this.") || strings.Contains(n, "--who") {
		t.Errorf("a new one, alone:\n%s", n)
	}
}

// failing is a hook's stdout that takes nothing.
type failing struct{}

func (failing) Write([]byte) (int, error) { return 0, os.ErrClosed }

// The hook's output: the bell alone as ever, the notes as the prompt's
// additionalContext, both in one object.
func TestHookOutput(t *testing.T) {
	if got := hookOutput(false, ""); got != "" {
		t.Errorf("neither: %q", got)
	}
	if got := hookOutput(true, ""); got != `{"terminalSequence":"\u0007"}` {
		t.Errorf("the bell: %s", got)
	}
	for _, ring := range []bool{false, true} {
		var out struct {
			Specific struct {
				Event   string `json:"hookEventName"`
				Context string `json:"additionalContext"`
			} `json:"hookSpecificOutput"`
			Bell string `json:"terminalSequence"`
		}
		got := hookOutput(ring, "notes")
		if err := json.Unmarshal([]byte(got), &out); err != nil || out.Specific.Event != "UserPromptSubmit" ||
			out.Specific.Context != "notes" || (out.Bell == "\a") != ring {
			t.Errorf("ring %v: %s (%v)", ring, got, err)
		}
	}
}
