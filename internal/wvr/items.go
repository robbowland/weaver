package wvr

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Reading work items (workitem.go): finding a repository's checkout, and
// fetching an item through gh or linear-tui.

var (
	reGHURL     = regexp.MustCompile(`^https?://github\.com/([\w.-]+/[\w.-]+)/(pull|issues)/(\d+)`)
	reGHRef     = regexp.MustCompile(`^([\w.-]+/[\w.-]+)#(\d+)$`)
	reLinearID  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*-\d+$`)
	reLinearURL = regexp.MustCompile(`^https?://linear\.app/[^/]+/issue/([A-Za-z][A-Za-z0-9]*-\d+)`)
	reGHRemote  = regexp.MustCompile(`github\.com[:/]([\w.-]+/[\w.-]+?)(?:\.git)?/?$`)
)

// ghRepo is the GitHub repository (owner/name) of checkout dir: its
// preferred remote's, else any remote's on GitHub. From the URLs as
// configured (an insteadOf rewrite is how it is reached, not what it is).
func ghRepo(dir string) string {
	out, err := exec.Command("git", "-C", dir, "config", "--get-regexp", `^remote\..*\.url$`).Output()
	if err != nil {
		return ""
	}
	pref := preferredRemote(dir)
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

// checkoutFor is the main checkout of GitHub repository owner/name, from
// those weaver knows: first (the one you are in), the repositories of
// recent threads and agents, and those zoxide knows of the same name. ""
// when none is.
func (e *Env) checkoutFor(repo, first string) string {
	name := strings.ToLower(repo[strings.IndexByte(repo, '/')+1:])
	var named, rest []string
	for _, l := range strings.Split(e.recentRepos(first), "\n") {
		d, _, _ := strings.Cut(l, "\t")
		if d == "" {
			continue
		}
		if strings.ToLower(filepath.Base(d)) == name {
			named = append(named, d)
		} else {
			rest = append(rest, d)
		}
	}
	if h := os.Getenv("HOME"); h != "" {
		for _, p := range []string{"code", "src", "dev", "projects"} {
			if d := filepath.Join(h, p, repo[strings.IndexByte(repo, '/')+1:]); exists(d + "/.git") {
				named = append(named, d)
			}
		}
	}
	// zoxide's directories of another name are many: only weaver's own.
	for i, d := range append(named, rest...) {
		if i >= len(named) && i-len(named) >= 20 {
			break
		}
		if strings.EqualFold(ghRepo(d), repo) {
			if r, _, ok := repoOf(d); ok {
				return r
			}
		}
	}
	return ""
}

// fetchItem reads the item ref names: through gh for GitHub (in dir, for
// a number without its repository), through linear-tui for Linear.
func fetchItem(ref itemRef, dir string) (workItem, error) {
	switch r := ref.(type) {
	case githubRef:
		if r.Repo == "" {
			if r.Repo = ghRepo(dir); r.Repo == "" {
				return nil, fmt.Errorf("%s: not in a GitHub repository; say which (owner/name#%d)", r.label(), r.Num)
			}
		}
		if r.Kind == ghPR {
			return fetchPR(r)
		}
		return fetchIssue(r)
	case linearRef:
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
	c := exec.Command(name, args...)
	var stderr strings.Builder
	c.Stderr = &stderr
	timer := time.AfterFunc(30*time.Second, func() {
		if c.Process != nil {
			c.Process.Kill()
		}
	})
	defer timer.Stop()
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

func ghNotes(cs []ghComment) []itemNote {
	var ns []itemNote
	for _, c := range cs {
		if strings.TrimSpace(c.Body) != "" {
			ns = append(ns, itemNote{Author: c.Author.Login, Body: c.Body})
		}
	}
	return ns
}

func fetchPR(r githubRef) (workItem, error) {
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
	it := pullRequest{Repo: r.Repo, Num: r.Num, Head: p.HeadRefName, Base: p.BaseRefName, Fork: p.IsCrossRepository,
		itemText: itemText{Title: p.Title, Body: p.Body, URL: p.URL, State: p.State, Author: p.Author.Login, Comments: ghNotes(p.Comments)}}
	// Review comments are in threads, which gh pr view leaves out: the
	// unresolved ones, as the author has yet to answer them.
	owner, name, _ := strings.Cut(r.Repo, "/")
	q := `query($o:String!,$r:String!,$n:Int!){repository(owner:$o,name:$r){pullRequest(number:$n){reviewThreads(first:100){nodes{isResolved isOutdated path line comments(first:30){nodes{author{login} body}}}}}}}`
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

func fetchIssue(r githubRef) (workItem, error) {
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
	return githubIssue{Repo: r.Repo, Num: r.Num, itemText: itemText{Title: p.Title, Body: p.Body, URL: p.URL,
		State: p.State, Author: p.Author.Login, Comments: ghNotes(p.Comments)}}, nil
}

// person is a name from linear-tui's JSON, a string or an object with one.
func person(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case map[string]any:
		for _, k := range []string{"name", "display_name", "displayName", "email"} {
			if s, ok := x[k].(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func fetchLinear(r linearRef) (workItem, error) {
	out, err := runJSON("linear-tui", "issue", "show", r.ID, "--json")
	if err != nil {
		return nil, err
	}
	var p struct {
		Identifier, Title, URL, Description string
		BranchName                          string `json:"branch_name"`
		State                               struct{ Name string }
		Creator                             any
		Comments                            []struct {
			Author any
			Body   string
		}
	}
	if err := json.Unmarshal(out, &p); err != nil {
		return nil, fmt.Errorf("linear-tui: %v", err)
	}
	it := linearIssue{ID: r.ID, Branch: p.BranchName, itemText: itemText{Title: p.Title, URL: p.URL, Body: p.Description,
		State: p.State.Name, Author: person(p.Creator)}}
	if p.Identifier != "" {
		it.ID = p.Identifier
	}
	for _, c := range p.Comments {
		if strings.TrimSpace(c.Body) != "" {
			it.Comments = append(it.Comments, itemNote{Author: person(c.Author), Body: c.Body})
		}
	}
	return it, nil
}

// threadOnBranch is the agent pane of a running thread of this session on
// branch of repo, or "".
func (e *Env) threadOnBranch(repo, branch string) string {
	live := map[string]bool{}
	ps, _ := e.Panes()
	for _, p := range ps {
		if !p.Exited {
			live[strconv.Itoa(p.ID)] = true
		}
	}
	for _, f := range e.SessionFiles("thread") {
		t, err := readThread(f)
		if err != nil || t.Repo != repo || t.Branch != branch {
			continue
		}
		base := strings.TrimSuffix(f, ".thread")
		if p := base[strings.LastIndexByte(base, '_')+1:]; live[p] {
			return p
		}
	}
	return ""
}
