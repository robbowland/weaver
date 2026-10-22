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
// IssueFilter, as GraphQL).
type Section struct {
	Title  string `json:"title"`
	Filter string `json:"filter"`
}

// open is an issue not done: triage, backlog, unstarted or started.
const open = `state: { type: { nin: ["completed", "canceled"] } }`

// Sections are weaver's: what is yours to do, what you made, and what you
// follow.
var Sections = []Section{
	{"Queue", `{ assignee: { isMe: { eq: true } }, state: { type: { in: ["unstarted", "started"] } } }`},
	{"Mine", `{ creator: { isMe: { eq: true } }, assignee: { isMe: { neq: true } }, ` + open + ` }`},
	{"Watch", `{ subscribers: { some: { isMe: { eq: true } } }, creator: { isMe: { neq: true } }, assignee: { isMe: { neq: true } }, ` + open + ` }`},
}

// Limit is how many issues a section shows.
const Limit = 30

// Result is a section's issues, and how asking for them went.
type Result struct {
	Section
	page.List[Issue]
}

// Query is section s's request: its page after cursor $after (null: its
// first), and whether there is another (Linear does not count a list's
// issues).
func Query(s Section) string {
	return fmt.Sprintf("query Section($after: String) { issues(filter: %s, first: %d, after: $after, orderBy: updatedAt) { nodes { %s } pageInfo { hasNextPage endCursor } } }",
		s.Filter, Limit, issueFields)
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
	return ask(key, s, after)
}

// ask is Ask, signed in as key.
func ask(key string, s Section, after string) Result {
	var d struct {
		Issues struct {
			Nodes    []node   `json:"nodes"`
			PageInfo pageInfo `json:"pageInfo"`
		} `json:"issues"`
	}
	vars := map[string]any{"after": nil}
	if after != "" {
		vars["after"] = after
	}
	r := Result{Section: s}
	if err := query(key, Query(s), vars, &d); err != nil {
		r.List = page.Failed[Issue](err)
		return r
	}
	for _, n := range d.Issues.Nodes {
		r.Items = append(r.Items, n.issue())
	}
	r.Next, r.Fetched = d.Issues.PageInfo.next(), time.Now()
	return r
}

// Fetch asks Linear for every section, a request each, at once: each
// section's issues, or why asking for it failed (every one, without a key).
func Fetch() []Result {
	out := make([]Result, len(Sections))
	key, err := Key()
	if err != nil {
		for i, s := range Sections {
			out[i] = Result{Section: s, List: page.Failed[Issue](err)}
		}
		return out
	}
	type answer struct {
		i int
		r Result
	}
	ch := make(chan answer)
	for i := range Sections {
		go func() { ch <- answer{i, ask(key, Sections[i], "")} }()
	}
	for range Sections {
		a := <-ch
		out[a.i] = a.r
	}
	return out
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
