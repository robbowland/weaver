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
