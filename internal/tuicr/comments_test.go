package tuicr

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/store"
)

// reviews writes a reviews directory as tuicr 0.28 keeps it: index.json,
// and one session file per entry, for the checkout dir (its canonical
// path, as tuicr keys it).
func reviews(t *testing.T, dir string, sessions map[string]string) string {
	t.Helper()
	r := t.TempDir()
	if err := os.MkdirAll(r+"/sessions", 0o755); err != nil {
		t.Fatal(err)
	}
	canon, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string]any{}
	for name, body := range sessions {
		var s session
		if err := json.Unmarshal([]byte(body), &s); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		n := len(s.Review)
		for _, f := range s.Files {
			n += len(f.File)
			for _, cs := range f.Lines {
				n += len(cs)
			}
		}
		if err := os.WriteFile(r+"/sessions/"+name+".json", []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		entries["o/r@b/staged-and-unstaged/"+name] = []any{map[string]any{
			"path": "sessions/" + name + ".json", "kind": map[string]any{"type": "local"},
			"updated_at": "2026-11-19T21:57:18.942640Z", "canonical_repo_path": canon,
			"display": map[string]any{"comment_count": n, "reviewed_count": 0, "file_count": 1, "anchor": "b"}}}
	}
	b, _ := json.Marshal(map[string]any{"version": "2.0", "entries": entries})
	if err := os.WriteFile(r+"/index.json", b, 0o644); err != nil {
		t.Fatal(err)
	}
	return r
}

// A session as tuicr writes it: a review comment, a file's, a line's, a
// range's on the old side, and one an agent added.
const sessionA = `{
 "id": "a", "version": "1.3", "repo_path": "/x/", "branch_name": "b", "base_commit": "c",
 "diff_source": "staged_and_unstaged", "commit_range": null,
 "created_at": "2026-11-19T10:00:00Z", "updated_at": "2026-11-19T10:05:00Z",
 "review_comments": [
  {"id": "r1", "content": "Overall fine.", "comment_type": "none", "created_at": "2026-11-19T10:04:00Z",
   "line_context": null, "side": null, "line_range": null, "author": "user", "lifecycle_state": "local_draft"}
 ],
 "files": {
  "app.go": {"path": "app.go", "reviewed": false, "status": "modified",
   "file_comments": [
    {"id": "f1", "content": "Split this file.", "comment_type": "suggestion", "created_at": "2026-11-19T10:03:00Z",
     "line_context": null, "side": null, "line_range": null, "author": "user", "lifecycle_state": "local_draft"}
   ],
   "line_comments": {
    "12": [{"id": "l1", "content": "Name it --who.", "comment_type": "issue", "created_at": "2026-11-19T10:01:00Z",
     "line_context": {"new_line": 12, "old_line": null, "content": "x"}, "side": "new", "line_range": null, "author": "user"}],
    "30": [{"id": "l2", "content": "Why drop this?", "comment_type": "question", "created_at": "2026-11-19T10:02:00Z",
     "side": "old", "line_range": {"start": 30, "end": 34}, "author": "user"},
     {"id": "bot", "content": "I agree.", "comment_type": "none", "created_at": "2026-11-19T10:02:30Z",
     "side": "new", "line_range": null, "author": "Claude Opus"}]
   },
   "reviewed_hunks": [], "content_hash": 1}
 },
 "session_notes": null
}`

// Your comments on the checkout: each where tuicr says it is, oldest
// first; an agent's, another checkout's and older ones left out.
func TestComments(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	r := reviews(t, dir, map[string]string{"a": sessionA})
	since := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC).Unix()
	var got []string
	for _, c := range Comments(r, since, dir) {
		got = append(got, c.ID+" "+c.Location+" "+c.Type)
	}
	want := []string{"l1 app.go:12 issue", "l2 app.go:30-34 [old] question", "f1 app.go suggestion", "r1 review "}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if cs := Comments(r, since, t.TempDir()); len(cs) != 0 {
		t.Errorf("another checkout's: %+v", cs)
	}
	if cs := Comments(r, since+150, dir); len(cs) != 2 {
		t.Errorf("since 10:02:30: %d comments, want 2 (f1, r1)", len(cs))
	}
	if cs := Comments(t.TempDir(), 0, dir); cs != nil {
		t.Errorf("no tuicr files: %+v", cs)
	}
}

