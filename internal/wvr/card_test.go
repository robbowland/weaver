package wvr

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/rex/rextest"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
	"github.com/robbowland/weaver/internal/tuicr"
)

// A thread's card counts your review comments its agent has not been
// given: they go with your next prompt.
func TestCardReviewComments(t *testing.T) {
	rextest.NoRex(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", home+"/data")
	t.Setenv("XDG_CONFIG_HOME", home+"/config")
	t.Setenv("FZF_SOCK", "")
	e := &Env{&session.Env{StateDir: t.TempDir(), Session: "s", SKey: "rex-s"}}
	wt := t.TempDir()
	th := store.Thread{Name: "b", Worktree: wt, Created: time.Now().Add(-time.Hour).Unix()}
	if store.Write(e.ThreadFile("5"), th) != nil || store.Write(e.StateFile("5"), agent.Record{State: agent.StateIdle, Root: wt}) != nil {
		t.Fatal("cannot write the thread")
	}
	card := func() string { return ansi.Strip(e.Preview([]string{"5", "b", "5"})) }
	if c := card(); strings.Contains(c, "review comment") {
		t.Errorf("no comments:\n%s", c)
	}
	// Two comments of yours in tuicr's files, on the thread's worktree.
	r := tuicr.Dir()
	if err := os.MkdirAll(r+"/sessions", 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	lines := map[string]any{}
	for _, id := range []string{"c1", "c2"} {
		lines[id[1:]] = []any{map[string]any{"id": id, "content": "x", "comment_type": "none", "created_at": now, "author": "user"}}
	}
	s, _ := json.Marshal(map[string]any{"review_comments": []any{}, "files": map[string]any{"a.go": map[string]any{"file_comments": []any{}, "line_comments": lines}}})
	canon, _ := filepath.EvalSymlinks(wt)
	ix, _ := json.Marshal(map[string]any{"entries": map[string]any{"o/r@b": []any{map[string]any{"path": "sessions/a.json",
		"kind": map[string]any{"type": "local"}, "canonical_repo_path": canon, "display": map[string]any{"comment_count": 2}}}}})
	if os.WriteFile(r+"/sessions/a.json", s, 0o644) != nil || os.WriteFile(r+"/index.json", ix, 0o644) != nil {
		t.Fatal("cannot write tuicr's files")
	}
	if c := card(); !strings.Contains(c, "review     2 review comments, with your next prompt") {
		t.Errorf("two waiting:\n%s", c)
	}
	if err := tuicr.ThreadGive(e.StateDir, th, tuicr.Comments(r, 0, wt)[:1]); err != nil {
		t.Fatal(err)
	}
	if c := card(); !strings.Contains(c, "1 review comment,") {
		t.Errorf("one given, one waiting:\n%s", c)
	}
}
