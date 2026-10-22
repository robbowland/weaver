package wvr

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/github"
	"github.com/robbowland/weaver/internal/linear"
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
	}}, nil)
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

// Every line has a key fzf finds it by again after a reload (--track
// --id-nth 2): on a line of none, fzf blocks for good, looking for it, and
// the view came up empty.
func TestRowsKeyed(t *testing.T) {
	e := viewEnv(t)
	now := time.Now()
	updateGitHub(e, func(c *ghCache) {
		c.Results = map[github.Kind][]github.Result{github.PRs: {
			{Section: github.Section{Title: "Queue"}, List: page.List[github.Item]{Total: 1, Items: prs(1), Fetched: now}},
			{Section: github.Section{Title: "Mine"}, List: page.List[github.Item]{Total: 1, Items: prs(2), Fetched: now}},
		}}
	})
	seen := map[string]bool{}
	for _, l := range strings.Split(strings.TrimSuffix(e.itemRows(githubSource{}), "\n"), "\n") {
		f := strings.Split(l, "\t")
		if f[1] == "" || seen[f[1]] {
			t.Errorf("line %q: no key of its own", l)
		}
		seen[f[1]] = true
	}
	if len(seen) != 5 {
		t.Errorf("%d lines", len(seen))
	}
}

// A section with more ends in a line that enter (and m) reads them by, a
// line the cursor rests on; its heading counts what is left.
func TestMoreLine(t *testing.T) {
	e := viewEnv(t)
	now := time.Now()
	updateGitHub(e, func(c *ghCache) {
		c.Results = map[github.Kind][]github.Result{github.PRs: {
			{Section: github.Section{Title: "Queue"}, List: page.List[github.Item]{Total: 1, Items: prs(1), Fetched: now}},
			{Section: github.Section{Title: "Mine"}, List: page.List[github.Item]{Total: 3, Items: prs(2), Next: "c1", Fetched: now}},
		}}
	})
	var ids, more string
	for _, l := range strings.Split(strings.TrimSuffix(e.itemRows(githubSource{}), "\n"), "\n") {
		f := strings.Split(l, "\t")
		ids += f[0] + "/" + f[2] + "/" + f[3] + " "
		if f[0] == "m1" {
			more = reAnsi.ReplaceAllString(f[4], "")
		}
	}
	// A heading or a gap steps to the lines around it the cursor rests on.
	if ids != "h0//2 i0// g1/2/5 h1/2/5 i1// m1// " {
		t.Errorf("lines %s", ids)
	}
	if strings.TrimSpace(more) != "⏎ more · 2 left" {
		t.Errorf("more line %q", more)
	}
}

// The view's own search is a section of each kind, first, SEARCH: asking
// until its first page comes, which a search changed since does not take,
// and saying what it found of the other kind.
func TestSearchSection(t *testing.T) {
	e := viewEnv(t)
	now := time.Now()
	f := github.Filters[4]
	updateGitHub(e, func(c *ghCache) {
		c.Results = map[github.Kind][]github.Result{
			github.PRs:    {{Section: github.Section{Title: "Queue"}, List: page.List[github.Item]{Total: 1, Items: prs(1), Fetched: now}}},
			github.Issues: {{Section: github.Section{Title: "Queue"}, List: page.List[github.Item]{Fetched: now}}},
		}
		c.search(f.Section())
	})
	heading := func(kind string) string {
		return reAnsi.ReplaceAllString(githubSource{}.read(e).sections[kind][0].heading(), "")
	}
	if h := heading("prs"); h != "SEARCH · mentions me · asking…" {
		t.Errorf("asked: %q", h)
	}
	issue := github.Item{Kind: github.Issues, Repo: "o/r", Number: 9, Title: "i", Labels: []string{"bug"}}
	updateGitHub(e, func(c *ghCache) {
		c.found(github.PRs, github.Result{Section: f.Section(), List: page.List[github.Item]{Total: 2, Items: prs(50, 51), Fetched: now}})
		c.found(github.Issues, github.Result{Section: f.Section(), List: page.List[github.Item]{Total: 1, Items: []github.Item{issue}, Fetched: now}})
		c.found(github.PRs, github.Result{Section: github.Filters[0].Section(), List: page.List[github.Item]{Items: prs(99), Fetched: now}})
	})
	if h := heading("prs"); h != "SEARCH · mentions me · 2 · issues: 1 (s)" {
		t.Errorf("found: %q", h)
	}
	if h := heading("issues"); h != "SEARCH · mentions me · 1 · pull requests: 2 (s)" {
		t.Errorf("found, of issues: %q", h)
	}
	if d := (githubSource{}).read(e); d.searched() != "mentions me" || len(d.sections["prs"]) != 2 {
		t.Errorf("%+v", d)
	}
	// f offers weaver's searches, then the repositories and labels it has
	// seen, a label within its repositories.
	var titles []string
	for _, f := range (githubSource{}).filters(e) {
		titles = append(titles, f.title+"|"+f.note)
	}
	if got := strings.Join(titles[len(github.Filters):], " "); got != "repository o/r| label bug|o/r" {
		t.Errorf("filters %s", got)
	}
	(githubSource{}).filters(e)[0].set(e)
	if s := readGitHub(e).Search[github.Issues]; s.Section != github.Filters[0].Section() || !s.Fetched.IsZero() {
		t.Errorf("set: %+v", s)
	}
	(githubSource{}).clear(e)
	if d := (githubSource{}).read(e); d.searched() != "" || len(d.sections["prs"]) != 1 {
		t.Errorf("cleared: %+v", d)
	}
}