// Your tuicr settings' username is yours too.
func TestCommentsUsername(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	if err := os.MkdirAll(cfg+"/tuicr", 0o755); err != nil {
		t.Fatal(err)
	}
	conf := "theme = \"x\"\nusername = \"Rob\"\n\n[forge]\nusername = \"nope\"\n"
	if err := os.WriteFile(cfg+"/tuicr/config.toml", []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	r := reviews(t, dir, map[string]string{"a": strings.ReplaceAll(sessionA, `"author": "Claude Opus"`, `"author": "Rob"`)})
	if cs := Comments(r, 0, dir); len(cs) != 5 {
		t.Errorf("%d comments, want 5 (Rob's too)", len(cs))
	}
}

// Given once, a comment is not unread again; a new one is.
func TestUnreadGive(t *testing.T) {
	given := t.TempDir() + "/threads/1-x/review-comments"
	cs := []Comment{{ID: "a"}, {ID: "b"}}
	if u := Unread(cs, given); len(u) != 2 {
		t.Fatalf("nothing given yet: %d unread", len(u))
	}
	if err := Give(given, cs[:1]); err != nil {
		t.Fatal(err)
	}
	if u := Unread(append(cs, Comment{ID: "c"}), given); len(u) != 2 || u[0].ID != "b" || u[1].ID != "c" {
		t.Errorf("after a given: %+v", u)
	}
}

// A thread's unread comments: on its worktree, since it was made, less
// those given.
func TestThreadUnread(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", home+"/.local/share")
	t.Setenv("XDG_CONFIG_HOME", home+"/.config")
	wt := t.TempDir()
	r := reviews(t, wt, map[string]string{"a": sessionA})
	if err := os.MkdirAll(filepath.Dir(Dir()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(r, Dir()); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	th := store.Thread{Created: time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC).Unix(), Worktree: wt}
	u := ThreadUnread(state, th, "")
	if len(u) != 4 {
		t.Fatalf("%d unread, want 4", len(u))
	}
	if err := Give(GivenFile(state, th), u[:3]); err != nil {
		t.Fatal(err)
	}
	if u := ThreadUnread(state, th, wt); len(u) != 1 || u[0].ID != "r1" {
		t.Errorf("after three given: %+v", u)
	}
}

// The context: marked as your review comments, each its location and
// text, in a fence nothing in them closes; what does not fit waits.
func TestContext(t *testing.T) {
	cs := []Comment{{ID: "a", Location: "app.go:12", Type: "issue", Text: "Use ```go fences```."},
		{ID: "b", Location: "review", Text: "Fine."}}
	s, n := Context(cs, "/w")
	if n != 2 {
		t.Fatalf("%d of 2 in:\n%s", n, s)
	}
	for _, want := range []string{"review comments", "tuicr review list --repo /w", "````review-comments\napp.go:12 (issue)\nUse ```go fences```.\n\nreview\nFine.\n````\n"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	big := []Comment{{ID: "a", Location: "x:1", Text: strings.Repeat("x", 6000)}, {ID: "b", Location: "x:2", Text: strings.Repeat("y", 6000)}}
	if s, n := Context(big, "/w"); n != 1 || !strings.Contains(s, "1 more come") || strings.Contains(s, "yyy") {
		t.Errorf("too much: %d given:\n%.200s", n, s)
	}
	if s, n := Context(nil, "/w"); s != "" || n != 0 {
		t.Errorf("none: %q %d", s, n)
	}
}
