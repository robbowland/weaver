package wvr

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/robbowland/weaver/internal/github"
	"github.com/robbowland/weaver/internal/items"
	"github.com/robbowland/weaver/internal/page"
	"github.com/robbowland/weaver/internal/store"
)

// The GITHUB view's source: the pull requests and issues of weaver's
// sections (internal/github, after gh-dash).

type githubSource struct{}

// ghCache is what GitHub said last, in $STATE_DIR/github.json: each
// section's items, and how asking for them went; and those of the view's
// own search, of each kind.
type ghCache struct {
	// Fetched is an older weaver's: when it asked for every section, which
	// is when its sections were read (settle).
	Fetched time.Time                       `json:"fetched,omitzero"`
	Results map[github.Kind][]github.Result `json:"results"`
	// Search is the view's own search (f), of each kind: none read yet just
	// after it was asked for.
	Search map[github.Kind]github.Result `json:"search,omitempty"`
}

// settle has an older weaver's cache say, section by section, when its
// sections were read.
func (c *ghCache) settle() {
	for _, rs := range c.Results {
		for i := range rs {
			if rs[i].Fetched.IsZero() && rs[i].Err == "" {
				rs[i].Fetched = c.Fetched
			}
		}
	}
	c.Fetched = time.Time{}
}

// refreshed is c with a refresh's answer: every section as asked, but one
// that failed with what that section (by its search, wherever it was) had;
// and the search's, if it is still the search.
func (c *ghCache) refreshed(got, found map[github.Kind][]github.Result) {
	for k, rs := range got {
		for i, r := range rs {
			if j := slices.IndexFunc(c.Results[k], func(o github.Result) bool { return o.Section == r.Section }); j >= 0 {
				rs[i].List = r.List.Since(c.Results[k][j].List)
			}
		}
	}
	c.Results = got
	for k, rs := range found {
		for _, r := range rs {
			c.found(k, r)
		}
	}
}

// search makes s the view's search, of both kinds, none of it read yet.
func (c *ghCache) search(s github.Section) {
	c.Search = map[github.Kind]github.Result{github.PRs: {Section: s}, github.Issues: {Section: s}}
}

// found is c with r, the first page of a search of kind k, if that is
// still the search: one that failed keeps what the search had.
func (c *ghCache) found(k github.Kind, r github.Result) {
	if s, ok := c.Search[k]; ok && s.Section == r.Section {
		r.List = r.List.Since(s.List)
		c.Search[k] = r
	}
}

// paged is c with p, the page of section s of kind k read after cursor
// after, after that section's items, if it still ends there.
func (c *ghCache) paged(k github.Kind, s github.Section, after string, p page.List[github.Item]) {
	for i, r := range c.Results[k] {
		if r.Section == s {
			c.Results[k][i].List = r.List.More(after, p, github.Item.Ref)
		}
	}
	if r, ok := c.Search[k]; ok && r.Section == s {
		r.List = r.List.More(after, p, github.Item.Ref)
		c.Search[k] = r
	}
}

// held is how many items c holds of section s of kind k (github.Held).
func (c ghCache) held(k github.Kind, s github.Section) int {
	if r, ok := c.Search[k]; ok && r.Section == s {
		return len(r.Items)
	}
	for _, r := range c.Results[k] {
		if r.Section == s {
			return len(r.Items)
		}
	}
	return 0
}

// section is c's section key of kind k: its index, or the search's ("s").
func (c ghCache) section(k github.Kind, key string) (github.Result, bool) {
	if key == searchKey {
		r, ok := c.Search[k]
		return r, ok
	}
	i, ok := atoi(key)
	if !ok || i >= len(c.Results[k]) {
		return github.Result{}, false
	}
	return c.Results[k][i], true
}

func (githubSource) name() string    { return "github" }
func (githubSource) service() string { return "GitHub" }

func (githubSource) guide() viewGuide {
	return viewGuide{
		queue:    "yours to do: your review asked of you, or assigned to you",
		team:     "your teams' to do: a review asked of a team you are in",
		mine:     "yours: opened by you",
		watch:    "what involves you otherwise",
		red:      "changes requested, or a check failing",
		green:    "approved",
		faint:    "a draft, or nothing to say but who",
		readOnly: "a review (a pull request), an investigation (an issue)",
	}
}
func (githubSource) kinds() []string { return []string{string(github.PRs), string(github.Issues)} }

func (githubSource) label(kind string) string {
	if kind == string(github.Issues) {
		return "ISSUES"
	}
	return "PULL REQUESTS"
}

func ghCacheFile(e *Env) string { return e.StateDir + "/github.json" }

func readGitHub(e *Env) ghCache {
	var c ghCache
	bestEffort(store.Read(ghCacheFile(e), &c))
	c.settle()
	return c
}

// updateGitHub changes the cache as it is now, under its lock: a refresh
// and a change at once each keep the other's.
func updateGitHub(e *Env, change func(*ghCache)) {
	bestEffort(store.Update(ghCacheFile(e), func(c *ghCache) {
		c.settle()
		change(c)
	}))
}

