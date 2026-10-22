// Package github reads the pull requests and issues weaver's GITHUB view
// shows, through gh's GraphQL API (`gh api graphql`, so gh's sign-in).
//
// The searches and the fields they ask for come from gh-dash
// (https://github.com/dlvhdr/gh-dash, MIT, Copyright (c) 2025 Dolev Hadar):
// its search of the issues index per section (`is:pr archived:false ...`),
// and the members of a pull request its rows and preview read, the last
// commit's status check rollup and contexts among them. Here a pull
// request carries all weaver's card shows, so no second request follows a
// selection, and the sections are asked for at once.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/robbowland/weaver/internal/page"
)

// Kind is what a section searches: pull requests or issues.
type Kind string

const (
	PRs    Kind = "prs"
	Issues Kind = "issues"
)

// Section is one search: its title, and its filters (GitHub's search
// syntax, without is:pr or is:issue); Also, a second search whose items it
// shows too (GitHub's search has no OR between qualifiers); and how many
// it shows at most (0: Limit).
type Section struct {
	Title   string `json:"title"`
	Filters string `json:"filters"`
	Also    string `json:"also,omitempty"`
	Max     int    `json:"max,omitempty"`
}

// limit is how many items section s shows at most.
func (s Section) limit() int {
	if s.Max > 0 {
		return s.Max
	}
	return Limit
}

// Sections are weaver's: what waits on you, what is yours, and what you
// take part in. A review asked of a team you are in counts as asked of you
// (review-requested:@me), and in a busy organisation those outnumber your
// own many times: so pull requests waiting on you alone (asked of you, or
// assigned to you; drafts too) are Queue, shown whole, and those waiting
// on your teams Team.
var Sections = map[Kind][]Section{
	PRs: {
		{Title: "Queue", Filters: "is:open user-review-requested:@me sort:updated-desc",
			Also: "is:open assignee:@me -author:@me sort:updated-desc", Max: 100},
		{Title: "Team", Filters: "is:open review-requested:@me -user-review-requested:@me draft:false sort:updated-desc", Max: 50},
		{Title: "Mine", Filters: "is:open author:@me sort:updated-desc"},
		{Title: "Watch", Filters: "is:open involves:@me -author:@me -assignee:@me sort:updated-desc"},
	},
	Issues: {
		{Title: "Queue", Filters: "is:open assignee:@me sort:updated-desc"},
		{Title: "Mine", Filters: "is:open author:@me sort:updated-desc"},
		{Title: "Watch", Filters: "is:open involves:@me -assignee:@me -author:@me sort:updated-desc"},
	},
}

// Item is a pull request or an issue, as the view shows it. Members only a
// pull request has are empty on an issue.
type Item struct {
	Kind      Kind      `json:"kind"`
	Repo      string    `json:"repo"` // owner/name
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	URL       string    `json:"url"`
	State     string    `json:"state"` // OPEN, CLOSED, MERGED
	Author    string    `json:"author"`
	UpdatedAt time.Time `json:"updatedAt"`
	Labels    []string  `json:"labels,omitempty"`
	Assignees []string  `json:"assignees,omitempty"`
	Comments  int       `json:"comments"`
	// A pull request's.
	Draft          bool     `json:"draft,omitempty"`
	Head           string   `json:"head,omitempty"`
	Base           string   `json:"base,omitempty"`
	Review         string   `json:"review,omitempty"` // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED
	Requested      []string `json:"requested,omitempty"`
	Additions      int      `json:"additions,omitempty"`
	Deletions      int      `json:"deletions,omitempty"`
	Files          int      `json:"files,omitempty"`
	Commits        int      `json:"commits,omitempty"`
	Checks         string   `json:"checks,omitempty"` // SUCCESS, FAILURE, PENDING, ERROR, EXPECTED; "" for none
	FailingChecks  []string `json:"failingChecks,omitempty"`
	PassingChecks  int      `json:"passingChecks,omitempty"`
	RunningChecks  int      `json:"runningChecks,omitempty"`
	Mergeable      string   `json:"mergeable,omitempty"`
	HeadInFork     bool     `json:"headInFork,omitempty"`
	ReviewDecision string   `json:"-"`
}

// Ref is the item as weaver names it: owner/name#N.
func (it Item) Ref() string { return fmt.Sprintf("%s#%d", it.Repo, it.Number) }

