package wvr

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/robbowland/weaver/internal/github"
	"github.com/robbowland/weaver/internal/linear"
)

// A key is a number, a reference or an identifier; words, a branch's
// slash or a bare # are not.
func TestKeyQuery(t *testing.T) {
	for q, want := range map[string]bool{
		"45": true, "#45": true, " #45 ": true, "o/r#45": true, "my-org/repo.go#7": true, "ENG-42": true, "eng-42": true,
		"": false, "#": false, "flag": false, "add a flag": false, "o/r": false, "4 5": false, "#45x": false,
	} {
		if keyQuery(q) != want {
			t.Errorf("%q: %v", q, !want)
		}
	}
}

// A key is searched for exactly: the keys of the items it names (by
// number, reference or identifier, in any case) at the start of the search
// line, ranked first, else it anywhere.
func TestKeySearch(t *testing.T) {
	pr := func(repo string, n int) viewItem {
		return ghItem(github.Item{Kind: github.PRs, Repo: repo, Number: n, Title: "t", Head: "b"})
	}
	its := []viewItem{pr("o/r", 145), pr("o/r", 45), pr("o/s", 45), linItem(linear.Issue{Identifier: "ENG-42"}), linItem(linear.Issue{Identifier: "OPS-42"})}
	for q, want := range map[string]string{
		"45":     `^#45\  | '45`,
		" #45 ":  `^#45\  | '#45`,
		"O/R#45": `^#45\  | 'O/R#45`,
		"42":     `^ENG-42\  | ^OPS-42\  | '42`,
		"eng-42": `^ENG-42\  | 'eng-42`,
		"9":      `'9`,
	} {
		if got := keySearch(its, q); got != want {
			t.Errorf("%q: %s, want %s", q, got, want)
		}
	}
	// What it is searched on: the key first, then the whole title and the
	// terms; no thread number or cell, whose digits match anything.
	e := viewEnv(t)
	updateGitHub(e, func(c *ghCache) {
		c.Results = map[github.Kind][]github.Result{github.PRs: {{Section: github.Section{Title: "Queue"}}}}
		c.Results[github.PRs][0].Items = []github.Item{{Kind: github.PRs, Repo: "o/r", Number: 45, Title: "Add a flag", Author: "alice", Head: "add-flag"}}
	})
	for _, l := range strings.Split(e.itemRows(githubSource{}), "\n") {
		if f := strings.Split(l, "\t"); f[0] == "i0" && reAnsi.ReplaceAllString(f[5], "") != "#45  Add a flag  @alice · o/r · add-flag" {
			t.Errorf("search line %q", f[5])
		}
	}
	if got := e.itemQuery(githubSource{}, "45"); got != "change-with-nth(6)+search(^#45\\  | '45)\n" {
		t.Errorf("query 45: %s", got)
	}
	if got := e.itemQuery(githubSource{}, "flag"); got != "change-with-nth(6)\n" {
		t.Errorf("query flag: %s", got)
	}
}

// / looks an item up when what it names is a whole id of the view's
// service (o/r#45 or #45 in GITHUB, ENG-42 in LINEAR) that nothing loaded
// has; never for a bare number, another service's id, or one loaded.
func TestLookupWanted(t *testing.T) {
	loaded := []viewItem{{ref: "o/r#45", key: "#45"}, {ref: "ENG-42", key: "ENG-42"}}
	for _, c := range []struct {
		src  itemSource
		q    string
		want bool
	}{
		{githubSource{}, "o/r#99", true},
		{githubSource{}, "#99", true},
		{githubSource{}, " #99 ", true},
		{githubSource{}, "o/r#45", false},
		{githubSource{}, "#45", false},
		{githubSource{}, "99", false},
		{githubSource{}, "ENG-99", false},
		{linearSource{}, "OPS-7", true},
		{linearSource{}, "eng-42", false},
		{linearSource{}, "o/r#99", false},
		{linearSource{}, "flaky", false},
	} {
		if got := lookupWanted(c.src, loaded, c.q); got != c.want {
			t.Errorf("%s %q: %v, want %v", c.src.name(), c.q, got, c.want)
		}
	}
}

// fakeView is a view's fzf socket that says its query is query, and keeps
// the actions posted to it.
func fakeView(t *testing.T, query string) (string, *[]string) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "v.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var posted []string
	s := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			posted = append(posted, string(b))
			mu.Unlock()
			return
		}
		fmt.Fprintf(w, `{"query":%q,"matchCount":0}`, query)
	})}
	go func() { _ = s.Serve(l) }()
	t.Cleanup(func() { _ = s.Close() })
	return sock, &posted
}

// The lookup waits for typing to stop on the id: still the query, it asks
// for it as ctrl+s does, the view's query cleared; another query by then,
// nothing.
func TestItemLookup(t *testing.T) {
	lookupPause = 0
	e := viewEnv(t)
	sock, posted := fakeView(t, "#77")
	t.Setenv("WEAVER_VIEW_SOCK", sock)
	e.itemLookup(githubSource{}, "o/r#99")
	if s, ok := readGitHub(e).Search[github.PRs]; ok || len(*posted) > 0 {
		t.Errorf("the query moved on: a search all the same, %+v, %q", s.Section, *posted)
	}
	sock, posted = fakeView(t, "o/r#99")
	t.Setenv("WEAVER_VIEW_SOCK", sock)
	e.itemLookup(githubSource{}, "o/r#99")
	if s := readGitHub(e).Search[github.PRs]; s.Title != "o/r#99" || len(*posted) != 1 || !strings.HasPrefix((*posted)[0], "clear-query+") {
		t.Errorf("looked up: %+v, %q", s.Section, *posted)
	}
}
