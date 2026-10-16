package wvr

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/robbowland/weaver/internal/cli"
	"github.com/robbowland/weaver/internal/linear"
	"github.com/robbowland/weaver/internal/store"
)

// The LINEAR view's source: the Linear issues of weaver's sections
// (internal/linear, after linear-tui).

type linearSource struct{}

// linCache is what Linear said last, in $STATE_DIR/linear.json.
type linCache struct {
	Fetched time.Time       `json:"fetched"`
	Err     string          `json:"err,omitempty"`
	Results []linear.Result `json:"results"`
}

func (linearSource) name() string        { return "linear" }
func (linearSource) kinds() []string     { return []string{"issues"} }
func (linearSource) label(string) string { return "LINEAR ISSUES" }

func linCacheFile(e *Env) string { return e.StateDir + "/linear.json" }

func readLinear(e *Env) linCache {
	var c linCache
	bestEffort(store.Read(linCacheFile(e), &c))
	return c
}

func (linearSource) read(e *Env) itemData {
	c := readLinear(e)
	d := itemData{fetched: c.Fetched, err: c.Err, sections: map[string][]itemSection{}}
	for _, r := range c.Results {
		s := itemSection{title: r.Title, total: len(r.Items)}
		for _, it := range r.Items {
			s.items = append(s.items, linItem(it))
		}
		d.sections["issues"] = append(d.sections["issues"], s)
	}
	return d
}

// fetch asks Linear again and keeps the answer: a section that failed keeps
// what it had.
func (linearSource) fetch(e *Env) {
	old := readLinear(e)
	res, err := linear.Fetch()
	c := linCache{Fetched: time.Now(), Results: res}
	if err != nil {
		c.Err = err.Error()
		if errors.Is(err, linear.ErrNoKey) {
			c.Results = old.Results
		}
		for i, r := range c.Results {
			if len(r.Items) == 0 && i < len(old.Results) {
				c.Results[i] = old.Results[i]
			}
		}
	}
	bestEffort(store.Write(linCacheFile(e), c))
}

// linPriority is an issue's priority as a signal: urgent and high stand out.
func linPriority(it linear.Issue) string {
	switch it.Priority {
	case 1:
		return CRed + "urgent" + COff
	case 2:
		return CGrey + "high" + COff
	}
	return ""
}

// linFact is the one thing to know about an issue: urgent, else its state.
func linFact(it linear.Issue) string {
	if it.Priority == 1 {
		return CRed + "urgent" + COff
	}
	if it.State.Type == "started" {
		return CGrey + strings.ToLower(it.State.Name) + COff
	}
	return CDim + strings.ToLower(it.State.Name) + COff
}

func who(p *linear.Person) string {
	switch {
	case p == nil:
		return ""
	case p.DisplayName != "":
		return "@" + p.DisplayName
	}
	return p.Name
}

// linItem is a Linear issue as the view shows it.
func linItem(it linear.Issue) viewItem {
	v := viewItem{
		ref:     it.Identifier,
		title:   untick(it.Title),
		fact:    linFact(it),
		where:   it.Identifier,
		sub:     it.Identifier + " · " + it.Team.Name + " · " + strings.ToLower(it.State.Name),
		updated: it.UpdatedAt,
		url:     it.URL,
		from:    []string{"linear", it.Identifier},
		branch:  it.BranchName,
		startOn: it.BranchName,
		body:    it.Description,
	}
	f := func(label, text, colour string) {
		if text != "" {
			v.fields = append(v.fields, cardField{label, text, colour, 1})
		}
	}
	f("priority", linPriority(it), "")
	f("assigned", who(it.Assignee), CGrey)
	f("by", who(it.Creator), CGrey)
	f("project", it.Project, CGrey)
	f("labels", strings.Join(it.Labels, ", "), CGrey)
	return v
}

// LinearLogin is `wvr linear login`: a personal API key, asked for (it
// does not show as you paste it) until Linear takes one or you give none,
// and kept in the macOS Keychain.
func (e *Env) LinearLogin() error {
	fmt.Println("weaver reads Linear with a personal API key: make one at")
	fmt.Println(CGrey + "  https://linear.app/settings/account/security" + COff)
	fmt.Println("(read access is enough), and paste it here. " + CDim + "Nothing cancels." + COff)
	in := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("\nkey: ")
		var key string
		if fd := int(os.Stdin.Fd()); term.IsTerminal(fd) {
			b, err := term.ReadPassword(fd)
			fmt.Println()
			if err != nil {
				return err
			}
			key = string(b)
		} else {
			key, _ = in.ReadString('\n')
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return cli.ErrCancelled
		}
		name, org, err := linear.Viewer(key)
		if err != nil {
			fmt.Println(CRed + "that key did not sign in: " + strings.TrimPrefix(err.Error(), "linear: ") + COff)
			continue
		}
		if err := linear.SaveKey(key); err != nil {
			return err
		}
		os.Remove(e.authFile("linear"))
		os.Remove(linCacheFile(e))
		fmt.Printf("signed in to %s as %s\n", org, name)
		return nil
	}
}

// Linear is `wvr linear login`.
func (e *Env) Linear(args []string) error {
	if firstArg(args) == "login" {
		return e.LinearLogin()
	}
	return errors.New("usage: wvr linear login")
}