// Result is a section's items, how many there are in all, and how asking
// for them went.
type Result struct {
	Section
	page.List[Item]
}

// Limit is how many items a section shows.
const Limit = 20

const prFields = `
fragment pr on PullRequest {
  number title body url state isDraft
  author { login }
  updatedAt
  repository { nameWithOwner }
  headRefName baseRefName isCrossRepository
  reviewDecision mergeable
  additions deletions changedFiles
  commits { totalCount }
  comments { totalCount }
  labels(first: 6) { nodes { name } }
  assignees(first: 3) { nodes { login } }
  reviewRequests(first: 10) { nodes { requestedReviewer { __typename ... on User { login } ... on Team { slug } ... on Bot { login } ... on Mannequin { login } } } }
  last: commits(last: 1) { nodes { commit { statusCheckRollup { state contexts(last: 100) { nodes { __typename ... on CheckRun { name status conclusion } ... on StatusContext { context state } } } } } } }
}`

const issueFields = `
fragment issue on Issue {
  number title body url state
  author { login }
  updatedAt
  repository { nameWithOwner }
  comments { totalCount }
  labels(first: 6) { nodes { name } }
  assignees(first: 3) { nodes { login } }
}`

// Query is the request for section i of kind k.
func Query(k Kind, i int) string {
	is, frag, fields := "is:pr", "pr", prFields
	if k == Issues {
		is, frag, fields = "is:issue", "issue", issueFields
	}
	s := Sections[k][i]
	search := func(alias, filters string) string {
		q, _ := json.Marshal(is + " archived:false " + filters)
		return fmt.Sprintf("  %s: search(type: ISSUE, first: %d, query: %s) { issueCount nodes { ...%s } }\n", alias, s.limit(), q, frag)
	}
	body := search("s", s.Filters)
	if s.Also != "" {
		body += search("t", s.Also)
	}
	return "query {\n" + body + "}\n" + fields + "\n"
}

type login struct {
	Login string `json:"login"`
}

type node struct {
	Number            int       `json:"number"`
	Title             string    `json:"title"`
	Body              string    `json:"body"`
	URL               string    `json:"url"`
	State             string    `json:"state"`
	IsDraft           bool      `json:"isDraft"`
	Author            *login    `json:"author"`
	UpdatedAt         time.Time `json:"updatedAt"`
	Repository        struct{ NameWithOwner string }
	HeadRefName       string `json:"headRefName"`
	BaseRefName       string `json:"baseRefName"`
	IsCrossRepository bool   `json:"isCrossRepository"`
	ReviewDecision    string `json:"reviewDecision"`
	Mergeable         string `json:"mergeable"`
	Additions         int    `json:"additions"`
	Deletions         int    `json:"deletions"`
	ChangedFiles      int    `json:"changedFiles"`
	Commits           struct{ TotalCount int }
	Comments          struct{ TotalCount int }
	Labels            struct{ Nodes []struct{ Name string } }
	Assignees         struct{ Nodes []login }
	ReviewRequests    struct {
		Nodes []struct {
			RequestedReviewer struct {
				Login string `json:"login"`
				Slug  string `json:"slug"`
			} `json:"requestedReviewer"`
		}
	} `json:"reviewRequests"`
	Last struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State    string
					Contexts struct {
						Nodes []struct {
							Typename   string `json:"__typename"`
							Name       string `json:"name"`
							Status     string `json:"status"`
							Conclusion string `json:"conclusion"`
							Context    string `json:"context"`
							State      string `json:"state"`
						}
					}
				} `json:"statusCheckRollup"`
			}
		}
	} `json:"last"`
}

