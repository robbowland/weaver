package wvr

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/items"
	"github.com/robbowland/weaver/internal/markdown"
	"github.com/robbowland/weaver/internal/oneline"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
	"github.com/robbowland/weaver/internal/tmux"
)

// The views of work items, GITHUB and LINEAR: a service's items under
// their sections, as the board shows threads, for starting threads from
// them. Enter on one opens the new-thread form for it (or goes to its
// thread), Alt R a read-only review; o opens it where it lives. What the
// service said is kept in $STATE_DIR (github.json, linear.json), so a view
// shows it at once and asks again behind it.

// itemSource is a service a view shows the work items of.
type itemSource interface {
	// name is the view's: github, linear.
	name() string
	// kinds are what s switches between, the first shown first.
	kinds() []string
	// label is what the header calls a kind: PULL REQUESTS, ISSUES.
	label(kind string) string
	// read is what the service said last, from its cache.
	read(e *Env) itemData
	// fetch asks the service again and keeps the answer: each section's
	// first page.
	fetch(e *Env)
	// more reads the page after section key's of kind, and keeps it after
	// the section's items until the next fetch.
	more(e *Env, kind, key string)
	// filters are the searches f offers: weaver's searches of your own,
	// then what is open in each repository, label or team weaver knows.
	filters(e *Env) []viewFilter
	// words is a search of the service for words as typed: its own search
	// syntax, or (bare words) within what weaver knows of.
	words(e *Env, q string) viewFilter
	// isID: is q a whole id of an item of the service's (o/r#45 or #45 for
	// GitHub, ENG-42 for Linear), which a search by words looks up as it is?
	isID(q string) bool
	// find reads the view's search (a filter's set made it), and keeps it.
	find(e *Env)
	// clear ends the view's search.
	clear(e *Env)
	// service is where the items live: GitHub, Linear.
	service() string
	// guide is what its ? card says of its sections (Queue, Team, Mine,
	// Watch) and its rows' signals (red, green, faint).
	guide() viewGuide
}

// viewGuide is a source's words for its ? card; team is "" for a source
// without that section.
type viewGuide struct {
	// sections are what each of its sections holds, in their order.
	sections          []sectionNote
	red, green, faint string
	readOnly          string // what alt+m starts
}

// sectionNote is a section's title, and what it holds, for ?'s card.
type sectionNote struct{ title, what string }

// viewFilter is a search f offers: what the picker shows (a note beside
// it), and what makes it the view's search, its source's own.
type viewFilter struct {
	title, note string
	set         func(*Env)
}

// searchKey is the key of a view's own search (f) among its sections: it
// comes first, as SEARCH.
const searchKey = "s"

// itemData is what a service said: its sections of each kind, the view's
// own search first.
type itemData struct {
	sections map[string][]itemSection
}

// searched is the title of the view's own search, "" for none.
func (d itemData) searched() string {
	for _, ss := range d.sections {
		for _, s := range ss {
			if s.search {
				return s.title
			}
		}
	}
	return ""
}

// itemSection is a section's items, how many there are in all (0: the
// service does not say), whether there are more to read, when they were
// read (zero: never) and why asking for them failed last ("": it did not).
// key is what its source's more names it by; search, whether it is the
// view's own search (its title as asked, and a note on what it found of
// another kind).
type itemSection struct {
	key     string
	title   string
	search  bool
	note    string
	total   int
	more    bool
	items   []viewItem
	fetched time.Time
	err     string
}

// section is kind's section key.
func (d itemData) section(kind, key string) (itemSection, bool) {
	for _, s := range d.sections[kind] {
		if s.key == key {
			return s, true
		}
	}
	return itemSection{}, false
}

// asOf is when what the view shows of kind was read: its oldest section's
// (zero when none has been read).
func (d itemData) asOf(kind string) time.Time {
	var at time.Time
	for _, s := range d.sections[kind] {
		if !s.fetched.IsZero() && (at.IsZero() || s.fetched.Before(at)) {
			at = s.fetched
		}
	}
	return at
}

// stale: is any section of any kind older than limit, or not read at all?
func (d itemData) stale(limit time.Duration) bool {
	n := 0
	for _, ss := range d.sections {
		for _, s := range ss {
			if time.Since(s.fetched) > limit {
				return true
			}
			n++
		}
	}
	return n == 0
}

// heading is section s's heading: its title (the view's own search's,
// SEARCH and it as asked), how many it shows of how many (N+ when the
// service has more but does not count them), and, when asking for it
// failed, that its items are from before.
func (s itemSection) heading() string {
	title := strings.ToUpper(s.title)
	if s.search {
		title = "SEARCH · " + s.title
	}
	h := CGrey + title + COff
	switch {
	case s.err != "" && s.fetched.IsZero():
		h += CDim + " · " + COff + CRed + "failed" + COff
	case s.fetched.IsZero():
		h += CDim + " · " + COff + asking
	default:
		count := strconv.Itoa(len(s.items))
		switch {
		case s.total > len(s.items):
			count += " of " + strconv.Itoa(s.total)
		case s.more:
			count += "+"
		}
		h = CGrey + title + " · " + count + COff
		if s.err != "" {
			h += CDim + " · " + COff + CRed + "failed" + COff + CDim + ", as of " + age(int64(time.Since(s.fetched).Seconds())) + " ago" + COff
		}
	}
	if s.note != "" {
		h += CDim + " · " + s.note + COff
	}
	return h
}

// viewItem is a work item as the view shows it, whichever service it is
// from.
type viewItem struct {
	ref     string // what a thread from it records: o/r#45, ENG-42
	title   string
	fact    string // the one thing to know, coloured
	where   string // repository and number, or identifier
	sub     string // the card's line under the title
	updated time.Time
	url     string
	from    []string // what `wvr _from` takes for it
	// branch is the branch a thread on it works on, or a prefix of it
	// (an issue's N-...), in repo when the service has repositories;
	// startOn is the branch a new one would start on.
	branch       string
	branchPrefix bool
	startOn      string
	repo         string
	fields       []cardField
	body         string // markdown
	// key and terms are what a search goes by, besides the whole title:
	// its number ("#45", "ENG-42"), then who, where, its branch, labels.
	key   string
	terms []string
}

