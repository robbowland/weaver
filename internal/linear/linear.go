// Package linear reads the Linear issues weaver's LINEAR view shows, and
// the one a thread starts from, through Linear's GraphQL API, signed in with
// a personal API key ($LINEAR_API_KEY, else the one kept in the macOS
// Keychain by `wvr linear login`).
//
// The fields an issue is read with, and how a list of them is asked for,
// come from linear-tui (https://github.com/k1-c/linear-tui, MIT, Copyright
// (c) 2026 k1-c): its ISSUE_FIELDS, its issues(filter:, orderBy: updatedAt)
// lists and its viewer query. Here the sections filter on isMe, so no
// request for the viewer comes first, and they are asked for at once.
package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/robbowland/weaver/internal/page"
)

// endpoint is Linear's API ($WEAVER_LINEAR_URL in tests).
func endpoint() string {
	if u := os.Getenv("WEAVER_LINEAR_URL"); u != "" {
		return u
	}
	return "https://api.linear.app/graphql"
}

// keychainService is the Keychain item weaver keeps the key in.
const keychainService = "weaver.linear"

// ErrNoKey says weaver has no Linear key: `wvr linear login` keeps one.
var ErrNoKey = errors.New("no Linear API key (wvr linear login)")

// Key is the API key weaver signs in to Linear with: $LINEAR_API_KEY, else
// the Keychain's.
func Key() (string, error) {
	if k := strings.TrimSpace(os.Getenv("LINEAR_API_KEY")); k != "" {
		return k, nil
	}
	out, err := exec.Command("security", "find-generic-password", "-s", keychainService, "-w").Output()
	if k := strings.TrimSpace(string(out)); err == nil && k != "" {
		return k, nil
	}
	return "", ErrNoKey
}