func (n node) item(k Kind) Item {
	it := Item{Kind: k, Repo: n.Repository.NameWithOwner, Number: n.Number, Title: n.Title, Body: n.Body, URL: n.URL,
		State: n.State, UpdatedAt: n.UpdatedAt, Comments: n.Comments.TotalCount}
	if n.Author != nil {
		it.Author = n.Author.Login
	}
	for _, l := range n.Labels.Nodes {
		it.Labels = append(it.Labels, l.Name)
	}
	for _, a := range n.Assignees.Nodes {
		it.Assignees = append(it.Assignees, a.Login)
	}
	if k != PRs {
		return it
	}
	it.Draft, it.Head, it.Base, it.HeadInFork = n.IsDraft, n.HeadRefName, n.BaseRefName, n.IsCrossRepository
	it.Review, it.Mergeable = n.ReviewDecision, n.Mergeable
	it.Additions, it.Deletions, it.Files, it.Commits = n.Additions, n.Deletions, n.ChangedFiles, n.Commits.TotalCount
	for _, r := range n.ReviewRequests.Nodes {
		if who := r.RequestedReviewer.Login; who != "" {
			it.Requested = append(it.Requested, "@"+who)
		} else if r.RequestedReviewer.Slug != "" {
			it.Requested = append(it.Requested, r.RequestedReviewer.Slug)
		}
	}
	if len(n.Last.Nodes) > 0 {
		if r := n.Last.Nodes[0].Commit.StatusCheckRollup; r != nil {
			it.Checks = r.State
			for _, c := range r.Contexts.Nodes {
				name, outcome := c.Name, c.Conclusion
				if c.Typename == "StatusContext" {
					name, outcome = c.Context, c.State
				}
				switch {
				case c.Typename == "CheckRun" && c.Status != "COMPLETED", outcome == "PENDING", outcome == "EXPECTED":
					it.RunningChecks++
				case outcome == "SUCCESS", outcome == "NEUTRAL", outcome == "SKIPPED":
					it.PassingChecks++
				default:
					it.FailingChecks = append(it.FailingChecks, name)
				}
			}
		}
	}
	return it
}

// Parse reads the response to Query(k, i): that section's result.
func Parse(k Kind, i int, b []byte) (Result, error) {
	r := Result{Section: Sections[k][i]}
	type search struct {
		IssueCount int    `json:"issueCount"`
		Nodes      []node `json:"nodes"`
	}
	var resp struct {
		Data struct {
			S *search `json:"s"`
			T *search `json:"t"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		return r, fmt.Errorf("github: %w", err)
	}
	if resp.Data.S == nil {
		if len(resp.Errors) > 0 {
			return r, errors.New("github: " + resp.Errors[0].Message)
		}
		return r, errors.New("github: no search in the answer")
	}
	// The section's own search, then its second one's items it has not got
	// (an item can match both), most recently updated first; the total, as
	// many as both match, less those both returned.
	seen := map[string]bool{}
	for _, s := range []*search{resp.Data.S, resp.Data.T} {
		if s == nil {
			continue
		}
		r.Total += s.IssueCount
		for _, n := range s.Nodes {
			id := n.Repository.NameWithOwner + "#" + strconv.Itoa(n.Number)
			if n.Number <= 0 {
				continue
			}
			if seen[id] {
				r.Total--
				continue
			}
			seen[id] = true
			r.Items = append(r.Items, n.item(k))
		}
	}
	if resp.Data.T != nil {
		sort.SliceStable(r.Items, func(a, b int) bool { return r.Items[a].UpdatedAt.After(r.Items[b].UpdatedAt) })
	}
	r.Fetched = time.Now()
	return r, nil
}

// Fetch asks GitHub for every section of both kinds through gh, a request
// each, all at once (a search takes about a second; GitHub answers aliased
// ones in turn), within 30 s: each section's items, or why asking for it
// failed.
func Fetch() map[Kind][]Result {
	type answer struct {
		k Kind
		i int
		r Result
	}
	ch := make(chan answer)
	n := 0
	for _, k := range []Kind{PRs, Issues} {
		for i := range Sections[k] {
			n++
			go func() {
				b, err := gh(Query(k, i))
				a := answer{k: k, i: i}
				if err == nil {
					a.r, err = Parse(k, i, b)
				}
				if err != nil {
					a.r = Result{Section: Sections[k][i], List: page.Failed[Item](err)}
				}
				ch <- a
			}()
		}
	}
	out := map[Kind][]Result{PRs: make([]Result, len(Sections[PRs])), Issues: make([]Result, len(Sections[Issues]))}
	for ; n > 0; n-- {
		a := <-ch
		out[a.k][a.i] = a.r
	}
	return out
}

// gh runs a GraphQL query through gh, within 30 s.
func gh(query string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "gh", "api", "graphql", "-f", "query="+query)
	c.WaitDelay = time.Second
	var stderr strings.Builder
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if i := strings.IndexByte(msg, '\n'); i >= 0 {
			msg = msg[:i]
		}
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New("gh: " + msg)
	}
	return out, nil
}
