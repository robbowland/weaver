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
	"github.com/robbowland/weaver/internal/github"
	"github.com/robbowland/weaver/internal/items"
	"github.com/robbowland/weaver/internal/oneline"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
)

// The GITHUB view: the pull requests and issues of weaver's sections
// (internal/github, after gh-dash), as the board shows threads, for
// starting threads from them. Enter on one opens the new-thread form for
// it (or goes to its thread), Alt R a read-only review; o opens it on
// GitHub. What GitHub said is kept in $STATE_DIR/github.json, so the view
// shows it at once and asks again behind it.

// ghCache is what GitHub said last.
type ghCache struct {
	Fetched time.Time                       `json:"fetched"`
	Err     string                          `json:"err,omitempty"`
	Results map[github.Kind][]github.Result `json:"results"`
}

func (e *Env) ghCacheFile() string { return e.StateDir + "/github.json" }

func (e *Env) readGitHub() ghCache {
	var c ghCache
	bestEffort(store.Read(e.ghCacheFile(), &c))
	return c
}

// ghFetch asks GitHub again and keeps the answer: a section that failed
// keeps what it had. Then every GITHUB view open shows it.
func (e *Env) ghFetch() {
	old := e.readGitHub()
	res, err := github.Fetch()
	c := ghCache{Fetched: time.Now(), Results: res}
	if err != nil {
		c.Err = err.Error()
		for k, rs := range res {
			for i, r := range rs {
				if len(r.Items) == 0 && r.Total == 0 && i < len(old.Results[k]) {
					res[k][i] = old.Results[k][i]
				}
			}
		}
	}
	bestEffort(store.Write(e.ghCacheFile(), c))
	e.ghReload()
}

// ghReload has every GITHUB view of the session show the cache again.
func (e *Env) ghReload() {
	socks, _ := filepath.Glob(e.SockDir + "/" + e.SKey + ".gh.*.sock")
	q := session.Quote(e.Self)
	for _, s := range socks {
		session.FzfPost(s, "reload("+q+" _github rows)+refresh-preview+transform("+q+" _github chrome)")
	}
}

// ghKind is the kind the session's view shows, kept in
// $STATE_DIR/<skey>.github-kind.
func (e *Env) ghKind() github.Kind {
	if l, _ := files.FirstLine(e.StateDir + "/" + e.SKey + ".github-kind"); l == string(github.Issues) {
		return github.Issues
	}
	return github.PRs
}

func (e *Env) ghSwitch() {
	k := github.Issues
	if e.ghKind() == github.Issues {
		k = github.PRs
	}
	bestEffort(store.WriteAtomic(e.StateDir+"/"+e.SKey+".github-kind", []byte(string(k)+"\n")))
}

// ghThread is a thread of this session, as the view matches it to an item.
type ghThread struct {
	num    int
	state  agent.State
	seen   bool
	since  int64
	ref    string // its work item's, owner/name#N
	repo   string // its repository on GitHub, owner/name
	branch string
}