// cardField is one of the card's labelled lines.
type cardField struct {
	label, text, colour string
	lines               int
}

// viewThread is a thread of this session, as a view matches it to an item.
type viewThread struct {
	pane  tmux.PaneID // its agent's
	num   int
	state agent.State
	seen  bool
	since int64
	// What it asks of you, and why it is quiet, as the board says them
	// (session.Need).
	need                 session.Need
	exited, notReporting bool
	ref                  string // its work item's
	repo                 string // its repository on GitHub, owner/name
	branch               string
}

// itemThreads are the session's threads.
func (e *Env) itemThreads() []viewThread {
	rows, now := e.AgentRows()
	slugs := map[string]string{}
	var ts []viewThread
	for _, r := range rows {
		if r.Tnum == 0 {
			continue
		}
		f := r.Sfile
		if !strings.HasSuffix(f, ".thread") {
			f = strings.TrimSuffix(f, ".json") + ".thread"
		}
		th, err := store.ReadThread(f)
		if err != nil {
			continue
		}
		t := viewThread{pane: r.Pane, num: r.Tnum, state: r.State, seen: r.Seen, since: r.Since, branch: th.Branch,
			need: r.Need(now), exited: r.Exited, notReporting: r.NotReporting(now)}
		if th.Item != nil {
			t.ref = th.Item.Ref
		}
		if th.Repo != "" {
			s, ok := slugs[th.Repo]
			if !ok {
				s = items.GitHubRepo(th.Repo)
				slugs[th.Repo] = s
			}
			t.repo = s
		}
		ts = append(ts, t)
	}
	return ts
}

// threadOf is the thread for it, if the session has one: its item, else
// its branch.
func threadOf(ts []viewThread, it viewItem) (viewThread, bool) {
	for _, t := range ts {
		if t.ref == it.ref {
			return t, true
		}
	}
	for _, t := range ts {
		if it.branch == "" || t.branch == "" || it.repo != "" && !items.SameRepo(t.repo, it.repo) {
			continue
		}
		if t.branch == it.branch || it.branchPrefix && strings.HasPrefix(t.branch, it.branch) {
			return t, true
		}
	}
	return viewThread{}, false
}

// cell is the thread's cell, painted as the board paints it: by what it
// asks of you, then how it is quiet.
func (t viewThread) cell() string {
	switch t.need {
	case session.NeedYou:
		return paint(GNeed, CRed, CDim, COff)
	case session.NeedLook:
		return paint(GDone, CGreen, CDim, COff)
	case session.NeedWait:
		return paint(GWork, CGrey, CDim, COff)
	case session.NeedNothing:
	}
	switch {
	case t.exited, t.notReporting:
	case t.state == agent.StateDone:
		return paint(GSeen, CDim, CDim, COff)
	case t.state == agent.StateInterrupted:
		return paint(GInt, CGrey, CDim, COff)
	}
	return paint(GIdle, CDim, CDim, COff)
}

// word is the thread's state as the card says it, as the board does.
func (t viewThread) word() string {
	switch {
	case t.exited:
		return "exited"
	case t.notReporting:
		return "not reporting"
	case t.need == session.NeedYou:
		return CRed + "needs you" + COff
	case t.need == session.NeedLook:
		return CGreen + "done" + COff
	}
	return string(t.state)
}

// untick is a title without its code's backticks, which GitHub and Linear
// show as code and a line shows as noise.
func untick(s string) string { return strings.ReplaceAll(s, "`", "") }

var reAnsi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func visible(s string) int { return rlen(reAnsi.ReplaceAllString(s, "")) }

// fitAnsi is s (in one colour) cut to n visible characters and padded to
// n, its colour kept.
func fitAnsi(s string, n int) string {
	if visible(s) <= n {
		return s + strings.Repeat(" ", n-visible(s))
	}
	colour := reAnsi.FindString(s)
	if !strings.HasPrefix(s, colour) {
		colour = ""
	}
	return colour + oneline.Fit(reAnsi.ReplaceAllString(s, ""), n) + COff
}

// itemKind is the kind src's view shows, kept in
// $STATE_DIR/<skey>.<name>-kind.
func (e *Env) itemKind(src itemSource) string {
	ks := src.kinds()
	if l, _ := files.FirstLine(e.StateDir + "/" + e.SKey + "." + src.name() + "-kind"); l != "" {
		for _, k := range ks {
			if k == l {
				return k
			}
		}
	}
	return ks[0]
}

// switchKind shows src's next kind.
func (e *Env) switchKind(src itemSource) {
	ks := src.kinds()
	next := ks[0]
	for i, k := range ks {
		if k == e.itemKind(src) {
			next = ks[(i+1)%len(ks)]
		}
	}
	bestEffort(store.WriteAtomic(e.StateDir+"/"+e.SKey+"."+src.name()+"-kind", []byte(next+"\n")))
}

// itemSock is where src's views listen: <skey>.view.<name>.<pid>.sock.
func (e *Env) itemSocks(name string) []string {
	ms, _ := filepath.Glob(e.SockDir + "/" + e.SKey + ".view." + name + ".*.sock")
	return ms
}

// reloadItems has every view of src show its cache again.
func (e *Env) reloadItems(src itemSource) {
	q := session.Quote(e.Self)
	for _, s := range e.itemSocks(src.name()) {
		session.FzfPost(s, cardUnscrolled+"+reload-sync("+q+" _items "+src.name()+" rows)+refresh-preview+transform("+q+" _items "+src.name()+" chrome)")
	}
}

