package wvr

import (
	"fmt"
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

// ghCache is what GitHub said last, in $STATE_DIR/github.json.
type ghCache struct {
	Fetched time.Time                       `json:"fetched"`
	Err     string                          `json:"err,omitempty"`
	Results map[github.Kind][]github.Result `json:"results"`
}

func (githubSource) name() string    { return "github" }
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
	return c
}

func (githubSource) read(e *Env) itemData {
	c := readGitHub(e)
	d := itemData{fetched: c.Fetched, err: c.Err, sections: map[string][]itemSection{}}
	for k, rs := range c.Results {
		for _, r := range rs {
			s := itemSection{title: r.Title, total: r.Total}
			for _, it := range r.Items {
				s.items = append(s.items, ghItem(it))
			}
			d.sections[string(k)] = append(d.sections[string(k)], s)
		}
	}
	return d
}

// fetch asks GitHub again and keeps the answer: a section that failed
// keeps what it had.
func (githubSource) fetch(e *Env) {
	old := readGitHub(e)
	res, err := github.Fetch()
	c := ghCache{Fetched: time.Now(), Results: res}
	if err != nil {
		c.Err = err.Error()
		for k, rs := range res {
			for i, r := range rs {
				if len(r.Items) == 0 && r.Total == 0 && i < len(old.Results[k]) {
					res[k][i] = old.Results[k][i]
				}
			}
		}
	}
	bestEffort(store.Write(ghCacheFile(e), c))
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
	}
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
		v.branch, v.startOn = it.Head, it.Head
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
