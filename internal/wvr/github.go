package wvr

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/robbowland/weaver/internal/github"
	"github.com/robbowland/weaver/internal/items"
	"github.com/robbowland/weaver/internal/store"
)

// The GITHUB view's source: the pull requests and issues of weaver's
// sections (internal/github, after gh-dash).

type githubSource struct{}

// ghCache is what GitHub said last, in $STATE_DIR/github.json: each
// section's items, and how asking for them went.
type ghCache struct {
	// Fetched is an older weaver's: when it asked for every section, which
	// is when its sections were read (settle).
	Fetched time.Time                       `json:"fetched,omitzero"`
	Results map[github.Kind][]github.Result `json:"results"`
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
// that failed with what that section (by its search, wherever it was) had.
func (c *ghCache) refreshed(got map[github.Kind][]github.Result) {
	for k, rs := range got {
		for i, r := range rs {
			if j := slices.IndexFunc(c.Results[k], func(o github.Result) bool { return o.Section == r.Section }); j >= 0 {
				rs[i].List = r.List.Since(c.Results[k][j].List)
			}
		}
	}
	c.Results = got
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

func (githubSource) read(e *Env) itemData {
	c := readGitHub(e)
	d := itemData{sections: map[string][]itemSection{}}
	for k, rs := range c.Results {
		for _, r := range rs {
			s := itemSection{title: r.Title, total: r.Total, more: r.Next != "", fetched: r.Fetched, err: r.Err}
			for _, it := range r.Items {
				s.items = append(s.items, ghItem(it))
			}
			d.sections[string(k)] = append(d.sections[string(k)], s)
		}
	}
	return d
}

// fetch asks GitHub again and keeps the answer (refreshed).
func (githubSource) fetch(e *Env) {
	got := github.Fetch()
	updateGitHub(e, func(c *ghCache) { c.refreshed(got) })
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
