package wvr

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/github"
	"github.com/robbowland/weaver/internal/page"
	"github.com/robbowland/weaver/internal/session"
)

// viewEnv is a session of no tmux server, its state in a directory of its
// own.
func viewEnv(t *testing.T) *Env {
	t.Helper()
	return &Env{&session.Env{StateDir: t.TempDir(), TSock: []string{"-S", filepath.Join(t.TempDir(), "none")}}}
}

func prs(ns ...int) []github.Item {
	var its []github.Item
	for _, n := range ns {
		its = append(its, github.Item{Kind: github.PRs, Repo: "o/r", Number: n, Title: "pr"})
	}
	return its
}

// A refresh replaces each section, but one that failed keeps what that
// section had, as of when it was read: found by its search, though the
// sections moved (an upgrade put Team before Mine).
func TestRefreshKeepsFailedSection(t *testing.T) {
	then, now := time.Now().Add(-time.Hour), time.Now()
	queue := github.Section{Title: "Queue", Filters: "q"}
	team := github.Section{Title: "Team", Filters: "t"}
	mine := github.Section{Title: "Mine", Filters: "m"}
	failed := page.Failed[github.Item](errors.New("gh: HTTP 502"))
	c := ghCache{Results: map[github.Kind][]github.Result{github.PRs: {
		{Section: queue, List: page.List[github.Item]{Total: 1, Items: prs(1), Fetched: then}},
		{Section: mine, List: page.List[github.Item]{Total: 1, Items: prs(2), Fetched: then}},
	}}}
	c.refreshed(map[github.Kind][]github.Result{github.PRs: {
		{Section: queue, List: page.List[github.Item]{Total: 1, Items: prs(3), Fetched: now}},
		{Section: team, List: failed},
		{Section: mine, List: failed},
	}})
	got := c.Results[github.PRs]
	if len(got) != 3 || got[0].Items[0].Number != 3 || got[0].Err != "" || !got[0].Fetched.Equal(now) {
		t.Errorf("Queue, asked again: %+v", got)
	}
	if len(got[1].Items) != 0 || got[1].Err == "" || !got[1].Fetched.IsZero() {
		t.Errorf("Team, failed with nothing before: %+v", got[1])
	}
	if len(got[2].Items) != 1 || got[2].Items[0].Number != 2 || got[2].Err != "gh: HTTP 502" || !got[2].Fetched.Equal(then) {
		t.Errorf("Mine, failed: %+v", got[2])
	}

	// The view: its age is its oldest section's, and the sections that
	// failed are named.
	e := viewEnv(t)
	updateGitHub(e, func(o *ghCache) { *o = c })
	if h := reAnsi.ReplaceAllString(e.itemFresh(githubSource{}), ""); h != "as of 1h ago · TEAM, MINE failed: gh: HTTP 502" {
		t.Errorf("header %q", h)
	}
	d := githubSource{}.read(e).sections["prs"]
	for i, want := range []string{"QUEUE · 1", "TEAM · failed", "MINE · 1 · failed, as of 1h ago"} {
		if h := reAnsi.ReplaceAllString(d[i].heading(), ""); h != want {
			t.Errorf("heading %q, want %q", h, want)
		}
	}
}

// An older weaver's cache said when it asked for every section, and one
// error for them all: each section reads as of then, and the error, of no
// section, goes.
func TestOldCache(t *testing.T) {
	e := viewEnv(t)
	at := time.Now().Add(-3 * time.Hour).UTC().Truncate(time.Second)
	old := `{"fetched":"` + at.Format(time.RFC3339) + `","err":"gh: timeout","results":{"prs":[` +
		`{"title":"Queue","filters":"q","total":1,"items":[{"kind":"prs","repo":"o/r","number":45,"title":"Add a flag"}]}]}}`
	if err := os.WriteFile(ghCacheFile(e), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	s := githubSource{}.read(e).sections["prs"]
	if len(s) != 1 || len(s[0].items) != 1 || !s[0].fetched.Equal(at) || s[0].err != "" {
		t.Fatalf("%+v", s)
	}
	if h := reAnsi.ReplaceAllString(e.itemFresh(githubSource{}), ""); h != "as of 3h ago" {
		t.Errorf("header %q", h)
	}
	// Linear's, the same.
	old = `{"fetched":"` + at.Format(time.RFC3339) + `","err":"no Linear API key (wvr linear login)","results":[` +
		`{"title":"Queue","filter":"{}","items":[{"identifier":"ENG-42","title":"Fix it"}]}]}`
	if err := os.WriteFile(linCacheFile(e), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if s := (linearSource{}).read(e).sections["issues"]; len(s) != 1 || !s[0].fetched.Equal(at) || s[0].err != "" {
		t.Errorf("%+v", s)
	}
}

// A heading says how many it shows of how many, or, of a list the service
// does not count (Linear's), that there are more.
func TestHeadingCount(t *testing.T) {
	its, now := make([]viewItem, 30), time.Now()
	for _, c := range []struct {
		s    itemSection
		want string
	}{
		{itemSection{title: "Queue", total: 57, items: its[:20], more: true, fetched: now}, "QUEUE · 20 of 57"},
		{itemSection{title: "Queue", items: its, more: true, fetched: now}, "QUEUE · 30+"},
		{itemSection{title: "Queue", items: its[:5], fetched: now}, "QUEUE · 5"},
	} {
		if got := reAnsi.ReplaceAllString(c.s.heading(), ""); got != c.want {
			t.Errorf("%q, want %q", got, c.want)
		}
	}
}