func (g githubSource) read(e *Env) itemData {
	c := readGitHub(e)
	d := itemData{sections: map[string][]itemSection{}}
	section := func(key string, r github.Result) itemSection {
		s := itemSection{key: key, title: r.Title, total: r.Total, more: r.Next != "", fetched: r.Fetched, err: r.Err}
		for _, it := range r.Items {
			s.items = append(s.items, ghItem(it))
		}
		return s
	}
	for k, rs := range c.Results {
		if r, ok := c.Search[k]; ok {
			s := section(searchKey, r)
			s.search = true
			// What the search found of the other kind, for s to show.
			for o, r := range c.Search {
				if o != k && len(r.Items) > 0 {
					s.note = strings.ToLower(g.label(string(o))) + ": " + strconv.Itoa(len(r.Items)) + " (s)"
				}
			}
			d.sections[string(k)] = append(d.sections[string(k)], s)
		}
		for i, r := range rs {
			d.sections[string(k)] = append(d.sections[string(k)], section(strconv.Itoa(i), r))
		}
	}
	return d
}

// fetch asks GitHub again, the search too, each as deep as it is held
// (what more read stays), and keeps the answer (refreshed).
func (githubSource) fetch(e *Env) {
	c := readGitHub(e)
	var search []github.Section
	if r, ok := c.Search[github.PRs]; ok {
		search = append(search, r.Section)
	}
	got, found := github.Fetch(c.held, search...)
	updateGitHub(e, func(c *ghCache) { c.refreshed(got, found) })
}

// more reads the page after section key's of kind, and keeps it (paged).
func (githubSource) more(e *Env, kind, key string) {
	k := github.Kind(kind)
	r, ok := readGitHub(e).section(k, key)
	if !ok || r.Next == "" {
		return
	}
	p := github.Ask(k, r.Section, r.Next)
	updateGitHub(e, func(c *ghCache) { c.paged(k, r.Section, r.Next, p.List) })
}

// filters are what f offers: weaver's searches of your own, then what is
// open in each repository weaver knows, then with each label it has seen
// (in the repositories it saw it in).
func (githubSource) filters(e *Env) []viewFilter {
	c := readGitHub(e)
	var fs []viewFilter
	add := func(f github.Filter, note string) { fs = append(fs, ghFilter(f, note)) }
	for _, f := range github.Filters {
		add(f, "")
	}
	for _, r := range e.knownRepos(c) {
		add(github.RepoFilter(r), "")
	}
	labels := map[string][]string{}
	for _, it := range c.items() {
		for _, l := range it.Labels {
			if !slices.ContainsFunc(labels[l], func(r string) bool { return items.SameRepo(r, it.Repo) }) {
				labels[l] = append(labels[l], it.Repo)
			}
		}
	}
	for _, l := range slices.Sorted(maps.Keys(labels)) {
		slices.Sort(labels[l])
		add(github.LabelFilter(l, labels[l]), strings.Join(labels[l], ", "))
	}
	return fs
}

// ghFilter is f as f offers it, with a note.
func ghFilter(f github.Filter, note string) viewFilter {
	return viewFilter{title: f.Title, note: note, set: func(e *Env) {
		updateGitHub(e, func(c *ghCache) { c.search(f.Section()) })
	}}
}

// words is a search for words as typed (github.Words), in the
// repositories weaver knows: an item named, or a number, read as it is;
// words, within their owners'.
func (githubSource) words(e *Env, q string) viewFilter {
	return ghFilter(github.Words(q, e.knownRepos(readGitHub(e))), "")
}

// find reads the first page of the search, of each kind, and keeps it.
func (githubSource) find(e *Env) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	got := map[github.Kind]github.Result{}
	for k, r := range readGitHub(e).Search {
		wg.Go(func() {
			a := github.Ask(k, r.Section, "")
			mu.Lock()
			got[k] = a
			mu.Unlock()
		})
	}
	wg.Wait()
	updateGitHub(e, func(c *ghCache) {
		for k, r := range got {
			c.found(k, r)
		}
	})
}

// clear ends the search.
func (githubSource) clear(e *Env) {
	updateGitHub(e, func(c *ghCache) { c.Search = nil })
}

// items are every item c holds, the search's too.
func (c ghCache) items() []github.Item {
	var its []github.Item
	for _, rs := range c.Results {
		for _, r := range rs {
			its = append(its, r.Items...)
		}
	}
	for _, r := range c.Search {
		its = append(its, r.Items...)
	}
	return its
}