// ghThreads are the session's threads.
func (e *Env) ghThreads() []ghThread {
	rows, _ := e.AgentRows()
	slugs := map[string]string{}
	var ts []ghThread
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
		t := ghThread{num: r.Tnum, state: r.State, seen: r.Seen, since: r.Since, branch: th.Branch}
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

// ghThreadOf is the thread for it, if the session has one: its item, else
// its branch (a pull request's head; an issue's N-... branch).
func ghThreadOf(ts []ghThread, it github.Item) (ghThread, bool) {
	for _, t := range ts {
		if t.ref == it.Ref() {
			return t, true
		}
	}
	for _, t := range ts {
		if t.repo != it.Repo || t.branch == "" {
			continue
		}
		if it.Kind == github.PRs && t.branch == it.Head || it.Kind == github.Issues && strings.HasPrefix(t.branch, strconv.Itoa(it.Number)+"-") {
			return t, true
		}
	}
	return ghThread{}, false
}

// cell is the thread's cell, painted as the board paints it.
func (t ghThread) cell() string {
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
func (t ghThread) word() string {
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

// ghFact is the one thing to know about an item, coloured as a signal.
func ghFact(it github.Item) string {
	if it.Kind == github.Issues {
		if len(it.Labels) > 0 {
			return CGrey + it.Labels[0] + COff
		}
		return CDim + "@" + it.Author + COff
	}
	switch {
	case it.Draft:
		return CDim + "draft" + COff
	case it.Review == "CHANGES_REQUESTED":
		return CRed + "changes requested" + COff
	case len(it.FailingChecks) == 1:
		return CRed + it.FailingChecks[0] + " failing" + COff
	case len(it.FailingChecks) > 1:
		return CRed + strconv.Itoa(len(it.FailingChecks)) + " checks failing" + COff
	case it.Review == "APPROVED":
		return CGreen + "approved" + COff
	case it.RunningChecks > 0:
		return CGrey + "checks running" + COff
	}
	return CDim + "@" + it.Author + COff
}

// ghWhere is where an item is: its repository's name and its number.
func ghWhere(it github.Item) string {
	return it.Repo[strings.IndexByte(it.Repo, '/')+1:] + " #" + strconv.Itoa(it.Number)
}

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

// GitHubRows is `wvr _github rows`: the view's lines for fzf, a section at a
// time under its heading: id kind repo number prev next url display. Headings and
// the gaps between are not items (prev and next are the items around them,
// for the cursor to step over them).
func (e *Env) GitHubRows() string {
	c := e.readGitHub()
	kind := e.ghKind()
	ts := e.ghThreads()
	cols, _ := lsSize()
	// Less the border, fzf's gutter and the card's.
	w := lsWidth(cols) - 8
	singular := "pr"
	if kind == github.Issues {
		singular = "issue"
	}
	type line struct{ id, kind, repo, num, url, text string }
	var ls []line
	now := time.Now()
	// The columns, as wide as their widest (within bounds).
	wf, ww := 8, 10
	for _, r := range c.Results[kind] {
		for _, it := range r.Items {
			wf, ww = max(wf, visible(ghFact(it))), max(ww, rlen(ghWhere(it)))
		}
	}
	wf, ww = min(wf, 22), min(ww, 24)
	wt := max(w-2-1-3-2-wf-2-ww-2-3, 12)
	for i, r := range c.Results[kind] {
		if len(r.Items) == 0 {
			continue
		}
		if len(ls) > 0 {
			ls = append(ls, line{id: "g" + strconv.Itoa(i)})
		}
		count := strconv.Itoa(r.Total)
		if r.Total > len(r.Items) {
			count = strconv.Itoa(len(r.Items)) + " of " + count
		}
		ls = append(ls, line{id: "h" + strconv.Itoa(i), text: CGrey + strings.ToUpper(r.Title) + " · " + count + COff})
		for _, it := range r.Items {
			num, cell := "  ", "   "
			if t, ok := ghThreadOf(ts, it); ok {
				num, cell = fmt.Sprintf("%02d", t.num), t.cell()
			}
			fact, where := ghFact(it), ghWhere(it)
			text := CDim + num + COff + " " + cell + "  " + oneline.Fit(oneline.Line(it.Title), wt) + "  " + fitAnsi(fact, wf) + "  " +
				CGrey + oneline.Fit(where, ww) + COff + "  " + CDim + fmt.Sprintf("%3s", age(int64(now.Sub(it.UpdatedAt).Seconds()))) + COff
			ls = append(ls, line{id: "i" + strconv.Itoa(len(ls)), kind: singular, repo: it.Repo, num: strconv.Itoa(it.Number), url: it.URL, text: text})
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
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", l.id, l.kind, l.repo, l.num, prev, next, l.url, l.text)
	}
	return b.String()
}

// GitHubChrome is `wvr _github chrome`: the view's header and footer, for
// fzf's load and its keys.
func (e *Env) GitHubChrome() string {
	var acts []string
	if a, ok := fzfAct("change-header", e.ghTitle()); ok {
		acts = append(acts, a)
	}
	if a, ok := fzfAct("change-footer", ghFooter()); ok {
		acts = append(acts, a)
	}
	return strings.Join(acts, "+") + "\n"
}

// ghTitle is the view's header: what it shows, how many, and how fresh.
func (e *Env) ghTitle() string {
	c := e.readGitHub()
	kind := e.ghKind()
	what := "PULL REQUESTS"
	if kind == github.Issues {
		what = "ISSUES"
	}
	n := 0
	for _, r := range c.Results[kind] {
		n += len(r.Items)
	}
	t := CBold + "[ WEAVER :: " + what + " ]" + COff + " " + strconv.Itoa(n)
	switch {
	case c.Fetched.IsZero():
		t += "   " + CDim + "asking GitHub…" + COff
	case c.Err != "":
		t += "   " + CRed + oneline.Cut(c.Err, 60) + COff
	default:
		t += "   " + CDim + "as of " + age(int64(time.Since(c.Fetched).Seconds())) + " ago" + COff
	}
	return t
}

// ghFooter is the view's keys: browsing, or searching.
func ghFooter() string {
	g, d, o := CGrey, CDim, COff
	items := []string{"⏎ THREAD", "Alt-R REVIEW", "o GITHUB", "s ISSUES/PRS", "/ SEARCH", "^r REFRESH"}
	if in := os.Getenv("FZF_INPUT_STATE"); in != "" && in != "hidden" {
		items = []string{"⏎ THREAD", "esc BACK"}
	}
	out := ""
	for _, it := range items {
		k, rest, _ := strings.Cut(it, " ")
		out += g + k + o + " " + d + rest + o + "  "
	}
	return out
}

var reHTMLComment = regexp.MustCompile(`(?s)<!--.*?-->`)

// GitHubCard is `wvr _github card KIND OWNER/NAME NUM`: an item's card,
// the thread first: what it has, or what enter starts.
func (e *Env) GitHubCard(args []string) string {
	if len(args) < 3 {
		return ""
	}
	ref := args[1] + "#" + args[2]
	var it github.Item
	found := false
	for _, rs := range e.readGitHub().Results {
		for _, r := range rs {
			for _, x := range r.Items {
				if x.Ref() == ref {
					it, found = x, true
				}
			}
		}
	}
	if !found {
		return ""
	}
	w := previewCols()
	now := time.Now()
	state := strings.ToLower(it.State)
	if it.Draft {
		state = "draft"
	}
	ls := []string{CBold + oneline.Line(it.Title) + COff,
		CDim + it.Repo + " #" + strconv.Itoa(it.Number) + " · " + state + " · " + age(int64(now.Sub(it.UpdatedAt).Seconds())) + COff, ""}
	add := func(f []string) { ls = append(ls, f...) }
	if t, ok := ghThreadOf(e.ghThreads(), it); ok {
		ls = append(ls, CDim+oneline.Cut("thread           ", 11)+COff+fmt.Sprintf("%02d %s %s", t.num, t.cell(), t.word())+
			CDim+" · "+age(now.Unix()-t.since)+" · ⏎ goes there"+COff)
	} else {
		branch := it.Head
		if it.Kind == github.Issues {
			branch = items.GitHubIssue{Repo: it.Repo, Num: it.Number, Text: items.Text{Title: it.Title}}.BranchName()
		}
		add(field(w, "thread", "none yet: ⏎ starts one on "+branch, CGrey, 2))
	}
	add(field(w, "by", "@"+it.Author, CGrey, 1))
	if it.Kind == github.PRs {
		add(field(w, "branch", it.Base+" ← "+it.Head, CGrey, 1))
		switch it.Review {
		case "CHANGES_REQUESTED":
			add(field(w, "review", "changes requested", CRed, 1))
		case "APPROVED":
			add(field(w, "review", "approved", CGreen, 1))
		}
		label := "review"
		if it.Review == "CHANGES_REQUESTED" || it.Review == "APPROVED" {
			label = ""
		}
		if len(it.Requested) > 0 {
			add(field(w, label, "waits on "+strings.Join(it.Requested, ", "), CGrey, 2))
		}
		label = "checks"
		if n := len(it.FailingChecks); n > 0 {
			add(field(w, label, strconv.Itoa(n)+" failing: "+strings.Join(it.FailingChecks, ", "), CRed, 2))
			label = ""
		}
		var cs []string
		if it.RunningChecks > 0 {
			cs = append(cs, strconv.Itoa(it.RunningChecks)+" running")
		}
		if it.PassingChecks > 0 {
			cs = append(cs, strconv.Itoa(it.PassingChecks)+" passing")
		}
		add(field(w, label, strings.Join(cs, " · "), CGrey, 1))
		add(field(w, "lines", fmt.Sprintf("+%d -%d · %d file%s · %d commit%s", it.Additions, it.Deletions,
			it.Files, plural(it.Files), it.Commits, plural(it.Commits)), CGrey, 1))
	}
	add(field(w, "labels", strings.Join(it.Labels, ", "), CGrey, 1))
	add(field(w, "assigned", strings.Join(it.Assignees, ", "), CGrey, 1))
	if it.Comments > 0 {
		add(field(w, "comments", strconv.Itoa(it.Comments), CGrey, 1))
	}
	if body := strings.TrimSpace(reHTMLComment.ReplaceAllString(it.Body, "")); body != "" {
		ls = append(ls, "")
		n := 0
		for _, para := range strings.Split(strings.ReplaceAll(body, "\r", ""), "\n") {
			if strings.TrimSpace(para) == "" {
				continue
			}
			for _, l := range wrap(oneline.Line(para), max(w-2, 20)) {
				if n == 16 {
					break
				}
				ls = append(ls, CGrey+l+COff)
				n++
			}
		}
	}
	return strings.ReplaceAll(strings.Join(ls, "\n"), nbsp, " ") + "\n"
}

// ghKeys are the view's keys while browsing; searching (/) unbinds them so
// they type.
const ghKeys = "j,k,g,G,o,s,q"

// GitHub is `wvr _github`: the view, for good (ctrl-c starts it again).
// It shows what GitHub said last at once, and asks again behind it when
// that is more than 2 minutes old, and every 5 minutes it is open.
func (e *Env) GitHub(args []string) error {
	switch sub := firstArg(args); sub {
	case "rows":
		fmt.Print(e.GitHubRows())
		return nil
	case "chrome":
		fmt.Print(e.GitHubChrome())
		return nil
	case "card":
		fmt.Print(e.GitHubCard(args[1:]))
		return nil
	case "switch":
		e.ghSwitch()
		return nil
	case "fetch":
		e.ghFetch()
		return nil
	case "fetch-bg":
		c := exec.Command(e.Self, "_github", "fetch")
		c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		return c.Start()
	case "":
	default:
		return fmt.Errorf("no _github %s", sub)
	}
	if _, err := exec.LookPath("fzf"); err != nil {
		return err
	}
	bestEffort(os.MkdirAll(e.SockDir, 0o700))
	sock := e.SockDir + "/" + e.SKey + ".gh." + strconv.Itoa(os.Getpid()) + ".sock"
	defer os.Remove(sock)
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, syscall.SIGHUP, syscall.SIGTERM)
	go func() {
		<-sig
		os.Remove(sock)
		os.Exit(129)
	}()
	if c := e.readGitHub(); time.Since(c.Fetched) > 2*time.Minute {
		go e.ghFetch()
	}
	go func() {
		for range time.Tick(5 * time.Minute) {
			e.ghFetch()
		}
	}()
	q := session.Quote(e.Self)
	reload := "reload(" + q + " _github rows)+transform(" + q + " _github chrome)"
	for {
		rows := e.GitHubRows()
		// The cursor on the first item, not its section's heading.
		first := 1
		for i, l := range strings.Split(rows, "\n") {
			if strings.HasPrefix(l, "i") {
				first = i + 1
				break
			}
		}
		// The card beside the list when there is room, as on the board.
		pw := "down,55%,border-top,wrap"
		if cols, _ := lsSize(); cardRight(cols) {
			pw = "right," + strconv.Itoa(cardW) + ",border-left,wrap"
		}
		args := []string{"--listen", sock, "--with-shell", "/bin/sh -c",
			"--sync", "--track", "--id-nth", "3,4", "--bind", "start:pos(" + strconv.Itoa(first) + ")",
			"--delimiter", "\t", "--with-nth", "8..", "--tabstop", "1",
			"--no-sort", "--ansi", "--layout", "reverse", "--no-input",
			"--header", e.ghTitle(), "--header-first", "--footer", ghFooter(), "--footer-border", "none",
			"--border", "sharp", "--highlight-line", "--pointer", " ", "--marker", " ", "--ellipsis", "…",
			"--no-scrollbar", "--color", FzfColors,
			"--prompt", "/ ", "--ghost", "search", "--info", "hidden", "--no-separator",
			"--preview", q + " _github card {2} {3} {4}", "--preview-window", pw,
			"--bind", "load:transform(" + q + " _github chrome)",
			"--bind", `focus:transform:case {1} in i*) ;; *) case $FZF_ACTION in up|page-up|half-page-up|offset-up|scroll-up|up-match|prev-*) t={5}; [ -n "$t" ] || t={6} ;; *) t={6}; [ -n "$t" ] || t={5} ;; esac; [ -n "$t" ] && echo "pos($t)" ;; esac`,
			"--bind", "j:down,k:up,g,home:first,G,end:last",
			"--bind", "enter,double-click,alt-N,alt-n:transform:case {1} in i*) echo 'execute-silent(" + q + " _from github {2} {3} {4})' ;; esac",
			"--bind", "alt-R,alt-r:transform:case {1} in i*) echo 'execute-silent(" + q + " _from github {2} {3} {4} --review)' ;; esac",
			"--bind", "o:transform:case {1} in i*) echo 'execute-silent(open {7})' ;; esac",
			"--bind", "s:execute-silent(" + q + " _github switch)+" + reload + "+first",
			"--bind", "ctrl-r:execute-silent(" + q + " _github fetch-bg)+change-header(" + strings.ReplaceAll(e.ghTitle(), ")", "") + "   …)",
			"--bind", "/:show-input+unbind(" + ghKeys + ")+transform(" + q + " _github chrome)",
			"--bind", "esc:clear-query+hide-input+rebind(" + ghKeys + ")+transform(" + q + " _github chrome)",
			"--bind", "q:ignore",
		}
		c := exec.Command("fzf", args...)
		c.Stdin = strings.NewReader(rows)
		c.Stderr = os.Stderr
		err := c.Run()
		// ctrl-c ends fzf: the view carries on, not without its terminal.
		if x, ok := err.(*exec.ExitError); ok && x.ExitCode() == 2 {
			return nil
		}
		t, err := os.Open("/dev/tty")
		if err != nil {
			return nil
		}
		_ = t.Close() // opened only to see that there is one
		time.Sleep(200 * time.Millisecond)
	}
}

func firstArg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}