// refreshItems has every view of src show what changed behind it, as
// session.pushStatus does when threads change: a reload only for rows that
// changed (`_items NAME refresh`), the card where you scrolled it.
func (e *Env) refreshItems(src itemSource) {
	q := session.Quote(e.Self)
	for _, s := range e.itemSocks(src.name()) {
		session.FzfPost(s, "transform("+q+" _items "+src.name()+" refresh)")
	}
}

// itemRows are the view's lines for fzf, a section at a time under its
// heading: id ref prev next display search. id is the line's kind and its
// section's key: h a heading, g the gap before one, i an item, m the line
// after a section's items when it has more (enter reads them). Headings
// and gaps are not for the cursor (prev and next are the lines around them
// it can rest on). id and ref are what fzf keeps the cursor on across a
// reload (--id-nth 1,2), unique: an item in two sections is two lines, and
// fzf goes to the first line of the key it had. ref is an item's ref,
// another line's id after a '-'. search is the line a search shows and
// matches: what an item is searched by (its number, whole title and terms,
// none of it cut to fit, and not its thread's number or cell, whose digits
// and brackets match anything), on the item's first line only, so a search
// finds it once; the other lines' is empty, so they go while you search.
func (e *Env) itemRows(src itemSource) string {
	d := src.read(e)
	ts := e.itemThreads()
	cols, _ := lsSize()
	// Less the border, fzf's gutter and the card's.
	w := lsWidth(cols) - 8
	var ls []viewLine
	now := time.Now()
	sections := d.sections[e.itemKind(src)]
	// The columns, as wide as their widest (within bounds).
	wf, ww := 8, 8
	for _, s := range sections {
		for _, it := range s.items {
			wf, ww = max(wf, visible(it.fact)), max(ww, rlen(it.where))
		}
	}
	wf, ww = min(wf, 22), min(ww, 24)
	wt := max(w-2-1-3-2-wf-2-ww-2-3, 12)
	searched := map[string]bool{}
	for _, s := range sections {
		// An empty section goes, but one that failed (it may not be empty)
		// and the search, which says so.
		if len(s.items) == 0 && s.err == "" && !s.search {
			continue
		}
		if len(ls) > 0 {
			ls = append(ls, viewLine{id: "g" + s.key})
		}
		ls = append(ls, viewLine{id: "h" + s.key, text: s.heading()})
		for _, it := range s.items {
			num, cell := "  ", "   "
			if t, ok := threadOf(ts, it); ok {
				num, cell = fmt.Sprintf("%02d", t.num), t.cell()
			}
			text := CDim + num + COff + " " + cell + "  " + oneline.Fit(oneline.Line(it.title), wt) + "  " + fitAnsi(it.fact, wf) + "  " +
				CGrey + oneline.Fit(it.where, ww) + COff + "  " + CDim + fmt.Sprintf("%3s", age(int64(now.Sub(it.updated).Seconds()))) + COff
			search := ""
			if !searched[it.ref] {
				searched[it.ref] = true
				search = it.key + "  " + oneline.Line(it.title) + "  " + CGrey + oneline.Line(strings.Join(it.terms, " · ")) + COff
			}
			ls = append(ls, viewLine{id: "i" + s.key, ref: it.ref, text: text, search: search})
		}
		if s.more {
			left := ""
			if s.total > len(s.items) {
				left = " · " + strconv.Itoa(s.total-len(s.items)) + " left"
			}
			ls = append(ls, viewLine{id: "m" + s.key, text: "       " + CGrey + "⏎" + COff + CDim + " more" + left + COff})
		}
	}
	rests := func(id string) bool { return id[0] == 'i' || id[0] == 'm' }
	var b strings.Builder
	for i, l := range ls {
		prev, next := "", ""
		if !rests(l.id) {
			for j := i - 1; j >= 0; j-- {
				if rests(ls[j].id) {
					prev = strconv.Itoa(j + 1)
					break
				}
			}
			for j := i + 1; j < len(ls); j++ {
				if rests(ls[j].id) {
					next = strconv.Itoa(j + 1)
					break
				}
			}
		}
		ref := l.ref
		if ref == "" {
			ref = "-" + l.id
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\t%s\n", l.id, ref, prev, next, l.text, l.search)
	}
	return b.String()
}

// rowKey is the key of the section that line id (itemRows') is of; "" for
// none.
func rowKey(id string) string {
	if id == "" {
		return ""
	}
	return id[1:]
}

// itemLabel is the view's border label, as the board's: what it shows and
// how many.
func (e *Env) itemLabel(src itemSource) string {
	kind := e.itemKind(src)
	n := 0
	for _, s := range src.read(e).sections[kind] {
		if !s.search {
			n += len(s.items)
		}
	}
	return strings.TrimSuffix(formLabel("WEAVER", src.label(kind)), " ") + " " + CGrey + strconv.Itoa(n) + COff + " "
}

// asking is the view's header while it asks again.
const asking = CDim + "asking…" + COff

// itemFresh is the view's header: how old what it shows is (its oldest
// section's), and which sections failed and why, their items being from
// before.
func (e *Env) itemFresh(src itemSource) string {
	d, kind := src.read(e), e.itemKind(src)
	ss := d.sections[kind]
	var failed []string
	why, n := "", 0
	for _, s := range ss {
		n += len(s.items)
		if s.err != "" {
			name := strings.ToUpper(s.title)
			if s.search {
				name = "SEARCH"
			}
			failed = append(failed, name)
			if why == "" {
				why = s.err
			}
		}
	}
	if len(failed) == len(ss) && why != "" {
		// Every one: the one reason, not a list of them all.
		failed = nil
	}
	if why != "" {
		if len(failed) > 0 {
			why = strings.Join(failed, ", ") + " failed: " + why
		}
		why = CRed + oneline.Cut(why, 80) + COff
	}
	at := d.asOf(kind)
	switch {
	case at.IsZero() && why != "":
		return why
	case at.IsZero():
		return asking
	}
	t := CDim + "as of " + age(int64(time.Since(at).Seconds())) + " ago" + COff
	switch {
	case why != "":
		return t + CDim + " · " + COff + why
	case n == 0:
		// Nothing to show: say so, not an empty list.
		t += CDim + " · nothing for you in any section" + COff
	}
	return t
}

// readOnly is what Alt R starts on kind: a review of a pull request, an
// investigation of an issue.
func readOnly(kind string) string {
	if kind == "prs" {
		return "REVIEW"
	}
	return "INVESTIGATE"
}

// itemHints are the view's keys: browsing, or searching.
func (e *Env) itemHints(src itemSource) []choice {
	if searching() {
		return []choice{{"⏎", "THREAD"}, {"ctrl+s", "ASK " + strings.ToUpper(src.service())}, {"esc", "BACK"}, {"tab", "VIEWS"}}
	}
	hs := []choice{{"⏎", "THREAD"}, {"alt+m", readOnly(e.itemKind(src))}, {"o", "OPEN"}}
	if len(src.kinds()) > 1 {
		hs = append(hs, choice{"s", strings.ToUpper(strings.Join(src.kinds(), "/"))})
	}
	hs = append(hs, choice{"f", "FILTER"})
	if src.read(e).searched() != "" {
		hs = append(hs, choice{"esc", "CLEAR"})
	}
	help := "HELP"
	if s := os.Getenv("WEAVER_VIEW_SOCK"); s != "" && files.Exists(s+".help") {
		help = "CARD"
	}
	return append(hs, choice{"/", "SEARCH"}, choice{"ctrl+r", "REFRESH"}, choice{"tab", "VIEWS"}, choice{"?", help})
}

// itemFooter is the view's footer: its hints, each a button.
func (e *Env) itemFooter(src itemSource) string { return hintLine(e.itemHints(src), 2) }

// itemMenu is a right click's menu on a view's row of kind k: what its keys
// do to the item (none on a section's heading), and the view's own.
func (e *Env) itemMenu(src itemSource, k string) []menuItem {
	var ms []menuItem
	if strings.HasPrefix(k, "i") {
		ms = []menuItem{{"start a thread", "", "Enter"}, {strings.ToLower(readOnly(e.itemKind(src))) + ", read-only", "m", "M-m"}}
		if !searching() {
			ms = append(ms, menuItem{"open in the browser", "o", "o"})
		}
	}
	if searching() {
		return ms
	}
	d := src.read(e)
	// No key of its own in the menu: m there is alt+m's.
	if s, ok := d.section(e.itemKind(src), rowKey(k)); ok && s.more {
		ms = append(ms, menuItem{"more of " + strings.ToLower(s.title), "", "m"})
	}
	if len(ms) > 0 {
		ms = append(ms, menuItem{})
	}
	if ks := src.kinds(); len(ks) > 1 {
		next := ks[0]
		for i, k := range ks {
			if k == e.itemKind(src) {
				next = ks[(i+1)%len(ks)]
			}
		}
		ms = append(ms, menuItem{"show " + strings.ToLower(src.label(next)), "s", "s"})
	}
	ms = append(ms, menuItem{"filter", "f", "f"})
	if d.searched() != "" {
		ms = append(ms, menuItem{"clear the search", "", "Escape"})
	}
	return append(ms, menuItem{"refresh", "r", "C-r"}, menuItem{"help", "?", "?"})
}

// itemCardRoom is where a view's card goes, as the board's goes: beside the
// list when there is room, else under it in what the list leaves.
func itemCardRoom(rows int) string {
	cols, lines := lsSize()
	if cardRight(cols) {
		return "right," + strconv.Itoa(cardW) + ",border-left,wrap"
	}
	// Less the borders, the header, the footer and the card's rule.
	return "down," + strconv.Itoa(max(lines-rows-6, 8)) + ",border-top,wrap"
}

// itemChrome is the view's label, header, footer and card's room, for
// fzf's load and keys.
func (e *Env) itemChrome(src itemSource) string {
	acts := []string{"change-preview-window(" + itemCardRoom(strings.Count(e.itemRows(src), "\n")) + ")"}
	if a, ok := fzfAct("change-border-label", e.itemLabel(src)); ok {
		acts = append(acts, a)
	}
	// Searching, the input takes the header's line, as on the board: the
	// list stays where it was.
	head := e.itemFresh(src)
	if searching() {
		head = ""
	}
	if a, ok := fzfAct("change-header", head); ok {
		acts = append(acts, a)
	}
	if a, ok := fzfAct("change-footer", e.itemFooter(src)); ok {
		acts = append(acts, a)
	}
	return strings.Join(acts, "+") + "\n"
}

// findItem is src's item ref, of any kind.
func findItem(d itemData, ref string) (viewItem, bool) {
	for _, ss := range d.sections {
		for _, s := range ss {
			for _, it := range s.items {
				if it.ref == ref {
					return it, true
				}
			}
		}
	}
	return viewItem{}, false
}

// itemCard is item ref's card: the thread first (what it has, or what
// enter starts), then what the service says, then its description.
func (e *Env) itemCard(src itemSource, ref string) string {
	if s := os.Getenv("WEAVER_VIEW_SOCK"); s != "" && files.Exists(s+".help") {
		return e.itemHelp(src)
	}
	it, ok := findItem(src.read(e), ref)
	if !ok {
		return ""
	}
	w := previewCols()
	now := time.Now()
	// Its title a link to it (a click opens it, as o does).
	ls := []string{markdown.Link(it.url, CBold+oneline.Line(it.title)+COff), CDim + it.sub + " · " + age(int64(now.Sub(it.updated).Seconds())) + COff, ""}
	if t, ok := threadOf(e.itemThreads(), it); ok {
		ls = append(ls, CDim+oneline.Cut("thread           ", 11)+COff+fmt.Sprintf("%02d %s %s", t.num, t.cell(), t.word())+
			CDim+" · "+age(now.Unix()-t.since)+" · ⏎ goes there"+COff)
	} else {
		on := ""
		if it.startOn != "" {
			on = " on " + it.startOn
		}
		ls = append(ls, field(w, "thread", "none yet: ⏎ starts one"+on, CGrey, 2)...)
	}
	for _, f := range it.fields {
		ls = append(ls, field(w, f.label, f.text, f.colour, max(f.lines, 1))...)
	}
	if body := renderMarkdown(it.body, max(w-2, 20)); body != "" {
		ls = append(ls, "", body)
	}
	return strings.ReplaceAll(strings.Join(ls, "\n"), nbsp, " ") + "\n"
}

// itemHelp is a view's ? card: its keys, its sections and its rows.
func (e *Env) itemHelp(src itemSource) string {
	d, g, o := CDim, CGrey, COff
	key := helpRow
	gd := src.guide()
	out := g + "KEYS" + o + "\n" +
		key("⏎  alt+N", "a thread from it", "(or to the one it has)") +
		key("alt+m", "a read-only thread:", gd.readOnly) +
		key("j k  g G", "move; first, last", "") +
		key("o", "open it in "+src.service(), "") +
		key("m", "more of its section", "(⏎ on the section's last line)") +
		key("f", "filter: a search of "+src.service()+", as SEARCH", "") +
		strings.Repeat(" ", 2+helpCol) + d + "(yours, or a repository's, a label's, a team's)" + o + "\n" +
		key("/", "search", "(esc: back)") +
		key("  ctrl+s", "ask "+src.service()+" for what you typed", "(⏎ too, if none match)")
	if len(src.kinds()) > 1 {
		out += key("s", strings.ToLower(src.label(src.kinds()[0]))+" ↔ "+strings.ToLower(src.label(src.kinds()[1])), "")
	}
	out += key("tab  shift+tab", "the next, previous view", "(THREADS, GITHUB, LINEAR)")
	out += key("ctrl+r", "ask "+src.service()+" again", "(it does every 5 minutes)") +
		key("?", "this help", "(? again: back)") +
		key("q  esc", "back to your thread", "(esc, with a SEARCH: clear it)") +
		"\n" + g + "SECTIONS" + o + "\n" +
		"  " + g + "SEARCH" + o + "   what f or ctrl+s asked for, or the item / named, not loaded\n"
	for _, s := range gd.sections {
		out += "  " + g + strings.ToUpper(s.title) + o + strings.Repeat(" ", max(9-len(s.title), 1)) + s.what + "\n"
	}
	out += "\n" + g + "ROWS" + o + "\n" +
		"  " + d + "01" + o + " " + paint(GWork, CGrey, CDim, COff) + "  the thread it has, as on the board\n"
	for _, s := range []struct{ word, colour, what string }{{"red", CRed, gd.red}, {"green", CGreen, gd.green}, {"faint", CDim, gd.faint}} {
		if s.what != "" {
			out += "  " + s.colour + s.word + o + strings.Repeat(" ", 8-len(s.word)) + s.what + "\n"
		}
	}
	return out + "\n" + mouseHelp()
}

// itemFrom opens the new-thread form for item ref (review: read-only).
func (e *Env) itemFrom(src itemSource, ref string, review bool) error {
	it, ok := findItem(src.read(e), ref)
	if !ok {
		return fmt.Errorf("no item %s in the %s view", ref, src.name())
	}
	// One thread a branch: the one it has, said so (Alt R too: its thread
	// is where to review it).
	if t, ok := threadOf(e.itemThreads(), it); ok && t.pane != "" {
		e.focus(t.pane)
		e.MarkSeen(t.pane)
		e.Say(2500, fmt.Sprintf("weaver: %s has thread %02d: went to it", it.where, t.num))
		return nil
	}
	args := append([]string{}, it.from...)
	if review {
		args = append(args, "--review")
	}
	return e.From(args)
}

// itemKeys are the view's keys while browsing; searching (/) unbinds them so
// they type.
const itemKeys = "j,k,g,G,o,s,m,f,q,?,/"

// itemMore is what m (and enter on a section's last line, at fzf's index n
// of it) does: when the section line id is of has more, read its next page
// behind the view (`_items NAME page`).
func (e *Env) itemMore(src itemSource, id, n string) {
	if s, ok := src.read(e).section(e.itemKind(src), rowKey(id)); !ok || !s.more {
		return
	}
	c := exec.Command(e.Self, "_items", src.name(), "page", id, n)
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	bestEffort(c.Start())
}

// itemPage reads the next page of the section line id is of, the view that
// asked saying it asks, and has every view show it; the view that asked
// from the section's last line (at index n) puts its cursor where that line
// was, on the first item it read, once the list has loaded (`<sock>.pos`,
// itemLoaded: till then fzf keeps the cursor on the line it was on).
func (e *Env) itemPage(src itemSource, id, n string) {
	asker := os.Getenv("WEAVER_VIEW_SOCK")
	if asker != "" {
		session.FzfPost(asker, "change-header("+asking+")")
	}
	src.more(e, e.itemKind(src), rowKey(id))
	if i, ok := atoi(n); ok && asker != "" && strings.HasPrefix(id, "m") {
		bestEffort(os.WriteFile(asker+".pos", []byte(strconv.Itoa(i+1)+"\n"), 0o600))
	}
	e.reloadItems(src)
}

// itemFilter is f: a picker of src's filters, the search it has first, to
// clear it. The one picked becomes the view's search (words no filter
// matches, a search for them); picked again, the search ends.
func (e *Env) itemFilter(src itemSource) {
	fs := src.filters(e)
	cur := src.read(e).searched()
	var b strings.Builder
	at := ""
	if cur != "" {
		b.WriteString("x\tclear  " + CDim + cur + COff + "\n")
	}
	for i, f := range fs {
		note := ""
		if f.note != "" {
			note = "  " + CDim + f.note + COff
		}
		fmt.Fprintf(&b, "%d\t%s%s\n", i, f.title, note)
		if f.title == cur {
			at = strconv.Itoa(i)
		}
	}
	out := e.pick(b.String(), formLabel("WEAVER", "FILTER"), formPrompt("filter"), "a search, or words to ask "+src.service()+" for", at)
	q, sel, _ := strings.Cut(out, "\n")
	v, _, _ := strings.Cut(sel, "\t")
	i, ok := atoi(v)
	switch {
	case v == "x", ok && i < len(fs) && fs[i].title == cur:
		src.clear(e)
	case ok && i < len(fs):
		e.itemSearch(src, fs[i])
	case sel == "" && strings.TrimSpace(q) != "":
		// Words no filter matched: the service asked for them.
		e.itemSearch(src, src.words(e, q))
	}
}

// itemSearch makes f the view's search, read behind the view once the
// view shows it asking (`<sock>.find`, itemLoaded: a reload of its answer
// before then would be lost under the view's own).
func (e *Env) itemSearch(src itemSource, f viewFilter) {
	f.set(e)
	if s := os.Getenv("WEAVER_VIEW_SOCK"); s != "" {
		bestEffort(os.WriteFile(s+".find", nil, 0o600))
	} else {
		e.itemFindBehind(src)
	}
}

// itemAsk is ctrl+s (and enter when nothing matches): the service asked for
// the query q, as the view's search, and the view back to its rows. Those
// actions go to the view's socket, as a key's would: from a transform
// (enter's), fzf would leave the query, the input hidden.
func (e *Env) itemAsk(src itemSource, me, q string) {
	s := os.Getenv("WEAVER_VIEW_SOCK")
	if strings.TrimSpace(q) == "" || s == "" {
		return
	}
	e.itemSearch(src, src.words(e, q))
	session.FzfPost(s, "clear-query+change-with-nth(5)+hide-input+rebind("+itemKeys+")+reload-sync("+me+" rows)+transform("+me+" chrome)")
}

// itemFindBehind reads the view's search behind the view (`_items NAME
// find`).
func (e *Env) itemFindBehind(src itemSource) {
	c := exec.Command(e.Self, "_items", src.name(), "find")
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	bestEffort(c.Start())
}

// itemFind reads the view's search and has every view show it, the view
// that asked with its cursor on the first item found.
func (e *Env) itemFind(src itemSource) {
	src.find(e)
	s, ok := src.read(e).section(e.itemKind(src), searchKey)
	if asker := os.Getenv("WEAVER_VIEW_SOCK"); asker != "" && ok && len(s.items) > 0 {
		// Under its heading, the first line.
		bestEffort(os.WriteFile(asker+".pos", []byte("2\n"), 0o600))
	}
	e.reloadItems(src)
}

// itemEsc is what esc does in a view, its query cleared: searching, back
// to its rows; else, with a SEARCH, the search ends; else back (to your
// thread).
func (e *Env) itemEsc(src itemSource, me, back string) string {
	switch {
	case searching():
		return "change-with-nth(5)+hide-input+rebind(" + itemKeys + ")+transform(" + me + " chrome)\n"
	case src.read(e).searched() != "":
		src.clear(e)
		return "reload-sync(" + me + " rows)+transform(" + me + " chrome)\n"
	}
	return back + "\n"
}

// itemLoaded is what a view does as its list loads: its chrome, the cursor
// where a page read asked for it (`<sock>.pos`), and the search f asked
// for read (`<sock>.find`).
func (e *Env) itemLoaded(src itemSource) string {
	out := e.itemChrome(src)
	s := os.Getenv("WEAVER_VIEW_SOCK")
	if s != "" && files.Exists(s+".find") {
		os.Remove(s + ".find")
		e.itemFindBehind(src)
	}
	if p, err := files.FirstLine(s + ".pos"); err == nil && s != "" {
		os.Remove(s + ".pos")
		if n, ok := atoi(p); ok {
			out = strings.TrimSuffix(out, "\n") + "+pos(" + strconv.Itoa(n) + ")\n"
		}
	}
	return out
}

// Items is `wvr _items NAME [rows|chrome|card REF|from REF [--review]|open
// REF|switch|help|fetch|fetch-bg|more ID [N]|page ID [N]]`: view NAME
// (github, linear), for good (ctrl-c starts it again), and what its fzf
// runs. It shows what the service said last at once, and asks again behind
// it when that is more than 2 minutes old, and every 5 minutes it is open.
func (e *Env) Items(args []string) error {
	src, ok := itemSourceOf(firstArg(args))
	if !ok {
		return fmt.Errorf("usage: wvr _items github|linear [...]")
	}
	args = args[1:]
	ref := ""
	if len(args) > 1 {
		ref = args[1]
	}
	switch sub := firstArg(args); sub {
	case "rows":
		fmt.Print(keepRows(os.Getenv("WEAVER_VIEW_SOCK"), e.itemRows(src)))
		return nil
	case "refresh":
		// As threads change, or behind a fetch: nothing while you read a
		// card you scrolled (cardHold); else its rows again if they
		// changed, else its chrome alone.
		me := session.Quote(e.Self) + " _items " + src.name()
		hold, unmark := e.cardHold()
		switch {
		case hold:
		case rowsChanged(os.Getenv("WEAVER_VIEW_SOCK"), e.itemRows(src)):
			fmt.Print(unmark + "reload-sync(" + me + " rows)\n")
		default:
			fmt.Print(unmark + "transform(" + me + " chrome)\n")
		}
		return nil
	case "chrome", "loaded":
		// A newer weaver installed since the view started: it ends, for the
		// view to start again in that weaver, as the home board does.
		if s := os.Getenv("WEAVER_VIEW_SOCK"); s != "" {
			if _, ok := e.successor(s + ".born"); ok {
				fmt.Print("abort\n")
				return nil
			}
		}
		if sub == "loaded" {
			fmt.Print(e.itemLoaded(src))
		} else {
			fmt.Print(e.itemChrome(src))
		}
		return nil
	case "card":
		fmt.Print(e.itemCard(src, ref))
		return nil
	case "from":
		return e.itemFrom(src, ref, len(args) > 2 && args[2] == "--review")
	case "open":
		if it, ok := findItem(src.read(e), ref); ok && it.url != "" {
			return exec.Command("open", it.url).Run()
		}
		return nil
	case "switch":
		e.switchKind(src)
		return nil
	case "click":
		fmt.Print(footClick(e.itemHints(src), 2))
		return nil
	case "menu":
		e.rowMenu(e.itemMenu(src, ref))
		return nil
	case "help":
		// ? shows the keys in the card's place; ? again, the card.
		if s := os.Getenv("WEAVER_VIEW_SOCK"); s != "" {
			if files.Exists(s + ".help") {
				os.Remove(s + ".help")
			} else {
				bestEffort(os.WriteFile(s+".help", nil, 0o600))
			}
		}
		return nil
	case "fetch":
		src.fetch(e)
		e.reloadItems(src)
		return nil
	case "fetch-bg":
		c := exec.Command(e.Self, "_items", src.name(), "fetch")
		c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		return c.Start()
	case "more":
		e.itemMore(src, ref, arg(args, 2))
		return nil
	case "page":
		e.itemPage(src, ref, arg(args, 2))
		return nil
	case "query":
		fmt.Print(e.itemQuery(src, os.Getenv("FZF_QUERY")))
		return nil
	case "filter":
		e.itemFilter(src)
		return nil
	case "find":
		e.itemFind(src)
		return nil
	case "ask":
		e.itemAsk(src, session.Quote(e.Self)+" _items "+src.name(), ref)
		return nil
	case "lookup":
		e.itemLookup(src, ref)
		return nil
	case "esc":
		// In Alt y's popover (esc peek), back ends it.
		back := "execute-silent(" + session.Quote(e.Self) + " go back)"
		if ref == "peek" {
			back = "abort"
		}
		fmt.Print(e.itemEsc(src, session.Quote(e.Self)+" _items "+src.name(), back))
		return nil
	case "":
	default:
		return fmt.Errorf("no _items %s %s", src.name(), sub)
	}
	_, err := e.itemsView(src, "")
	return err
}

// itemSourceOf is view name's source of items.
func itemSourceOf(name string) (itemSource, bool) {
	switch name {
	case "github":
		return githubSource{}, true
	case "linear":
		return linearSource{}, true
	}
	return nil, false
}

// itemsView is the view itself. In Alt y's popover (`wvr _peek`), peek is
// the file for what is chosen there (peekItemChoice); it ends with a key,
// and says which way tab and shift-tab went ("next", "prev"), else "".
func (e *Env) itemsView(src itemSource, peek string) (string, error) {
	if _, err := exec.LookPath("fzf"); err != nil {
		return "", err
	}
	if err := e.SockDirReady(); err != nil {
		return "", err
	}
	sock := e.SockDir + "/" + e.SKey + ".view." + src.name() + "." + strconv.Itoa(os.Getpid()) + ".sock"
	defer os.Remove(sock)
	defer os.Remove(sock + ".help")
	defer os.Remove(sock + ".pos")
	defer os.Remove(sock + ".find")
	defer os.Remove(sock + ".born")
	defer os.Remove(sock + ".rows")
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, syscall.SIGHUP, syscall.SIGTERM)
	go func() {
		<-sig
		os.Remove(sock)
		os.Remove(sock + ".born")
		os.Exit(129)
	}()
	// The view outlives installs of weaver (installs.go): when it started
	// and from which, for chrome to tell that a newer weaver is installed
	// under it.
	if peek == "" {
		e.markBorn(sock + ".born")
		e.watchInstalls(sock+".born", sock)
	}
	go func() {
		for range time.Tick(5 * time.Minute) {
			src.fetch(e)
			e.refreshItems(src)
		}
	}()
	q := session.Quote(e.Self)
	me := q + " _items " + src.name()
	reload := "reload-sync(" + me + " rows)+transform(" + me + " chrome)"
	act := func(cmd string) string {
		return "transform:case {1} in i*) echo 'execute-silent(" + me + " " + cmd + " {2})' ;; esac"
	}
	fromAct, reviewAct := "execute-silent("+me+" from {2})", "execute-silent("+me+" from {2} --review)"
	backAct := "execute-silent(" + q + " go back)"
	nextAct, prevAct, menuAct := "execute-silent("+q+" go next)", "execute-silent("+q+" go prev)", "execute-silent("+me+" menu {1})"
	peekArg := ""
	if peek != "" {
		// In the popover, what opens a dialog or goes elsewhere ends it.
		fromAct, reviewAct = "print(from)+accept", "print(review)+accept"
		backAct, nextAct, prevAct, menuAct = "abort", "print(next)+accept", "print(prev)+accept", "ignore"
		peekArg = " peek"
	}
	// Alt N on an item, its thread; enter too, and on a section's last
	// line, its next page.
	threadAct := "transform:case {1} in i*) echo '" + fromAct + "' ;; esac"
	// Nothing matching the query, it asks the service (ctrl+s): an action
	// of its own, since fzf skips one that names a field with no match.
	enterAct := "transform[case {1} in i*) echo '" + fromAct + "' ;; m*) echo 'execute-silent(" + me + " more {1} {n})' ;; esac]" +
		`+transform:[ "$FZF_MATCH_COUNT" = 0 ] && [ -n "$FZF_QUERY" ] && echo 'trigger(ctrl-s)'`
	reviewAct = "transform:case {1} in i*) echo '" + reviewAct + "' ;; esac"
	for {
		rows := keepRows(sock, e.itemRows(src))
		// The cursor on the first item, not its section's heading.
		first := 1
		for i, l := range strings.Split(rows, "\n") {
			if strings.HasPrefix(l, "i") {
				first = i + 1
				break
			}
		}
		start := "start:pos(" + strconv.Itoa(first) + ")"
		if src.read(e).stale(2 * time.Minute) {
			// Asked again once fzf listens: an answer sooner (a fake gh's)
			// had no list to reload, and the view stayed empty.
			start += "+execute-silent(" + me + " fetch-bg)"
		}
		pw := itemCardRoom(strings.Count(rows, "\n"))
		args := []string{"--listen", sock, "--with-shell", "/bin/sh -c",
			"--sync", "--track", "--id-nth", "1,2", "--bind", start,
			"--delimiter", "\t", "--with-nth", "5", "--tabstop", "1",
			// Browsing, the rows as they come; searching, the best match
			// first (ties as they come), on the search line.
			"--tiebreak", "index", "--ansi", "--layout", "reverse", "--no-input",
			"--bind", "change:transform(" + me + " query)",
			"--header", e.itemFresh(src), "--header-first", "--footer", e.itemFooter(src), "--footer-border", "none",
			"--border", "sharp", "--border-label", e.itemLabel(src), "--border-label-pos", "3", "--highlight-line", "--pointer", " ", "--marker", " ", "--ellipsis", "…",
			"--scrollbar", "│", "--color", FzfColors,
			"--prompt", "/ ", "--ghost", "search", "--info", "hidden", "--no-separator",
			"--preview", me + " card {2}", "--preview-window", pw,
			"--bind", "load:transform(" + me + " loaded)",
			"--bind", "focus:" + focusRefresh("transform("+me+" refresh)") + `+transform:case {1} in i*|m*) ;; *) case $FZF_ACTION in up|page-up|half-page-up|offset-up|scroll-up|up-match|prev-*) t={3}; [ -n "$t" ] || t={4} ;; *) t={4}; [ -n "$t" ] || t={3} ;; esac; [ -n "$t" ] && echo "pos($t)" ;; esac`,
			"--bind", "j:down,k:up,g,home:first,G,end:last",
			"--bind", "enter,double-click:" + enterAct,
			"--bind", "alt-N,alt-n:" + threadAct,
			"--bind", "alt-m:" + reviewAct,
			"--bind", "o:" + act("open"),
			"--bind", "m:execute-silent(" + me + " more {1})",
			"--bind", "ctrl-r:" + cardUnscrolled + "+execute-silent(" + me + " fetch-bg)+change-header(" + asking + ")",
			"--bind", "/:show-input+unbind(" + itemKeys + ")+transform(" + me + " chrome)",
			// q and esc: back to your thread, as on home.
			// esc's own clear-query, so fzf searches again (a transform's
			// leaves a key's search in place).
			"--bind", "esc:clear-query+transform(" + me + " esc" + peekArg + ")",
			"--bind", "f:execute(" + me + " filter)+" + reload,
			"--bind", "ctrl-s:execute-silent(" + me + " ask {q})",
			"--bind", "q:" + backAct,
			"--bind", "?:execute-silent(" + me + " help)+" + cardUnscrolled + "+refresh-preview+transform(" + me + " chrome)",
			// The footer's hints are buttons; a right click, the row's menu.
			"--bind", "click-footer:transform(" + me + " click)",
			"--bind", "right-click:" + menuAct,
		}
		args = append(args, cardScrollBinds...)
		if peek != "" {
			args = append(args, "--bind", "alt-y:abort", "--bind", "alt-1:print(alt-1)+accept")
		}
		if len(src.kinds()) > 1 {
			args = append(args, "--bind", "s:execute-silent("+me+" switch)+"+reload+"+first")
		} else {
			args = append(args, "--bind", "s:ignore")
		}
		// Tab and shift-tab: the next and previous view, as on home.
		args = append(args, "--bind", "tab:"+nextAct, "--bind", "btab:"+prevAct)
		c := fzfCmd(args, "WEAVER_VIEW_SOCK="+sock)
		c.Stdin = strings.NewReader(rows)
		c.Stderr = os.Stderr
		if peek != "" {
			out, _ := c.Output()
			key, sel, _ := strings.Cut(strings.TrimRight(string(out), "\n"), "\n")
			step, cmds := peekItemChoice(src.name(), key, sel)
			return step, writePeek(peek, cmds)
		}
		err := c.Run()
		// ctrl-c ends fzf: the view carries on, not without its terminal.
		if x, ok := err.(*exec.ExitError); ok && x.ExitCode() == 2 {
			return "", nil
		}
		t, err := os.Open("/dev/tty")
		if err != nil {
			return "", nil
		}
		_ = t.Close() // opened only to see that there is one
		// A newer weaver is installed (chrome ended the view for it): the
		// view starts again in it, asking as it asks.
		if next, ok := e.successor(sock + ".born"); ok {
			os.Remove(sock)
			os.Remove(sock + ".born")
			return "", syscall.Exec(next, []string{next, "_items", src.name()}, os.Environ())
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// peekItemChoice is what key, pressed on view name's line sel in Alt y's
// popover, asks for: tab and shift-tab's step ("next", "prev"), or the wvr
// commands to run once the popover has closed (the new-thread form for the
// item, read-only for a review; Alt 1's jump).
func peekItemChoice(name, key, sel string) (string, [][]string) {
	ref := ""
	if f := strings.Split(sel, "\t"); strings.HasPrefix(sel, "i") && len(f) > 1 {
		ref = f[1]
	}
	switch {
	case key == "next" || key == "prev":
		return key, nil
	case key == "alt-1":
		return "", [][]string{{"next"}}
	case key == "from" && ref != "":
		return "", [][]string{{"_items", name, "from", ref}}
	case key == "review" && ref != "":
		return "", [][]string{{"_items", name, "from", ref, "--review"}}
	}
	return "", nil
}

func firstArg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}