// knownRepos are the GitHub repositories weaver knows: the checkouts
// threads started in ($STATE_DIR/repos), most recent first, then those of
// the items c holds, by name; each once, in any case.
func (e *Env) knownRepos(c ghCache) []string {
	var rs []string
	add := func(r string) {
		if r != "" && !slices.ContainsFunc(rs, func(o string) bool { return items.SameRepo(o, r) }) {
			rs = append(rs, r)
		}
	}
	if b, err := os.ReadFile(e.StateDir + "/repos"); err == nil {
		for _, d := range strings.Split(string(b), "\n") {
			if d != "" {
				add(items.GitHubRepo(d))
			}
		}
	}
	var seen []string
	for _, it := range c.items() {
		seen = append(seen, it.Repo)
	}
	slices.Sort(seen)
	for _, r := range seen {
		add(r)
	}
	return rs
}

// ghItem is a pull request or issue as the view shows it.
func ghItem(it github.Item) viewItem {
	state := strings.ToLower(it.State)
	if it.Draft {
		state = "draft"
	}
	v := viewItem{
		ref:     it.Ref(),
		title:   untick(it.Title),
		fact:    ghFact(it),
		where:   it.Repo[strings.IndexByte(it.Repo, '/')+1:] + " #" + strconv.Itoa(it.Number),
		sub:     it.Repo + " #" + strconv.Itoa(it.Number) + " · " + state,
		updated: it.UpdatedAt,
		url:     it.URL,
		repo:    it.Repo,
		body:    it.Body,
		key:     "#" + strconv.Itoa(it.Number),
		terms:   append([]string{"@" + it.Author, it.Repo, it.Head}, it.Labels...),
	}
	v.terms = slices.DeleteFunc(v.terms, func(s string) bool { return s == "" || s == "@" })
	f := func(label, text, colour string, lines int) {
		if text != "" {
			v.fields = append(v.fields, cardField{label, text, colour, lines})
		}
	}
	if it.Kind == github.Issues {
		v.from = []string{"github", "issue", it.Repo, strconv.Itoa(it.Number)}
		v.branch, v.branchPrefix = strconv.Itoa(it.Number)+"-", true
		v.startOn = items.GitHubIssue{Repo: it.Repo, Num: it.Number, Text: items.Text{Title: it.Title}}.BranchName()
		f("by", "@"+it.Author, CGrey, 1)
	} else {
		v.from = []string{"github", "pr", it.Repo, strconv.Itoa(it.Number)}
		// Its thread's branch, as the thread names it: pr-N for one from a
		// fork, whose own branch (its main, say) is not this repository's.
		v.branch = items.PullRequest{Repo: it.Repo, Num: it.Number, Head: it.Head, Fork: it.HeadInFork}.BranchName()
		v.startOn = v.branch
		f("by", "@"+it.Author, CGrey, 1)
		f("branch", it.Base+" ← "+it.Head, CGrey, 1)
		label := "review"
		switch it.Review {
		case "CHANGES_REQUESTED":
			f(label, "changes requested", CRed, 1)
			label = ""
		case "APPROVED":
			f(label, "approved", CGreen, 1)
			label = ""
		}
		if len(it.Requested) > 0 {
			f(label, "waits on "+strings.Join(it.Requested, ", "), CGrey, 2)
		}
		label = "checks"
		if n := len(it.FailingChecks); n > 0 {
			f(label, strconv.Itoa(n)+" failing: "+strings.Join(it.FailingChecks, ", "), CRed, 2)
			label = ""
		}
		var cs []string
		if it.RunningChecks > 0 {
			cs = append(cs, strconv.Itoa(it.RunningChecks)+" running")
		}
		if it.PassingChecks > 0 {
			cs = append(cs, strconv.Itoa(it.PassingChecks)+" passing")
		}
		f(label, strings.Join(cs, " · "), CGrey, 1)
		f("lines", fmt.Sprintf("+%d -%d · %d file%s · %d commit%s", it.Additions, it.Deletions,
			it.Files, plural(it.Files), it.Commits, plural(it.Commits)), CGrey, 1)
	}
	f("labels", strings.Join(it.Labels, ", "), CGrey, 1)
	f("assigned", strings.Join(it.Assignees, ", "), CGrey, 1)
	if it.Comments > 0 {
		f("comments", strconv.Itoa(it.Comments), CGrey, 1)
	}
	return v
}

// ghFact is the one thing to know about an item, coloured as a signal.
func ghFact(it github.Item) string {
	if it.Kind == github.Issues {
		if len(it.Labels) > 0 {
			return CGrey + it.Labels[0] + COff
		}
		return CDim + "@" + it.Author + COff
	}
	switch {
	case it.Draft:
		return CDim + "draft" + COff
	case it.Review == "CHANGES_REQUESTED":
		return CRed + "changes requested" + COff
	case len(it.FailingChecks) == 1:
		return CRed + it.FailingChecks[0] + " failing" + COff
	case len(it.FailingChecks) > 1:
		return CRed + strconv.Itoa(len(it.FailingChecks)) + " checks failing" + COff
	case it.Review == "APPROVED":
		return CGreen + "approved" + COff
	case it.RunningChecks > 0:
		return CGrey + "checks running" + COff
	}
	return CDim + "@" + it.Author + COff
}