// Words asked for are searched within the owners of the repositories
// weaver knows, each once, in any case.
func TestWordsWithinOwners(t *testing.T) {
	e := viewEnv(t)
	updateGitHub(e, func(c *ghCache) {
		c.Results = map[github.Kind][]github.Result{github.PRs: {{Section: github.Section{Title: "Queue"}, List: page.List[github.Item]{Items: []github.Item{
			{Kind: github.PRs, Repo: "o/r", Number: 1}, {Kind: github.PRs, Repo: "O/s", Number: 2}, {Kind: github.PRs, Repo: "p/q", Number: 3},
		}}}}}
	})
	f := (githubSource{}).words(e, " flaky  test ")
	f.set(e)
	if s := readGitHub(e).Search[github.PRs]; f.title != "flaky test" || s.Filters != "user:O user:p flaky test" {
		t.Errorf("%q: %+v", f.title, s.Section)
	}
}

// fakeGitHubPages puts a gh on PATH that answers every search with as many
// pull requests (numbered from 1) as it asks for, of 500 in all.
func fakeGitHubPages(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	gh := `#!/bin/sh
n=$(printf '%s' "$*" | sed -n 's/.*first: \([0-9][0-9]*\).*/\1/p' | head -n 1)
nodes=$(seq 1 "$n" | awk '{ printf "%s{\"number\":%d,\"title\":\"pr\",\"state\":\"OPEN\",\"repository\":{\"nameWithOwner\":\"o/r\"},\"updatedAt\":\"2026-10-16T10:00:00Z\"}", (NR > 1 ? "," : ""), $1 }')
echo "{\"data\":{\"s\":{\"issueCount\":500,\"pageInfo\":{\"hasNextPage\":true,\"endCursor\":\"c$n\"},\"nodes\":[$nodes]}}}"
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(gh), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// A refresh keeps what you loaded of a section (more): it asks for as many
// as the section holds, at least its own page (so what is new still comes)
// and at most what one request of GitHub's brings (100).
func TestRefreshKeepsDepth(t *testing.T) {
	fakeGitHubPages(t)
	e := viewEnv(t)
	held := map[string]int{"Team": 150, "Mine": 45, "Watch": 3}
	updateGitHub(e, func(c *ghCache) {
		c.Results = map[github.Kind][]github.Result{}
		for _, s := range github.Sections[github.PRs] {
			var ns []int
			for n := 1; n <= held[s.Title]; n++ {
				ns = append(ns, n)
			}
			c.Results[github.PRs] = append(c.Results[github.PRs], github.Result{Section: s, List: page.List[github.Item]{Items: prs(ns...), Next: "old"}})
		}
	})
	githubSource{}.fetch(e)
	for _, r := range readGitHub(e).Results[github.PRs] {
		want := map[string]int{"Queue": 100, "Team": 100, "Mine": 45, "Watch": 20}[r.Title]
		if len(r.Items) != want || r.Err != "" || !strings.HasPrefix(r.Next, "c"+strconv.Itoa(want)) {
			t.Errorf("%s, holding %d: %d after a refresh (next %q), want %d", r.Title, held[r.Title], len(r.Items), r.Next, want)
		}
	}
}

// A Linear refresh keeps what you loaded of a section as GitHub's does: as
// many as it holds, at least its own page (30), at most what one request
// of Linear's brings (250).
func TestLinearRefreshKeepsDepth(t *testing.T) {
	reFirst := regexp.MustCompile(`first: (\d+)`)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Query string }
		bestEffort(json.NewDecoder(r.Body).Decode(&req))
		n, _ := strconv.Atoi(reFirst.FindStringSubmatch(req.Query)[1])
		var ns []string
		for i := 1; i <= n; i++ {
			ns = append(ns, fmt.Sprintf(`{"identifier":"ENG-%d","title":"t","team":{"key":"ENG","name":"Engineering"},"state":{"name":"Todo","type":"unstarted"}}`, i))
		}
		fmt.Fprintf(w, `{"data":{"issues":{"nodes":[%s],"pageInfo":{"hasNextPage":true,"endCursor":"c%d"}}}}`, strings.Join(ns, ","), n)
	}))
	t.Cleanup(s.Close)
	t.Setenv("WEAVER_LINEAR_URL", s.URL)
	t.Setenv("LINEAR_API_KEY", "lin_test")
	e := viewEnv(t)
	held := map[string]int{"Queue": 300, "Mine": 45, "Watch": 3}
	updateLinear(e, func(c *linCache) {
		c.Results = nil
		for _, sec := range linear.Sections {
			var its []linear.Issue
			for i := 1; i <= held[sec.Title]; i++ {
				its = append(its, linear.Issue{Identifier: fmt.Sprintf("ENG-%d", i)})
			}
			c.Results = append(c.Results, linear.Result{Section: sec, List: page.List[linear.Issue]{Items: its, Next: "old"}})
		}
	})
	linearSource{}.fetch(e)
	for _, r := range readLinear(e).Results {
		want := map[string]int{"Queue": 250, "Mine": 45, "Watch": 30}[r.Title]
		if len(r.Items) != want || r.Err != "" || r.Next != "c"+strconv.Itoa(want) {
			t.Errorf("%s, holding %d: %d after a refresh (next %q, err %q), want %d", r.Title, held[r.Title], len(r.Items), r.Next, r.Err, want)
		}
	}
}
