package wvr

import (
	"strings"
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
