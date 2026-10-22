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
	// fetch asks the service again and keeps the answer.
	fetch(e *Env)
	// service is where the items live: GitHub, Linear.
	service() string
	// guide is what its ? card says of its sections (Queue, Team, Mine,
	// Watch) and its rows' signals (red, green, faint).
	guide() viewGuide
}

// viewGuide is a source's words for its ? card; team is "" for a source
// without that section.
type viewGuide struct {
	queue, team, mine, watch string
	red, green, faint        string
	readOnly                 string // what alt+m starts
}

// itemData is what a service said: its sections of each kind.
type itemData struct {
	sections map[string][]itemSection
}

// itemSection is a section's items, how many there are in all (0: the
// service does not say), whether there are more to read, when they were
// read (zero: never) and why asking for them failed last ("": it did not).
type itemSection struct {
	title   string
	total   int
	more    bool
	items   []viewItem
	fetched time.Time
	err     string
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

// heading is section s's heading: its title, how many it shows of how
// many (N+ when the service has more but does not count them), and, when
// asking for it failed, that its items are from before.
func (s itemSection) heading() string {
	h := CGrey + strings.ToUpper(s.title)
	if s.err != "" && s.fetched.IsZero() {
		return h + COff + CDim + " · " + COff + CRed + "failed" + COff
	}
	count := strconv.Itoa(len(s.items))
	switch {
	case s.total > len(s.items):
		count += " of " + strconv.Itoa(s.total)
	case s.more:
		count += "+"
	}
	h += " · " + count + COff
	if s.err != "" {
		h += CDim + " · " + COff + CRed + "failed" + COff + CDim + ", as of " + age(int64(time.Since(s.fetched).Seconds())) + " ago" + COff
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
	pane   tmux.PaneID // its agent's
	num    int
	state  agent.State
	seen   bool
	since  int64
	ref    string // its work item's
	repo   string // its repository on GitHub, owner/name
	branch string
}

// itemThreads are the session's threads.
func (e *Env) itemThreads() []viewThread {
	rows, _ := e.AgentRows()
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
		t := viewThread{pane: r.Pane, num: r.Tnum, state: r.State, seen: r.Seen, since: r.Since, branch: th.Branch}
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

// cell is the thread's cell, painted as the board paints it.
func (t viewThread) cell() string {
	switch t.state {
	case agent.StateNeedsYou:
		return paint(GNeed, CRed, CDim, COff)
	case agent.StateDone:
		if t.seen {
			return paint(GSeen, CDim, CDim, COff)
		}
		return paint(GDone, CGreen, CDim, COff)
	case agent.StateWorking, agent.StateStarting:
		return paint(GWork, CGrey, CDim, COff)
	case agent.StateInterrupted:
		return paint(GInt, CGrey, CDim, COff)
	case agent.StateIdle, agent.StateEnded:
	}
	return paint(GIdle, CDim, CDim, COff)
}

// word is the thread's state as the card says it.
func (t viewThread) word() string {
	switch t.state {
	case agent.StateNeedsYou:
		return CRed + "needs you" + COff
	case agent.StateDone:
		if !t.seen {
			return CGreen + "done" + COff
		}
	case agent.StateWorking, agent.StateIdle, agent.StateInterrupted, agent.StateEnded, agent.StateStarting:
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
		session.FzfPost(s, "reload-sync("+q+" _items "+src.name()+" rows)+refresh-preview+transform("+q+" _items "+src.name()+" chrome)")
	}
}

// itemRows are the view's lines for fzf, a section at a time under its
// heading: id ref prev next display search. Headings and the gaps between
// are not items (prev and next are the items around them, for the cursor to
// step over them). ref is what fzf keeps the cursor on across a reload
// (--id-nth): an item's ref, another line's id after a '-'; never empty,
// since fzf blocks until it finds the line it was on, and a line of no key
// it never finds. search is the line a search shows and matches: the
// item's number, its whole title and its terms, none of it cut to fit; a
// heading's and a gap's is empty, so they go while you search.
func (e *Env) itemRows(src itemSource) string {
	d := src.read(e)
	ts := e.itemThreads()
	cols, _ := lsSize()
	// Less the border, fzf's gutter and the card's.
	w := lsWidth(cols) - 8
	type line struct{ id, ref, text, search string }
	var ls []line
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
	for i, s := range sections {
		// An empty section goes, but one that failed: it may not be empty.
		if len(s.items) == 0 && s.err == "" {
			continue
		}
		if len(ls) > 0 {
			ls = append(ls, line{id: "g" + strconv.Itoa(i)})
		}
		ls = append(ls, line{id: "h" + strconv.Itoa(i), text: s.heading()})
		for _, it := range s.items {
			num, cell := "  ", "   "
			if t, ok := threadOf(ts, it); ok {
				num, cell = fmt.Sprintf("%02d", t.num), t.cell()
			}
			text := CDim + num + COff + " " + cell + "  " + oneline.Fit(oneline.Line(it.title), wt) + "  " + fitAnsi(it.fact, wf) + "  " +
				CGrey + oneline.Fit(it.where, ww) + COff + "  " + CDim + fmt.Sprintf("%3s", age(int64(now.Sub(it.updated).Seconds()))) + COff
			search := CDim + num + COff + " " + cell + "  " + it.key + "  " + oneline.Line(it.title) + "  " +
				CGrey + oneline.Line(strings.Join(it.terms, " · ")) + COff
			ls = append(ls, line{id: "i" + strconv.Itoa(len(ls)), ref: it.ref, text: text, search: search})
		}
	}
	var b strings.Builder
	for i, l := range ls {
		prev, next := "", ""
		if l.id[0] != 'i' {
			for j := i - 1; j >= 0; j-- {
				if ls[j].id[0] == 'i' {
					prev = strconv.Itoa(j + 1)
					break
				}
			}
			for j := i + 1; j < len(ls); j++ {
				if ls[j].id[0] == 'i' {
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

// itemLabel is the view's border label, as the board's: what it shows and
// how many.
func (e *Env) itemLabel(src itemSource) string {
	kind := e.itemKind(src)
	n := 0
	for _, s := range src.read(e).sections[kind] {
		n += len(s.items)
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
			failed = append(failed, strings.ToUpper(s.title))
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
		return []choice{{"⏎", "THREAD"}, {"esc", "BACK"}, {"tab", "VIEWS"}}
	}
	hs := []choice{{"⏎", "THREAD"}, {"alt+m", readOnly(e.itemKind(src))}, {"o", "OPEN"}}
	if len(src.kinds()) > 1 {
		hs = append(hs, choice{"s", strings.ToUpper(strings.Join(src.kinds(), "/"))})
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
		key("/", "search", "(esc: back)")
	if len(src.kinds()) > 1 {
		out += key("s", strings.ToLower(src.label(src.kinds()[0]))+" ↔ "+strings.ToLower(src.label(src.kinds()[1])), "")
	}
	out += key("tab  shift+tab", "the next, previous view", "(THREADS, GITHUB, LINEAR)")
	out += key("ctrl+r", "ask "+src.service()+" again", "(it does every 5 minutes)") +
		key("?", "this help", "(? again: back)") +
		key("q  esc", "back to your thread", "") +
		"\n" + g + "SECTIONS" + o + "\n" +
		"  " + g + "QUEUE" + o + "   " + gd.queue + "\n"
	if gd.team != "" {
		out += "  " + g + "TEAM" + o + "    " + gd.team + "\n"
	}
	out += "  " + g + "MINE" + o + "    " + gd.mine + "\n" +
		"  " + g + "WATCH" + o + "   " + gd.watch + "\n" +
		"\n" + g + "ROWS" + o + "\n" +
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
		e.Tmux("display-message", "-d", "2500", fmt.Sprintf("weaver: %s has thread %02d: went to it", it.where, t.num))
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
const itemKeys = "j,k,g,G,o,s,q,?"

// Items is `wvr _items NAME [rows|chrome|card REF|from REF [--review]|open
// REF|switch|help|fetch|fetch-bg]`: view NAME (github, linear), for good
// (ctrl-c starts it again), and what its fzf runs. It shows what the
// service said last at once, and asks again behind it when that is more
// than 2 minutes old, and every 5 minutes it is open.
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
		fmt.Print(e.itemRows(src))
		return nil
	case "chrome":
		// A newer weaver installed since the view started: it ends, for the
		// view to start again in that weaver, as the home board does.
		if s := os.Getenv("WEAVER_VIEW_SOCK"); s != "" {
			if _, ok := e.successor(s + ".born"); ok {
				fmt.Print("abort\n")
				return nil
			}
		}
		fmt.Print(e.itemChrome(src))
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
	bestEffort(os.MkdirAll(e.SockDir, 0o700))
	sock := e.SockDir + "/" + e.SKey + ".view." + src.name() + "." + strconv.Itoa(os.Getpid()) + ".sock"
	defer os.Remove(sock)
	defer os.Remove(sock + ".help")
	defer os.Remove(sock + ".born")
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
	refresh := func() {
		src.fetch(e)
		e.reloadItems(src)
	}
	if src.read(e).stale(2 * time.Minute) {
		go refresh()
	}
	go func() {
		for range time.Tick(5 * time.Minute) {
			refresh()
		}
	}()
	q := session.Quote(e.Self)
	me := q + " _items " + src.name()
	reload := "reload-sync(" + me + " rows)+transform(" + me + " chrome)"
	act := func(cmd string) string {
		return "transform:case {1} in i*) echo 'execute-silent(" + me + " " + cmd + " {2})' ;; esac"
	}
	fromAct, reviewAct := act("from"), "transform:case {1} in i*) echo 'execute-silent("+me+" from {2} --review)' ;; esac"
	backAct := "execute-silent(" + q + " go back)"
	nextAct, prevAct, menuAct := "execute-silent("+q+" go next)", "execute-silent("+q+" go prev)", "execute-silent("+me+" menu {1})"
	if peek != "" {
		// In the popover, what opens a dialog or goes elsewhere ends it.
		fromAct = "transform:case {1} in i*) echo 'print(from)+accept' ;; esac"
		reviewAct = "transform:case {1} in i*) echo 'print(review)+accept' ;; esac"
		backAct, nextAct, prevAct, menuAct = "abort", "print(next)+accept", "print(prev)+accept", "ignore"
	}
	for {
		rows := e.itemRows(src)
		// The cursor on the first item, not its section's heading.
		first := 1
		for i, l := range strings.Split(rows, "\n") {
			if strings.HasPrefix(l, "i") {
				first = i + 1
				break
			}
		}
		pw := itemCardRoom(strings.Count(rows, "\n"))
		args := []string{"--listen", sock, "--with-shell", "/bin/sh -c",
			"--sync", "--track", "--id-nth", "2", "--bind", "start:pos(" + strconv.Itoa(first) + ")",
			"--delimiter", "\t", "--with-nth", "5", "--tabstop", "1",
			// Browsing, the rows as they come; searching, the best match
			// first (ties as they come), on the search line.
			"--tiebreak", "index", "--ansi", "--layout", "reverse", "--no-input",
			"--bind", `change:transform:[ -n "$FZF_QUERY" ] && echo 'change-with-nth(6)' || echo 'change-with-nth(5)'`,
			"--header", e.itemFresh(src), "--header-first", "--footer", e.itemFooter(src), "--footer-border", "none",
			"--border", "sharp", "--border-label", e.itemLabel(src), "--border-label-pos", "3", "--highlight-line", "--pointer", " ", "--marker", " ", "--ellipsis", "…",
			"--scrollbar", "│", "--color", FzfColors,
			"--prompt", "/ ", "--ghost", "search", "--info", "hidden", "--no-separator",
			"--preview", me + " card {2}", "--preview-window", pw,
			"--bind", "load:transform(" + me + " chrome)",
			"--bind", `focus:transform:case {1} in i*) ;; *) case $FZF_ACTION in up|page-up|half-page-up|offset-up|scroll-up|up-match|prev-*) t={3}; [ -n "$t" ] || t={4} ;; *) t={4}; [ -n "$t" ] || t={3} ;; esac; [ -n "$t" ] && echo "pos($t)" ;; esac`,
			"--bind", "j:down,k:up,g,home:first,G,end:last",
			"--bind", "enter,double-click,alt-N,alt-n:" + fromAct,
			"--bind", "alt-m:" + reviewAct,
			"--bind", "o:" + act("open"),
			"--bind", "ctrl-r:execute-silent(" + me + " fetch-bg)+change-header(" + asking + ")",
			"--bind", "/:show-input+unbind(" + itemKeys + ")+transform(" + me + " chrome)",
			// q and esc: back to your thread, as on home.
			"--bind", `esc:clear-query+change-with-nth(5)+transform{if [ "$FZF_INPUT_STATE" = hidden ]; then echo '` + backAct + `'; else echo 'hide-input+rebind(` + itemKeys + `)+transform(` + me + ` chrome)'; fi}`,
			"--bind", "q:" + backAct,
			"--bind", "?:execute-silent(" + me + " help)+refresh-preview+transform(" + me + " chrome)",
			// The footer's hints are buttons; a right click, the row's menu.
			"--bind", "click-footer:transform(" + me + " click)",
			"--bind", "right-click:" + menuAct,
		}
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
