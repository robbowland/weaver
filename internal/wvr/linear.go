package wvr

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"slices"
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

// linCache is what Linear said last, in $STATE_DIR/linear.json: each
// section's issues, and how asking for them went.
type linCache struct {
	// Fetched is an older weaver's: when it asked for every section, which
	// is when its sections were read (settle).
	Fetched time.Time       `json:"fetched,omitzero"`
	Results []linear.Result `json:"results"`
}

// settle has an older weaver's cache say, section by section, when its
// sections were read.
func (c *linCache) settle() {
	for i := range c.Results {
		if c.Results[i].Fetched.IsZero() && c.Results[i].Err == "" {
			c.Results[i].Fetched = c.Fetched
		}
	}
	c.Fetched = time.Time{}
}

// refreshed is c with a refresh's answer: every section as asked, but one
// that failed with what that section (by its filter, wherever it was) had.
func (c *linCache) refreshed(got []linear.Result) {
	for i, r := range got {
		if j := slices.IndexFunc(c.Results, func(o linear.Result) bool { return o.Section == r.Section }); j >= 0 {
			got[i].List = r.List.Since(c.Results[j].List)
		}
	}
	c.Results = got
}

func (linearSource) service() string     { return "Linear" }
func (linearSource) name() string        { return "linear" }
func (linearSource) kinds() []string     { return []string{"issues"} }
func (linearSource) label(string) string { return "LINEAR ISSUES" }

func (linearSource) guide() viewGuide {
	return viewGuide{
		queue:    "yours to do: assigned to you, to do or in progress",
		mine:     "made by you, someone else's to do",
		watch:    "what you subscribe to otherwise",
		red:      "urgent",
		faint:    "not started yet",
		readOnly: "an investigation",
	}
}

func linCacheFile(e *Env) string { return e.StateDir + "/linear.json" }

func readLinear(e *Env) linCache {
	var c linCache
	bestEffort(store.Read(linCacheFile(e), &c))
	c.settle()
	return c
}

// updateLinear changes the cache as it is now, under its lock: a refresh
// and a change at once each keep the other's.
func updateLinear(e *Env, change func(*linCache)) {
	bestEffort(store.Update(linCacheFile(e), func(c *linCache) {
		c.settle()
		change(c)
	}))
}

func (linearSource) read(e *Env) itemData {
	c := readLinear(e)
	d := itemData{sections: map[string][]itemSection{}}
	for _, r := range c.Results {
		s := itemSection{title: r.Title, total: r.Total, more: r.Next != "", fetched: r.Fetched, err: r.Err}
		for _, it := range r.Items {
			s.items = append(s.items, linItem(it))
		}
		d.sections["issues"] = append(d.sections["issues"], s)
	}
	return d
}

// fetch asks Linear again and keeps the answer (refreshed).
func (linearSource) fetch(e *Env) {
	got := linear.Fetch()
	updateLinear(e, func(c *linCache) { c.refreshed(got) })
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
		key:     it.Identifier,
		terms:   append([]string{it.Team.Name, who(it.Assignee), who(it.Creator), it.BranchName}, it.Labels...),
	}
	v.terms = slices.DeleteFunc(v.terms, func(s string) bool { return s == "" })
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

// LinearLogin is `wvr linear login`: a personal API key, asked for at the
// terminal (a dot a character) until Linear takes one or you go back, and
// kept in the macOS Keychain. In a view's window (card) it is laid out as
// the view's setup card is.
func (e *Env) LinearLogin(card bool) error {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return errors.New("wvr linear login needs a terminal")
	}
	defer func() { _ = tty.Close() }()
	said := ""
	for {
		pad := ""
		if card {
			pad = placeCard(tty, 8)
		}
		fmt.Fprint(tty, cardRow(pad, "view", CBold+"LINEAR"+COff+" "+CDim+"·"+COff+" "+CGrey+"sign in with a personal API key"+COff))
		fmt.Fprint(tty, cardRow(pad, "make", "https://linear.app/settings/account/security"))
		fmt.Fprint(tty, cardRow(pad, "", CDim+"read access is enough; weaver keeps it in the Keychain"+COff))
		if said != "" {
			fmt.Fprint(tty, cardRow(pad, "", CRed+said+COff))
		}
		fmt.Fprint(tty, "\n"+cardKeys(pad, "⏎", "sign in", "esc", "back")+"\n")
		fmt.Fprint(tty, strings.TrimSuffix(cardRow(pad, "key", ""), "\n"))
		key, ok := readSecret(tty)
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return cli.ErrCancelled
		}
		name, org, err := linear.Viewer(key)
		if err != nil {
			said = "that key did not sign in: " + strings.TrimPrefix(err.Error(), "linear: ")
			if !card {
				fmt.Fprintln(tty)
			}
			continue
		}
		if err := linear.SaveKey(key); err != nil {
			return err
		}
		os.Remove(e.authFile("linear"))
		os.Remove(linCacheFile(e))
		if !card {
			fmt.Fprintf(tty, "signed in to %s as %s\n", org, name)
		}
		return nil
	}
}

// readSecret is a line typed at tty, not shown (a dot a character): enter
// ends it; esc or ^c goes back (false).
func readSecret(t *os.File) (string, bool) {
	fd := int(t.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		l, err := bufio.NewReader(t).ReadString('\n')
		return l, err == nil || l != ""
	}
	defer func() { _ = term.Restore(fd, old) }()
	var b []rune
	buf := make([]byte, 256)
	for {
		n, err := t.Read(buf)
		if err != nil || n == 0 {
			return "", false
		}
		for _, c := range string(buf[:n]) {
			switch {
			case c == '\r' || c == '\n':
				fmt.Fprint(t, "\r\n")
				return string(b), true
			case c == 0x1b || c == 0x03:
				fmt.Fprint(t, "\r\n")
				return "", false
			case c == 0x7f || c == 0x08:
				if len(b) > 0 {
					b = b[:len(b)-1]
					fmt.Fprint(t, "\b \b")
				}
			case c >= 0x20:
				b = append(b, c)
				fmt.Fprint(t, "•")
			}
		}
	}
}

// Linear is `wvr linear login`.
func (e *Env) Linear(args []string) error {
	if firstArg(args) == "login" {
		return e.LinearLogin(false)
	}
	return errors.New("usage: wvr linear login")
}
