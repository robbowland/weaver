package wvr

import (
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/robbowland/weaver/internal/session"
)

// A view's search (/) is fzf's fuzzy match on each item's search line (its
// key, whole title and terms), best first. A query that reads as a key
// (45, #45, o/r#45, ENG-42) would match fuzzily all that has its digits:
// fzf searches for it exactly instead, the items it names first.

// viewLine is a line of a view (itemRows): its id, the ref fzf tracks it
// by, what it shows, and its search line.
type viewLine struct{ id, ref, text, search string }

var reKey = regexp.MustCompile(`^(#?\d+|[\w.-]+/[\w.-]+#\d+|[A-Za-z][A-Za-z0-9]*-\d+)$`)

// keyQuery: does q read as an item's key: a number (45, #45), a reference
// (o/r#45) or an identifier (ENG-42)?
func keyQuery(q string) bool { return reKey.MatchString(strings.TrimSpace(q)) }

// names: is it the item key q names? Its reference or key in any case, or
// its number: #45's, ENG-45's.
func (it viewItem) names(q string) bool {
	q = strings.TrimSpace(q)
	if strings.EqualFold(q, it.ref) || strings.EqualFold(q, it.key) {
		return true
	}
	n := strings.TrimPrefix(q, "#")
	return digits(n) && (it.key == "#"+n || strings.HasSuffix(it.key, "-"+n))
}

// keySearch is fzf's search for key q among its: the keys of the items q
// names, each at the start of a search line (a longer match there than
// anywhere else, so ranked first), or q exactly anywhere.
func keySearch(its []viewItem, q string) string {
	var terms []string
	seen := map[string]bool{}
	for _, it := range its {
		if it.names(q) && !seen[it.key] {
			seen[it.key] = true
			terms = append(terms, "^"+it.key+`\ `)
		}
	}
	return strings.Join(append(terms, "'"+strings.TrimSpace(q)), " | ")
}

// itemQuery is what fzf does as the query changes to q: none, the rows as
// they are; a key, fzf's search for it (keySearch) on the search line, and
// for a whole id nothing loaded has, a lookup behind (itemLookup); any other
// query, fzf's own search for it there.
func (e *Env) itemQuery(src itemSource, q string) string {
	switch {
	case strings.TrimSpace(q) == "":
		return "change-with-nth(5)\n"
	case keyQuery(q):
		var its []viewItem
		for _, s := range src.read(e).sections[e.itemKind(src)] {
			its = append(its, s.items...)
		}
		if lookupWanted(src, its, q) {
			e.itemLookupBehind(src, strings.TrimSpace(q))
		}
		return "change-with-nth(6)+search(" + keySearch(its, q) + ")\n"
	}
	return "change-with-nth(6)\n"
}

// lookupWanted: should / look q up with the service: a whole id of its
// (isID) that no item loaded has? What a view loads is a few sections'
// first pages: an item it names exactly is no less there for that.
func lookupWanted(src itemSource, its []viewItem, q string) bool {
	q = strings.TrimSpace(q)
	return src.isID(q) && !slices.ContainsFunc(its, func(it viewItem) bool { return it.names(q) })
}

// lookupPause is how long the query must stay a whole id before / looks it
// up: typing it (ENG-4, ENG-42) asks once, for the one it ends on.
var lookupPause = 600 * time.Millisecond

// itemLookupBehind looks up id q behind the view (`_items NAME lookup q`).
func (e *Env) itemLookupBehind(src itemSource, q string) {
	c := exec.Command(e.Self, "_items", src.name(), "lookup", q)
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	bestEffort(c.Start())
}

// itemLookup is / looking up id q, not loaded: once typing has stopped on it
// (still the view's query after lookupPause), what ctrl+s does (itemAsk):
// the view's / filter would hide what it found.
func (e *Env) itemLookup(src itemSource, q string) {
	time.Sleep(lookupPause)
	if cur, ok := session.FzfQuery(os.Getenv("WEAVER_VIEW_SOCK")); !ok || strings.TrimSpace(cur) != q {
		return
	}
	e.itemAsk(src, session.Quote(e.Self)+" _items "+src.name(), q)
}