// SaveKey keeps key in the Keychain, through security's own prompt-less
// command mode, so the key is never on a command line.
func SaveKey(key string) error {
	if strings.ContainsAny(key, " \t\n\"'\\") {
		return errors.New("that is not an API key")
	}
	c := exec.Command("security", "-i")
	c.Stdin = strings.NewReader("add-generic-password -U -s " + keychainService + " -a linear -w " + key + "\n")
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("security: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// query runs a GraphQL query with variables, as key, into v (its data).
func query(key, q string, vars map[string]any, v any) error {
	body, err := json.Marshal(map[string]any{"query": q, "variables": vars})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("linear: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var r struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("linear: %s", resp.Status)
	}
	if len(r.Errors) > 0 && (len(r.Data) == 0 || string(r.Data) == "null") {
		return errors.New("linear: " + r.Errors[0].Message)
	}
	if resp.StatusCode != http.StatusOK && len(r.Data) == 0 {
		return fmt.Errorf("linear: %s", resp.Status)
	}
	return json.Unmarshal(r.Data, v)
}

// Viewer is who key signs in as: their name, and their workspace's.
func Viewer(key string) (string, string, error) {
	var d struct {
		Viewer struct {
			Name         string
			Organization struct{ Name string }
		}
	}
	err := query(key, "query Viewer { viewer { name organization { name } } }", nil, &d)
	return d.Viewer.Name, d.Viewer.Organization.Name, err
}

// Issue is a Linear issue, as the view, its card and a thread's prompt
// read it.
type Issue struct {
	Identifier    string    `json:"identifier"`
	Title         string    `json:"title"`
	Description   string    `json:"description"`
	URL           string    `json:"url"`
	BranchName    string    `json:"branchName"`
	Priority      int       `json:"priority"` // 0 none, 1 urgent, 2 high, 3 medium, 4 low
	PriorityLabel string    `json:"priorityLabel"`
	State         State     `json:"state"`
	Team          Team      `json:"team"`
	Assignee      *Person   `json:"assignee"`
	Creator       *Person   `json:"creator"`
	Labels        []string  `json:"labels"`
	Project       string    `json:"project"`
	UpdatedAt     time.Time `json:"updatedAt"`
	Comments      []Comment `json:"comments,omitempty"`
}

// State is an issue's workflow state: its name, and its type (triage,
// backlog, unstarted, started, completed, canceled).
type State struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Team is the team an issue is in.
type Team struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// Person is someone in Linear.
type Person struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

// Comment is a comment on an issue.
type Comment struct {
	Author string `json:"author"`
	Body   string `json:"body"`
}

// issueFields are linear-tui's ISSUE_FIELDS, less what weaver does not
// show.
const issueFields = `
  identifier title description url branchName
  priority priorityLabel
  state { name type }
  team { key name }
  assignee { name displayName }
  creator { name displayName }
  labels { nodes { name } }
  project { name }
  updatedAt`

// node is an issue as the API sends it.
type node struct {
	Issue
	Labels  struct{ Nodes []struct{ Name string } } `json:"labels"`
	Project *struct{ Name string }                  `json:"project"`
	Comment struct {
		Nodes []struct {
			Body string
			User *Person
		}
	} `json:"comments"`
}

func (n node) issue() Issue {
	it := n.Issue
	it.Labels = nil
	for _, l := range n.Labels.Nodes {
		it.Labels = append(it.Labels, l.Name)
	}
	if n.Project != nil {
		it.Project = n.Project.Name
	}
	for _, c := range n.Comment.Nodes {
		who := ""
		if c.User != nil {
			who = c.User.DisplayName
		}
		it.Comments = append(it.Comments, Comment{Author: who, Body: c.Body})
	}
	return it
}

// Section is one list of issues: its title and its filter (an
// IssueFilter, as GraphQL); or, with a term, what Linear's search finds for
// it (within the filter, if any).
type Section struct {
	Title  string `json:"title"`
	Filter string `json:"filter"`
	Term   string `json:"term,omitempty"`
}

// open is an issue not done: triage, backlog, unstarted or started.
const open = `state: { type: { nin: ["completed", "canceled"] } }`

// openStates are the state types of an issue not done (open).
var openStates = []string{"triage", "backlog", "unstarted", "started"}

// role is how an open issue involves you, each in exactly one: assigned to
// you; else made by you; else followed (subscribed to).
type role int

const (
	assigned role = iota
	made
	followed
)

func (r role) String() string { return [...]string{"assigned", "made", "followed"}[r] }

// who is the IssueFilter's fields for issues of role r.
func (r role) who() string {
	switch r {
	case made:
		return `creator: { isMe: { eq: true } }, assignee: { isMe: { neq: true } }`
	case followed:
		return `subscribers: { some: { isMe: { eq: true } } }, creator: { isMe: { neq: true } }, assignee: { isMe: { neq: true } }`
	case assigned:
	}
	return `assignee: { isMe: { eq: true } }`
}

// list is one of weaver's sections: the issues of a role in some states.
type list struct {
	title  string
	role   role
	states []string
}

// filter is l as an IssueFilter.
func (l list) filter() string {
	if slices.Equal(l.states, openStates) {
		return "{ " + l.role.who() + ", " + open + " }"
	}
	return "{ " + l.role.who() + `, state: { type: { in: ["` + strings.Join(l.states, `", "`) + `"] } } }`
}

// lists are weaver's sections, every open issue that involves you in one:
// what is yours to do, yours for later, what you made, and what you follow.
var lists = []list{
	{"Queue", assigned, []string{"unstarted", "started"}},
	{"Backlog", assigned, []string{"triage", "backlog"}},
	{"Mine", made, openStates},
	{"Watch", followed, openStates},
}

// Sections are lists as the views ask for them.
var Sections = func() []Section {
	ss := make([]Section, len(lists))
	for i, l := range lists {
		ss[i] = Section{Title: l.title, Filter: l.filter()}
	}
	return ss
}()

// Limit is how many issues a section shows.
const Limit = 30

// pageMax is the most issues Linear answers a request with.
const pageMax = 250

// again is how many issues to ask a section's first page for again, held
// as it is: as many as it holds (its pages read since), at least Limit, at
// most one request's.
func again(held int) int { return min(max(held, Limit), pageMax) }

// Result is a section's issues, and how asking for them went.
type Result struct {
	Section
	page.List[Issue]
}

// Query is section s's request: its page of n issues after cursor $after
// (null: its first), and whether there is another (Linear does not count a
// list's issues, but counts what its search finds); a term's, $term.
func Query(s Section, n int) string {
	if s.Term != "" {
		filter := ""
		if s.Filter != "" {
			filter = ", filter: " + s.Filter
		}
		return fmt.Sprintf("query Search($after: String, $term: String!) { searchIssues(term: $term%s, first: %d, after: $after) { nodes { %s } pageInfo { hasNextPage endCursor } totalCount } }",
			filter, n, issueFields)
	}
	return fmt.Sprintf("query Section($after: String) { issues(filter: %s, first: %d, after: $after, orderBy: updatedAt) { nodes { %s } pageInfo { hasNextPage endCursor } } }",
		s.Filter, n, issueFields)
}

// pageInfo is where a list's next page starts, if it has one.
type pageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

// next is the cursor of the page after, "" when there is none.
func (p pageInfo) next() string {
	if p.HasNextPage {
		return p.EndCursor
	}
	return ""
}

// Ask asks Linear for section s, its page after cursor after ("": its
// first): what it found, or why asking failed.
func Ask(s Section, after string) Result {
	key, err := Key()
	if err != nil {
		return Result{Section: s, List: page.Failed[Issue](err)}
	}
	return ask(key, s, after, Limit)
}

// ask is Ask, signed in as key, a page of n issues.
func ask(key string, s Section, after string, n int) Result {
	type list struct {
		Nodes      []node   `json:"nodes"`
		PageInfo   pageInfo `json:"pageInfo"`
		TotalCount float64  `json:"totalCount"`
	}
	var d struct {
		Issues *list `json:"issues"`
		Search *list `json:"searchIssues"`
	}
	vars := map[string]any{"after": nil}
	if after != "" {
		vars["after"] = after
	}
	if s.Term != "" {
		vars["term"] = s.Term
	}
	r := Result{Section: s}
	if err := query(key, Query(s, n), vars, &d); err != nil {
		r.List = page.Failed[Issue](err)
		return r
	}
	l := d.Issues
	if s.Term != "" {
		l = d.Search
	}
	if l == nil {
		r.List = page.Failed[Issue](errors.New("linear: no issues in the answer"))
		return r
	}
	for _, n := range l.Nodes {
		r.Items = append(r.Items, n.issue())
	}
	r.Total, r.Next, r.Fetched = int(l.TotalCount), l.PageInfo.next(), time.Now()
	return r
}

// Fetch asks Linear for every section, and for each of search (a search
// of your own), a request each, at once: each one's issues, or why asking
// for it failed (every one, without a key); the sections, then the
// searches. Each first page as deep as held says it is held (again).
func Fetch(held func(Section) int, search ...Section) ([]Result, []Result) {
	asked := append(append([]Section{}, Sections...), search...)
	out := make([]Result, len(asked))
	key, err := Key()
	if err != nil {
		for i, s := range asked {
			out[i] = Result{Section: s, List: page.Failed[Issue](err)}
		}
		return out[:len(Sections)], out[len(Sections):]
	}
	type answer struct {
		i int
		r Result
	}
	ch := make(chan answer)
	for i, s := range asked {
		go func() { ch <- answer{i, ask(key, s, "", again(held(s)))} }()
	}
	for range asked {
		a := <-ch
		out[a.i] = a.r
	}
	return out[:len(Sections)], out[len(Sections):]
}

// Filters are the searches of your own the views offer before a team's.
var Filters = []Section{
	{Title: "assigned to me", Filter: `{ assignee: { isMe: { eq: true } }, ` + open + ` }`},
	{Title: "created by me", Filter: `{ creator: { isMe: { eq: true } }, ` + open + ` }`},
	{Title: "subscribed to", Filter: `{ subscribers: { some: { isMe: { eq: true } } }, ` + open + ` }`},
}

// TeamFilter is what is open in the team of key (ENG), called name.
func TeamFilter(key, name string) Section {
	k, _ := json.Marshal(key)
	return Section{Title: "team " + name, Filter: `{ team: { key: { eq: ` + string(k) + ` } }, ` + open + ` }`}
}

// Words is what Linear's search finds for words.
func Words(words string) Section {
	w := strings.Join(strings.Fields(words), " ")
	return Section{Title: w, Term: w}
}

// Get is issue id (ENG-42) with its comments, for a thread's prompt.
func Get(id string) (Issue, error) {
	key, err := Key()
	if err != nil {
		return Issue{}, err
	}
	var d struct {
		Issue *node `json:"issue"`
	}
	q := "query Issue($id: String!) { issue(id: $id) { " + issueFields + " comments(first: 50) { nodes { body user { name displayName } } } } }"
	if err := query(key, q, map[string]any{"id": id}, &d); err != nil {
		return Issue{}, err
	}
	if d.Issue == nil {
		return Issue{}, fmt.Errorf("linear: no issue %s", id)
	}
	return d.Issue.issue(), nil
}
