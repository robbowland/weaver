package items

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/robbowland/weaver/internal/git"
	"github.com/robbowland/weaver/internal/linear"
)

// Reading work items (workitem.go): finding a repository's checkout, and
// fetching an item through gh or Linear's API.

var (
	reGHURL     = regexp.MustCompile(`^https?://github\.com/([\w.-]+/[\w.-]+)/(pull|issues)/(\d+)`)
	reGHRef     = regexp.MustCompile(`^([\w.-]+/[\w.-]+)#(\d+)$`)
	reLinearID  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*-\d+$`)
	reLinearURL = regexp.MustCompile(`^https?://linear\.app/[^/]+/issue/([A-Za-z][A-Za-z0-9]*-\d+)`)
	reGHRemote  = regexp.MustCompile(`github\.com[:/]([\w.-]+/[\w.-]+?)(?:\.git)?/?$`)
)

// GitHubRepo is the GitHub repository (owner/name) of checkout dir: its
// preferred remote's, else any remote's on GitHub. From the URLs as
// configured (an insteadOf rewrite is how it is reached, not what it is).
func GitHubRepo(dir string) string {
	out, err := exec.Command("git", "-C", dir, "config", "--get-regexp", `^remote\..*\.url$`).Output()
	if err != nil {
		return ""
	}
	pref := git.PreferredRemote(dir)
	first := ""
	for _, l := range strings.Split(string(out), "\n") {
		k, u, ok := strings.Cut(l, " ")
		if !ok {
			continue
		}
		m := reGHRemote.FindStringSubmatch(u)
		if m == nil {
			continue
		}
		if strings.TrimSuffix(strings.TrimPrefix(k, "remote."), ".url") == pref {
			return m[1]
		}
		if first == "" {
			first = m[1]
		}
	}
	return first
}

// Fetch reads the item ref names: through gh for GitHub (in dir, for
// a number without its repository), through Linear's API for Linear.
func Fetch(ref Ref, dir string) (Item, error) {
	switch r := ref.(type) {
	case GitHubRef:
		if r.Repo == "" {
			if r.Repo = GitHubRepo(dir); r.Repo == "" {
				return nil, fmt.Errorf("%s: not in a GitHub repository; say which (owner/name#%d)", r.Label(), r.Num)
			}
		}
		if r.Kind == KindPR {
			return fetchPR(r)
		}
		return fetchIssue(r)
	case LinearRef:
		return fetchLinear(r)
	}
	return nil, fmt.Errorf("no such work item: %v", ref)
}

// runJSON runs a command for its JSON output, at most 30 s; its error says
// what the command said.
func runJSON(name string, args ...string) ([]byte, error) {
	if _, err := exec.LookPath(name); err != nil {
		return nil, fmt.Errorf("%s is not installed", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, name, args...)
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
		return nil, fmt.Errorf("%s: %s", name, msg)
	}
	return out, nil
}

type ghComment struct {
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	Body string `json:"body"`
}

func ghNotes(cs []ghComment) []Note {
	var ns []Note
	for _, c := range cs {
		if strings.TrimSpace(c.Body) != "" {
			ns = append(ns, Note{Author: c.Author.Login, Body: c.Body})
		}
	}
	return ns
}

func fetchPR(r GitHubRef) (Item, error) {
	out, err := runJSON("gh", "pr", "view", strconv.Itoa(r.Num), "-R", r.Repo, "--json",
		"number,title,body,url,author,state,headRefName,baseRefName,isCrossRepository,comments")
	if err != nil {
		return nil, err
	}
	var p struct {
		Title, Body, URL, State  string
		HeadRefName, BaseRefName string
		IsCrossRepository        bool
		Author                   struct{ Login string }
		Comments                 []ghComment
	}
	if err := json.Unmarshal(out, &p); err != nil {
		return nil, fmt.Errorf("gh: %v", err)
	}
	it := PullRequest{Repo: r.Repo, Num: r.Num, Head: p.HeadRefName, Base: p.BaseRefName, Fork: p.IsCrossRepository,
		Text: Text{Title: p.Title, Body: p.Body, URL: p.URL, State: p.State, Author: p.Author.Login, Comments: ghNotes(p.Comments)}}
	// Review comments are in threads, which gh pr view leaves out: the
	// unresolved ones, as the author has yet to answer them.
	owner, name, _ := strings.Cut(r.Repo, "/")
	q := `query($o:String!,$r:String!,$n:Int!){repository(owner:$o,name:$r){PullRequest(number:$n){reviewThreads(first:100){nodes{isResolved isOutdated path line comments(first:30){nodes{author{login} body}}}}}}}`
	if out, err := runJSON("gh", "api", "graphql", "-f", "query="+q, "-F", "o="+owner, "-F", "r="+name, "-F", "n="+strconv.Itoa(r.Num)); err == nil {
		var g struct {
			Data struct {
				Repository struct {
					PullRequest struct {
						ReviewThreads struct {
							Nodes []struct {
								IsResolved, IsOutdated bool
								Path                   string
								Line                   int
								Comments               struct{ Nodes []ghComment }
							}
						}
					}
				}
			}
		}
		if json.Unmarshal(out, &g) == nil {
			for _, t := range g.Data.Repository.PullRequest.ReviewThreads.Nodes {
				if t.IsResolved {
					continue
				}
				w := t.Path
				if t.Line > 0 {
					w += ":" + strconv.Itoa(t.Line)
				}
				if t.IsOutdated {
					w += " (outdated)"
				}
				for _, n := range ghNotes(t.Comments.Nodes) {
					n.Where = w
					it.Review = append(it.Review, n)
				}
			}
		}
	}
	return it, nil
}

func fetchIssue(r GitHubRef) (Item, error) {
	out, err := runJSON("gh", "issue", "view", strconv.Itoa(r.Num), "-R", r.Repo, "--json",
		"number,title,body,url,author,state,comments")
	if err != nil {
		return nil, err
	}
	var p struct {
		Title, Body, URL, State string
		Author                  struct{ Login string }
		Comments                []ghComment
	}
	if err := json.Unmarshal(out, &p); err != nil {
		return nil, fmt.Errorf("gh: %v", err)
	}
	return GitHubIssue{Repo: r.Repo, Num: r.Num, Text: Text{Title: p.Title, Body: p.Body, URL: p.URL,
		State: p.State, Author: p.Author.Login, Comments: ghNotes(p.Comments)}}, nil
}

func fetchLinear(r LinearRef) (Item, error) {
	p, err := linear.Get(r.ID)
	if err != nil {
		return nil, err
	}
	it := LinearIssue{ID: p.Identifier, Branch: p.BranchName, Text: Text{Title: p.Title, URL: p.URL, Body: p.Description,
		State: p.State.Name}}
	if p.Creator != nil {
		it.Author = p.Creator.Name
	}
	if it.ID == "" {
		it.ID = r.ID
	}
	for _, c := range p.Comments {
		if strings.TrimSpace(c.Body) != "" {
			it.Comments = append(it.Comments, Note{Author: c.Author, Body: c.Body})
		}
	}
	return it, nil
}
