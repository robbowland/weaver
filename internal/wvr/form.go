package wvr

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/cli"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/git"
	"github.com/robbowland/weaver/internal/items"
	"github.com/robbowland/weaver/internal/oneline"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
	"github.com/robbowland/weaver/internal/tmux"
	"golang.org/x/sys/unix"
)

// The new-thread form (`wvr new`, n on the board, Alt N): fzf with the
// task as its query and the thread's choices as its header. While it is
// open its fields live in a file, as JSON (form); a long task written in
// $EDITOR in FILE.task.

// form is the thread the form would start: the repository (its main
// checkout; empty: not in one), Here (the checkout "this checkout" means),
// the branch as you edited it (empty: from the task), Where (worktree |
// here), the agent (claude | codex), the base (empty: the default), Mode
// (review: read-only; empty: work) and the work item it starts from.
type form struct {
	Repo   string     `json:"repo"`
	Here   string     `json:"here"`
	Branch string     `json:"branch"`
	Where  Where      `json:"where"`
	Agent  agent.Kind `json:"agent"`
	Base   string     `json:"base"`
	Mode   items.Mode `json:"mode,omitempty"`
	Item   *items.Box `json:"item,omitempty"`
}

func loadForm(f string) form {
	fm := form{Where: WhereWorktree, Agent: agent.Claude}
	bestEffort(store.Read(f, &fm)) // none yet: the defaults
	return fm
}

func (fm form) save(f string) {
	bestEffort(store.Write(f, fm)) // unsaved, the form starts from what it had
}

func tildify(p string) string {
	h := os.Getenv("HOME")
	switch {
	case p == h:
		return "~"
	case strings.HasPrefix(p, h+"/"):
		return "~" + p[len(h):]
	}
	return p
}

// formPath is a path as the form shows it: with ~, and only its last two
// parts when long.
func formPath(p string) string {
	t := tildify(p)
	if rlen(t) > 40 {
		d := p[:max(strings.LastIndexByte(p, '/'), 0)]
		t = "…/" + filepath.Base(d) + "/" + filepath.Base(p)
	}
	return t
}

func agentCmd(a agent.Kind) string {
	switch a {
	case agent.Claude:
		if c := os.Getenv("WEAVER_CLAUDE"); c != "" {
			return c
		}
		return "claude"
	case agent.Codex:
		if c := os.Getenv("WEAVER_CODEX"); c != "" {
			return c
		}
		return "codex"
	}
	return ""
}

func agentInstalled(a agent.Kind) bool {
	c := agentCmd(a)
	if c == "" {
		return false
	}
	_, err := exec.LookPath(strings.Fields(c)[0])
	return err == nil
}

// fetchFile is where repo's fetch keeps its state: .pid while it runs, .rc
// (git's status, or "none") once done.
func (e *Env) fetchFile(repo string) string {
	return e.StateDir + "/fetch/" + strings.ReplaceAll(repo, "/", "%")
}

// fetchStart fetches repo's remote in the background, unless that is under
// way or succeeded in the last minute. Its own session, so ssh can never ask
// for a passphrase on the form's terminal; when done it redraws form sf's
// header, if the form is open.
func (e *Env) fetchStart(repo, sf string) {
	if repo == "" {
		return
	}
	f := e.fetchFile(repo)
	if os.MkdirAll(filepath.Dir(f), 0o755) != nil {
		return
	}
	if p, err := files.FirstLine(f + ".pid"); err == nil {
		if n, ok := atoi(p); ok && syscall.Kill(n, 0) == nil {
			return
		}
	}
	if rc, err := files.FirstLine(f + ".rc"); err == nil && rc == "0" {
		if st, err := os.Stat(f + ".rc"); err == nil && time.Since(st.ModTime()) < time.Minute {
			return
		}
	}
	os.Remove(f + ".rc")
	os.Remove(f + ".pid")
	rm := git.PreferredRemote(repo)
	if rm == "" {
		os.WriteFile(f+".rc", []byte("none\n"), 0o644)
		return
	}
	c := exec.Command(e.Self, "_fetch", repo, rm, f, sf)
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if c.Start() == nil {
		os.WriteFile(f+".pid", []byte(strconv.Itoa(c.Process.Pid)+"\n"), 0o644)
		c.Process.Release()
	}
}

// Fetch is `wvr _fetch REPO REMOTE FETCHF [FORM]`: the fetch itself.
func (e *Env) Fetch(repo, remote, f, sf string) {
	c := exec.Command("git", "-C", repo, "fetch", "--prune", "--quiet", remote)
	c.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if ef, err := os.Create(f + ".err"); err == nil {
		c.Stderr = ef
		defer ef.Close()
	}
	rc := 0
	if err := c.Run(); err != nil {
		rc = 1
		if x, ok := err.(*exec.ExitError); ok {
			rc = x.ExitCode()
		}
	}
	tmp := f + ".rc." + strconv.Itoa(os.Getpid())
	if os.WriteFile(tmp, []byte(strconv.Itoa(rc)+"\n"), 0o644) == nil {
		os.Rename(tmp, f+".rc")
	}
	os.Remove(f + ".pid")
	if sf == "" {
		return
	}
	if s := e.formSock(sf); files.Exists(s) {
		session.FzfPost(s, "reload("+session.Quote(e.Self)+" _form rows "+session.Quote(sf)+" {q})")
	}
}

// formSock is form sf's fzf socket (in SockDir: macOS caps a socket's path
// at about 104 bytes).
func (e *Env) formSock(sf string) string {
	return e.SockDir + "/form." + sf[strings.LastIndexByte(sf, '.')+1:] + ".sock"
}

// formField is one of the form's rows: its id (what enter and a click on it
// do), its label, and its value, plain and coloured.
type formField struct{ id, label, plain, col string }

// FormHeader is the form's header for form file sf and the task typed so
// far: what went wrong starting the thread, and how much of the task is
// in $EDITOR's file beyond its first line.
func (e *Env) FormHeader(sf, task, errs string) string {
	var b strings.Builder
	if errs != "" {
		b.WriteString("        " + CRed + errs + COff + "\n")
	}
	if first, err := files.FirstLine(sf + ".task"); err == nil {
		bs, _ := os.ReadFile(sf + ".task")
		n := strings.Count(string(bs), "\n")
		if len(bs) > 0 && bs[len(bs)-1] != '\n' {
			n++
		}
		if lines := n - 1; first == task && lines > 0 {
			b.WriteString("        " + CGrey + "+" + strconv.Itoa(lines) + " more lines" + COff + CDim + " (^e)" + COff + "\n")
		}
	}
	// Its line stays when it says nothing, so a message coming or going
	// never moves the rows.
	if b.Len() == 0 {
		return " \n"
	}
	return b.String()
}

// formFields are the thread's choices, for form file sf and the task typed
// so far.
func (e *Env) formFields(sf, task string) []formField {
	fm := loadForm(sf)
	var fs []formField
	add := func(id, l, p, c string) { fs = append(fs, formField{id, l, p, c}) }
	if fm.Item != nil {
		it := fm.Item.Item
		title := it.Content().Title
		if rlen(title) > 56 {
			title = oneline.Cut(title, 55) + "…"
		}
		add("from", "from", it.Label()+"  "+title, CBold+it.Label()+COff+"  "+title)
		if fm.Mode == items.ModeReview {
			add("mode", "mode", "review  read-only", "review  "+CGrey+"read-only"+COff)
		} else {
			add("mode", "mode", "work", "work")
		}
	}
	if fm.Repo != "" {
		t := formPath(fm.Repo)
		add("repo", "repo", filepath.Base(fm.Repo)+"  "+t, CBold+filepath.Base(fm.Repo)+COff+"  "+CGrey+t+COff)
	} else {
		add("repo", "repo", "not in a git repository", CGrey+"not in a git repository"+COff)
	}
	if fm.Where == WhereHere {
		cur, _ := git.Line(fm.Here, "symbolic-ref", "--short", "-q", "HEAD")
		if cur == "" {
			cur = "this directory"
		}
		t := formPath(fm.Here)
		add("where", "where", cur+"  "+t, cur+"  "+CGrey+t+COff)
		// The rows stay, so nothing below moves: they say why they do not
		// apply, and choosing one makes it a new worktree again.
		add("branch", "branch", cur+"  the checkout's", cur+"  "+CGrey+"the checkout's"+COff)
		add("base", "base", "-  it works where it is", CDim+"-"+COff+"  "+CGrey+"it works where it is"+COff)
	} else {
		add("where", "where", "new worktree", "new worktree")
		br := fm.Branch
		if br == "" {
			br = items.Slugify(task)
		}
		var m git.Refs
		if fm.Repo != "" {
			m = git.RefFacts(fm.Repo, br)
		}
		switch {
		case br == "":
			add("branch", "branch", "from the task", CGrey+"from the task"+COff)
			e.baseRow(fm, m, add)
		case fm.Repo != "" && (m.Has("refs/heads/"+br) ||
			func() bool {
				rm := git.PreferredRemote(fm.Repo)
				return rm != "" && m.Has("refs/remotes/"+rm+"/"+br)
			}()):
			add("branch", "branch", br+"  exists: continue on it", br+"  "+CGrey+"exists: continue on it"+COff)
			add("base", "base", "-  it carries on from the branch", CDim+"-"+COff+"  "+CGrey+"it carries on from the branch"+COff)
		default:
			add("branch", "branch", br, br)
			e.baseRow(fm, m, add)
		}
	}
	if agentInstalled(fm.Agent) {
		add("agent", "agent", string(fm.Agent), string(fm.Agent))
	} else {
		add("agent", "agent", string(fm.Agent)+"  not installed", string(fm.Agent)+"  "+CGrey+"not installed"+COff)
	}
	return fs
}

// formStart is the form's first row, the one enter starts the thread from:
// what it would start, in a line.
func formStart(fm form, task string) string {
	what := string(fm.Agent)
	if fm.Mode == items.ModeReview {
		what += ", read-only,"
	}
	// An item's thread with no task starts its agent told nothing.
	waits := ""
	if fm.Item != nil && strings.TrimSpace(task) == "" {
		waits = ", waiting for you"
	}
	if fm.Where == WhereHere {
		where := filepath.Base(fm.Here)
		if br, _ := git.Line(fm.Here, "symbolic-ref", "--short", "-q", "HEAD"); br != "" {
			where = br
		}
		return CGrey + what + " in " + where + ", where it is" + waits + COff
	}
	br := fm.Branch
	if br == "" {
		br = items.Slugify(task)
	}
	if br == "" {
		return CGrey + what + " in a new worktree" + waits + COff
	}
	return CGrey + what + " in a new worktree, on " + br + waits + COff
}

// formGhost is the task's placeholder: an item's thread may start with
// none.
func formGhost(fm form) string {
	if fm.Item != nil {
		return "what should the agent do? (nothing: it waits for you)"
	}
	return "what should the agent do?"
}

// FormRows are the form's rows for fzf: id, then what it shows.
// The form's rows are dense, grouped by what they are about with a blank
// line between groups only (what it starts from, where it works, who works):
// nearness says what belongs together, and a terminal's least gap is a
// whole line, twice a row's height.
var formGroup = map[string]int{"from": 1, "mode": 1, "repo": 2, "where": 2, "branch": 2, "base": 2, "agent": 3}

func (e *Env) FormRows(sf, task string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "start\t%s%6s%s  %s\n", CBold+CInk, "start", COff, formStart(loadForm(sf), task))
	last := 0
	for _, f := range e.formFields(sf, task) {
		if g := formGroup[f.id]; g != last {
			fmt.Fprintf(&b, "gap%d\t \n", g)
			last = g
		}
		fmt.Fprintf(&b, "%s\t%s%6s%s  %s\n", f.id, CGrey, f.label, COff, f.col)
	}
	return b.String()
}

// formFocus is what the form does as its cursor lands on row id: its keys
// in the footer, or, on a gap between groups, on in the direction it was
// going ($FZF_ACTION).
func formFocus(id string) string {
	if !strings.HasPrefix(id, "gap") {
		if a, ok := fzfAct("change-footer", formRowFooter(id)); ok {
			return a + "\n"
		}
		return ""
	}
	switch a := os.Getenv("FZF_ACTION"); {
	case a == "up", a == "page-up", a == "half-page-up", a == "scroll-up", strings.HasPrefix(a, "prev"), a == "first":
		return "up\n"
	}
	return "down\n"
}

// formKeys are each row's key, for the footer: the row's action from
// anywhere in the form.
var formKeys = map[string]string{"repo": "^r", "where": "alt-w", "branch": "alt-b", "base": "alt-B", "agent": "alt-a", "mode": "alt-r"}

// keyName is fzf's name for a key as weaver writes keys: alt-w is Alt w,
// as in the help and the README (^r stays).
func keyName(k string) string {
	if rest, ok := strings.CutPrefix(k, "alt-"); ok {
		return "Alt " + rest
	}
	return k
}

// formDoes is what enter does on each of the form's rows that has a
// choice (start starts).
var formDoes = map[string]string{"repo": "PICK THE REPOSITORY", "where": "PICK WHERE IT WORKS", "branch": "NAME THE BRANCH",
	"base": "PICK WHERE IT STARTS", "agent": "SWITCH THE AGENT", "mode": "SWITCH WORK / READ-ONLY"}

// formEnter is what enter does on row id, with the key that does it from
// anywhere ("" on a row with nothing to do).
func formEnter(id string) string {
	switch {
	case id == "start" || id == "":
		return "START"
	case formDoes[id] != "":
		return formDoes[id] + " (" + keyName(formKeys[id]) + ")"
	}
	return ""
}

// formHints are the form's footer with the cursor on row id: what enter
// does there, then how to move, the editor and cancelling. The same hints
// in the same places on every row, enter's slot as wide as its widest, so
// nothing in it moves as the cursor does: only what enter does changes.
func formHints(id string) []choice {
	w := rlen(formEnter("start"))
	for f := range formDoes {
		w = max(w, rlen(formEnter(f)))
	}
	what := formEnter(id)
	what += strings.Repeat(" ", w-rlen(what))
	return []choice{{"⏎", what}, {"↑↓", "FIELDS"}, {"^e", "TASK IN $EDITOR"}, {"esc", "CANCEL"}}
}

// formRowFooter is the form's footer on row id: its hints, each a button.
func formRowFooter(id string) string { return hintLine(formHints(id), 3) }

// baseRow is the form's base row: where the new branch starts, and how the
// fetch is going.
func (e *Env) baseRow(fm form, m git.Refs, add func(id, l, p, c string)) {
	if fm.Base != "" {
		note := ""
		switch fm.Base {
		case "@":
			cur, ok := git.Line(fm.Here, "symbolic-ref", "--short", "-q", "HEAD")
			if fm.Here == "" {
				cur, ok = git.Line(fm.Repo, "symbolic-ref", "--short", "-q", "HEAD")
			}
			if !ok || cur == "" {
				cur = "this checkout"
			}
			note = "the HEAD of " + cur
		case "^":
			note = "the local default branch"
		}
		p, c := fm.Base, fm.Base
		if note != "" {
			p += "  " + note
			c += "  " + CGrey + note + COff
		}
		add("base", "base", p, c)
		return
	}
	if fm.Repo == "" {
		add("base", "base", "-  no repository", CDim+"-"+COff+"  "+CGrey+"no repository"+COff)
		return
	}
	v, note := "", ""
	if rm := git.PreferredRemote(fm.Repo); rm == "" {
		v, note = m.LocalDefault(""), "local: no remote"
		if v == "" {
			v = "the default branch"
		}
	} else {
		v = m.RemoteDefault(rm)
		if v == "" {
			v = rm
		}
		f := e.fetchFile(fm.Repo)
		rc, _ := files.FirstLine(f + ".rc")
		pid, _ := files.FirstLine(f + ".pid")
		switch {
		case rc == "0":
			note = "fetched"
		case rc != "":
			v, note = m.LocalDefault(rm), "local: could not fetch "+rm
			if v == "" {
				v = "the default branch"
			}
		case pid != "":
			if n, ok := atoi(pid); ok && syscall.Kill(n, 0) == nil {
				note = "fetching…"
			}
		}
	}
	p, c := v, v
	if note != "" {
		p += "  " + note
		c += "  " + CGrey + note + COff
	}
	add("base", "base", p, c)
}

// newFormLabel is the new-thread form's label: a review says so.
func newFormLabel(fm form) string {
	if fm.Mode == items.ModeReview {
		return formLabel("WEAVER", "REVIEW")
	}
	return formLabel("WEAVER", "NEW THREAD")
}

// formLabel is a form's border label in the board's grammar:
// [ WHERE :: WHAT ], the brackets and :: faint, where in ink, what grey.
func formLabel(where, what string) string {
	return " " + CDim + "[" + COff + " " + CBold + CInk + where + COff + " " + CDim + "::" + COff + " " + CGrey + what + COff +
		" " + CDim + "]" + COff + " "
}

var formFzf = []string{"--border", "sharp", "--border-label-pos", "3", "--footer-border", "none", "--pointer", " ", "--marker", " ", "--no-info", "--no-separator", "--ansi", "--height", "100%", "--highlight-line", "--scrollbar", "│"}

// pickHints are a picker's footer, pickFooter as it shows.
var (
	pickHints  = []choice{{"⏎", "PICK"}, {"↑↓", "MOVE"}, {"esc", "BACK"}}
	pickFooter = hintLine(pickHints, 3)
)

// checkThread: can a thread start from task and the form's fields? Why
// not, or "".
func checkThread(task string, fm form) string {
	if strings.TrimSpace(task) == "" && fm.Item == nil {
		return "say what the agent should do"
	}
	if !agentInstalled(fm.Agent) {
		return string(fm.Agent) + " is not installed"
	}
	if fm.Where == WhereHere {
		if st, err := os.Stat(fm.Here); err != nil || !st.IsDir() {
			return "no such directory: " + fm.Here
		}
		return ""
	}
	if fm.Repo == "" {
		return "not in a git repository: pick one (repo), or work in this directory (where)"
	}
	br := fm.Branch
	if br == "" {
		br = items.Slugify(task)
	}
	switch {
	case br == "":
		return "name the branch (alt-b): the task has no letters to name it after"
	case exec.Command("git", "check-ref-format", "--branch", br).Run() != nil:
		return "not a valid branch name: " + br
	}
	switch {
	case fm.Base == "" || fm.Base == "@" || fm.Base == "^" || strings.HasPrefix(fm.Base, "pr:") || strings.HasPrefix(fm.Base, "mr:"):
	case !git.OK(fm.Repo, "rev-parse", "--verify", "--quiet", fm.Base+"^{commit}"):
		return "no such base: " + fm.Base
	}
	return ""
}

// New is `wvr new [options] [TASK]`: a thread for TASK (or, without
// one, the form's), its worktree and its window. ok false for --help.
func (e *Env) New(args []string) error {
	var repoArg, branch, base, agentArg, itemArg string
	var from tmux.PaneID
	var where Where
	var itemKind items.GitHubKind
	var task []string
	nofocus, edit := false, false
	mode := items.ModeWork
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--repo", "--from", "--branch", "-b", "--base", "--agent", "--pr", "--issue":
			if i+1 >= len(args) {
				return errors.New(a + " needs a value")
			}
			v := args[i+1]
			switch a {
			case "--repo":
				repoArg = v
			case "--from":
				from, _ = tmux.ParsePane(v)
			case "--branch", "-b":
				branch = v
			case "--base":
				base = v
			case "--agent":
				agentArg = v
			case "--pr", "--issue":
				itemArg, itemKind = v, items.GitHubKind(strings.TrimPrefix(a, "--"))
			}
			i++
		case "--review":
			mode = items.ModeReview
		case "--here":
			where = WhereHere
		case "--worktree":
			where = WhereWorktree
		case "--no-focus":
			nofocus = true
		case "--edit", "-e":
			edit = true
		case "--":
			task = append(task, args[i+1:]...)
			i = len(args)
		case "-h", "--help":
			return cli.ErrHelp
		default:
			if strings.HasPrefix(a, "-") {
				return errors.New("unknown option: " + a + " (see wvr help)")
			}
			task = append(task, a)
		}
	}
	if e.Session == "" {
		return errors.New("not inside tmux: start it with `wvr`, then Alt N (or wvr new) there")
	}
	kind, ok := agent.ParseKind(agentArg)
	if !ok {
		return errors.New("unknown agent: " + agentArg + " (claude or codex)")
	}
	// Where it starts: --repo; the agent of --from's repository; else here.
	start, _ := os.Getwd()
	switch {
	case repoArg != "":
		start = repoArg
		if st, err := os.Stat(start); err != nil || !st.IsDir() {
			return errors.New("no such directory: " + repoArg)
		}
	case from != "":
		f := ""
		if s, err := store.ReadState(e.StateFile(from)); err == nil {
			f = s.Root
			if f == "" {
				f = s.Cwd
			}
		}
		if st, err := os.Stat(f); f == "" || err != nil || !st.IsDir() {
			f = ""
			if t, err := store.ReadThread(e.ThreadFile(from)); err == nil {
				f = t.Worktree
			}
		}
		if st, err := os.Stat(f); f != "" && err == nil && st.IsDir() {
			start = f
		}
	}
	if where == "" {
		where = WhereWorktree
	}
	fm := form{Branch: branch, Where: where, Agent: kind, Base: base}
	fm.Mode = mode
	if r, c, ok := git.RepoOf(start); ok {
		fm.Repo, fm.Here = r, c
	} else {
		if repoArg != "" {
			return errors.New("not a git repository: " + repoArg)
		}
		if d, err := filepath.EvalSymlinks(start); err == nil {
			fm.Here = d
		} else {
			fm.Here = start
		}
		// Not in a repository: the one you used last, for a new worktree.
		if l, err := files.FirstLine(e.StateDir + "/repos"); err == nil && l != "" {
			if st, err := os.Stat(l); err == nil && st.IsDir() {
				if r, _, ok := git.RepoOf(l); ok {
					fm.Repo = r
				}
			}
		}
	}
	t := strings.Join(task, " ")
	// A work item: its repository's checkout, its branch, and the task for
	// it, read before the form opens.
	repoErr := ""
	if itemArg != "" {
		// In a popup (Alt N in a view), what went wrong stays on screen
		// until a key: the popup would close with it.
		dieItem := func(m string) error {
			if cli.IsTTY(0) && cli.IsTTY(2) && t == "" {
				fmt.Fprintf(os.Stderr, "%sweaver: %s%s\n\n%spress a key%s", CRed, m, COff, CGrey, COff)
				readKey()
				return cli.ErrSaid
			}
			return errors.New(m)
		}
		ref, err := items.ParseRef(itemKind, itemArg)
		if err != nil {
			return dieItem(err.Error())
		}
		if r, ok := ref.(items.GitHubRef); ok {
			if r.Repo == "" {
				r.Repo = items.GitHubRepo(start)
				ref = r
			}
			if r.Repo != "" && (fm.Repo == "" || !strings.EqualFold(items.GitHubRepo(fm.Repo), r.Repo)) {
				if c := e.checkoutFor(r.Repo, fm.Repo); c != "" {
					fm.Repo, fm.Here = c, c
				} else {
					fm.Repo = ""
					repoErr = "no checkout of " + r.Repo + " known: pick it (^r)"
				}
			}
		}
		if cli.IsTTY(2) {
			fmt.Fprintf(os.Stderr, "%sweaver: reading %s…%s", CGrey, items.RefName(ref), COff)
		}
		x, err := items.Fetch(ref, start)
		if cli.IsTTY(2) {
			fmt.Fprint(os.Stderr, "\r\033[K")
		}
		if err != nil {
			return dieItem(err.Error())
		}
		if branch == "" {
			fm.Branch = x.BranchName()
		}
		fm.Where = WhereWorktree
		fm.Item = &items.Box{Item: x}
		// Already a thread on its branch: go to it.
		if fm.Repo != "" {
			if a := e.threadOnBranch(fm.Repo, fm.Branch); a != "" {
				e.focus(a)
				e.MarkSeen(a)
				fmt.Printf("weaver: %s already has a thread (%s): went to it\n", x.Ref(), fm.Branch)
				return nil
			}
		}
	}
	if edit {
		f, err := os.CreateTemp("", "weaver-task.")
		if err != nil {
			return errors.New("cannot make a temporary file")
		}
		if t != "" {
			f.WriteString(t + "\n")
		}
		f.Close()
		defer os.Remove(f.Name())
		ed := os.Getenv("VISUAL")
		if ed == "" {
			ed = os.Getenv("EDITOR")
		}
		if ed == "" {
			ed = "vi"
		}
		w := strings.Fields(ed)
		c := exec.Command(w[0], append(w[1:], f.Name())...)
		if tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
			c.Stdin, c.Stdout = tty, tty
			defer tty.Close()
		}
		c.Stderr = os.Stderr
		c.Run()
		b, _ := files.ReadTrim(f.Name())
		if strings.TrimSpace(b) == "" {
			return cli.ErrCancelled
		}
		t = b
	}
	if t != "" {
		if repoErr != "" {
			return errors.New(repoErr + " (or --repo DIR)")
		}
		if m := checkThread(t, fm); m != "" {
			return errors.New(m)
		}
		return e.threadCreate(t, fm, nofocus)
	}
	if _, err := exec.LookPath("fzf"); err != nil || !cli.IsTTY(0) || !cli.IsTTY(1) {
		return errors.New(`what should the agent do? weaver new "<task>" (the form needs a terminal and fzf)`)
	}
	os.MkdirAll(e.StateDir, 0o755)
	tf, err := os.CreateTemp(e.StateDir, "form.")
	if err != nil {
		return errors.New("cannot make the form's file")
	}
	sf := tf.Name()
	tf.Close()
	fm.save(sf)
	defer func() {
		os.Remove(sf)
		os.Remove(sf + ".task")
	}()
	// An item's form starts with no task, so its agent is told nothing
	// unless you say (WEAVER_ITEM_TASK=1: weaver's, "Implement ENG-42").
	query := ""
	if fm.Item != nil && os.Getenv("WEAVER_ITEM_TASK") == "1" {
		query = fm.Item.Item.DefaultTask(mode)
	}
	for {
		t, err := e.newForm(sf, query, repoErr)
		if err != nil {
			return err
		}
		query, repoErr = t, ""
		if i := strings.IndexByte(query, '\n'); i >= 0 {
			query = query[:i]
		}
		err = e.threadCreate(t, loadForm(sf), nofocus)
		if err == nil {
			return nil
		}
		// The thread could not be made: say why, unless that is on screen.
		if !errors.Is(err, cli.ErrSaid) {
			fmt.Fprintln(os.Stderr, "weaver: "+err.Error())
		}
		fmt.Fprintf(os.Stderr, "\n%sweaver: no thread started.%s\n", CRed, COff)
		tt, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			return cli.ErrCancelled
		}
		c := choose(tt, "", []choice{{"⏎", "back to the form"}, {"esc", "cancel"}}, 0)
		_ = tt.Close()
		if c != 0 {
			return cli.ErrCancelled
		}
	}
}

// readKey is one key from the terminal, unechoed.
func readKey() byte {
	t, err := os.Open("/dev/tty")
	if err != nil {
		return 0x1b
	}
	defer t.Close()
	fd := int(t.Fd())
	old, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err == nil {
		raw := *old
		raw.Lflag &^= unix.ECHO | unix.ICANON
		raw.Cc[unix.VMIN], raw.Cc[unix.VTIME] = 1, 0
		unix.IoctlSetTermios(fd, unix.TIOCSETA, &raw)
		defer unix.IoctlSetTermios(fd, unix.TIOCSETA, old)
	}
	b, err := bufio.NewReader(t).ReadByte()
	if err != nil {
		return 0x1b
	}
	return b
}

// newForm is the form until you start a thread it can start: the task, or
// 130 when you cancel.
func (e *Env) newForm(sf, query, errs string) (string, error) {
	q := session.Quote(e.Self)
	qs := session.Quote(sf)
	hd := q + " _form header " + qs + " {q}"
	rows := q + " _form rows " + qs + " {q}"
	fm := loadForm(sf)
	// The fetch for a new branch's base starts now, while you type (and
	// while the header is made).
	if fm.Where == WhereWorktree && fm.Base == "" {
		go e.fetchStart(fm.Repo, sf)
	}
	if st, err := os.Stat(e.SockDir); err != nil || !st.IsDir() {
		os.MkdirAll(e.SockDir, 0o700)
		os.Chmod(e.SockDir, 0o700)
	}
	for {
		h := strings.TrimRight(e.FormHeader(sf, query, errs), "\n")
		sock := e.formSock(sf)
		os.Remove(sock)
		args := []string{"--listen", sock, "--disabled", "--reverse"}
		args = append(args, formFzf...)
		// The task is the query; the rows are what enter (or a click) acts
		// on: start, first, then each choice, which ↑↓, tab and the mouse
		// move between. After a choice, the cursor is back on start.
		args = append(args, "--border-label", newFormLabel(loadForm(sf)), "--prompt", formPrompt("task"), "--ghost", formGhost(loadForm(sf)),
			"--query", query, "--header", h, "--footer", formRowFooter("start"), "--color", FzfColors,
			// No --track: fzf would hold keys while a reload looked for the
			// row. Typing is for the task, and puts the cursor on start.
			"--delimiter", "\t", "--with-nth", "2..", "--cycle",
			"--bind", "change:bg-cancel+bg-transform-header("+hd+")+reload("+rows+")+first",
			"--bind", "focus:transform("+q+" _form focus {1})",
			"--bind", "tab:down,btab:up",
			"--bind", "enter,left-click:transform("+q+" _form keys {1} "+qs+")",
			"--bind", "double-click:ignore",
			"--bind", "click-footer:transform("+q+" _click form {1})",
			"--bind", "load:transform("+q+" _form land "+qs+" {q})")
		args = append(args, roomy()...)
		for id, k := range formKeys {
			args = append(args, "--bind", strings.Replace(k, "^", "ctrl-", 1)+":"+formAct(q, qs, id))
		}
		args = append(args,
			"--bind", "ctrl-e:execute("+q+" _form editor "+qs+" {q})+transform-query("+q+" _form query "+qs+" {q})+transform-header("+hd+")+reload("+rows+")")

		c := fzfCmd(args, "SHELL=/bin/sh")
		c.Stdin = strings.NewReader(e.FormRows(sf, query))
		c.Stderr = os.Stderr
		out, err := c.Output()
		os.Remove(sock)
		rc := 0
		if x, ok := err.(*exec.ExitError); ok {
			rc = x.ExitCode()
		}
		if rc == 130 {
			return "", cli.ErrCancelled
		}
		if rc > 1 {
			return "", errors.New("fzf failed (the form needs fzf 0.62 or newer)")
		}
		query = strings.TrimRight(string(out), "\n")
		if i := strings.IndexByte(query, '\n'); i >= 0 {
			query = query[:i]
		}
		task := query
		if first, err := files.FirstLine(sf + ".task"); err == nil && first == query {
			if b, err := files.ReadTrim(sf + ".task"); err == nil {
				task = b
			}
		}
		if errs = checkThread(task, loadForm(sf)); errs == "" {
			return task, nil
		}
	}
}

// formAct is the action that changes the form's row id (a picker, which
// needs the terminal, or a switch, which does not), then shows the rows
// again with the cursor where it was (the row acted on, or the one its key
// was pressed on): never moved on for you. The row is kept beside the form
// (.at) for the reload's load to land on (_form land).
func formAct(q, qs, id string) string {
	ex := "execute"
	if id == "agent" || id == "mode" {
		ex = "execute-silent"
	}
	return "execute-silent(printf '%s' {1} >" + qs + ".at)+" + ex + "(" + q + " _form " + id + " " + qs + " {q})+reload(" + q + " _form rows " + qs + " {q})" +
		"+transform-header(" + q + " _form header " + qs + " {q})+transform-border-label(" + q + " _form label " + qs + ")"
}

// Form is `wvr _form OP FILE [QUERY]`: what the form's keys do, run by fzf;
// `wvr _form footer ID` and `wvr _form keys ID FILE` for its rows.
func (e *Env) Form(op, sf, q string) {
	switch op {
	case "footer":
		fmt.Print(formRowFooter(sf))
		return
	case "focus":
		fmt.Print(formFocus(sf))
		return
	case "keys":
		// What enter (or a click) on row sf does: start the thread, change
		// that choice, or nothing.
		switch id := sf; {
		case id == "start":
			fmt.Print("print-query")
		case formKeys[id] != "":
			fmt.Print(formAct(session.Quote(e.Self), session.Quote(q), id))
		}
		return
	}
	fm := loadForm(sf)
	switch op {
	case "header":
		fmt.Print(e.FormHeader(sf, q, ""))
	case "rows":
		fmt.Print(e.FormRows(sf, q))
	case "land":
		// After a choice: the cursor back on the row it was on.
		at, err := files.FirstLine(sf + ".at")
		os.Remove(sf + ".at")
		if err != nil || at == "" {
			return
		}
		for i, l := range strings.Split(e.FormRows(sf, q), "\n") {
			if id, _, _ := strings.Cut(l, "\t"); id == at {
				fmt.Printf("pos(%d)\n", i+1)
			}
		}
	case "fields":
		// The choices as text, as the form shows them.
		for _, f := range e.formFields(sf, q) {
			fmt.Printf("%6s  %s\n", f.label, f.plain)
		}
	case "where":
		if fm.Repo == "" {
			// Not a repository: only where it is.
			fm.Where = WhereHere
			fm.save(sf)
			return
		}
		out := e.pick(e.formWheres(fm), formLabel("WEAVER", "NEW THREAD › WHERE"), formPrompt("where"), "a new worktree, or a checkout that is there", whereValue(fm))
		d := pickValue(out)
		switch d {
		case "":
			return
		case "new":
			fm.Where = WhereWorktree
			if fm.Base == "" {
				e.fetchStart(fm.Repo, sf)
			}
		default:
			if strings.HasPrefix(d, "~/") {
				d = os.Getenv("HOME") + d[1:]
			}
			r, c, ok := git.RepoOf(d)
			if !ok || r != fm.Repo {
				return
			}
			fm.Where, fm.Here = WhereHere, c
		}
		fm.save(sf)
	case "label":
		fmt.Print(newFormLabel(fm))
	case "mode":
		if fm.Mode == items.ModeReview {
			fm.Mode = items.ModeWork
		} else {
			fm.Mode = items.ModeReview
		}
		fm.save(sf)
	case "agent":
		if fm.Agent == agent.Claude {
			fm.Agent = agent.Codex
		} else {
			fm.Agent = agent.Claude
		}
		fm.save(sf)
	case "base":
		// A base means a new worktree.
		if fm.Where == WhereHere {
			fm.Where = WhereWorktree
			fm.save(sf)
		}
		if fm.Repo == "" {
			return
		}
		out := e.pick(e.formBases(fm), formLabel("WEAVER", "NEW THREAD › BASE"), formPrompt("base"), "where the new branch starts", baseValue(fm))
		if out == "" {
			return
		}
		d := pickValue(out)
		if d == "" {
			return
		}
		if d == "=" {
			d = ""
		}
		fm.Base = d
		fm.save(sf)
		if fm.Base == "" {
			e.fetchStart(fm.Repo, sf)
		}
	case "branch":
		// A branch means a new worktree.
		if fm.Where == WhereHere {
			fm.Where = WhereWorktree
			fm.save(sf)
		}
		br := fm.Branch
		if br == "" {
			br = items.Slugify(q)
		}
		args := append([]string{"--disabled", "--reverse"}, formFzf...)
		args = append(args, "--border-label", formLabel("WEAVER", "NEW THREAD › BRANCH"), "--prompt", formPrompt("branch"), "--query", br, "--ghost", "branch name",
			"--color", FzfColors, "--header", "        "+CGrey+"names the branch and its worktree; an existing branch continues"+COff,
			"--footer", CGrey+"⏎"+COff+" "+CDim+"KEEP"+COff+"   "+CGrey+"esc"+COff+" "+CDim+"BACK"+COff, "--bind", "enter:print-query")
		args = append(args, roomy()...)
		c := fzfCmd(args, "SHELL=/bin/sh")
		c.Stderr = os.Stderr
		b, err := c.Output()
		if err != nil {
			return
		}
		out := strings.TrimRight(string(b), "\n")
		if out != "" && exec.Command("git", "check-ref-format", "--branch", out).Run() == nil {
			fm.Branch = out
			if out == items.Slugify(q) {
				fm.Branch = ""
			}
			fm.save(sf)
		}
	case "repo":
		out := e.pick(e.recentRepos(fm.Repo), formLabel("WEAVER", "NEW THREAD › REPOSITORY"), formPrompt("repo"), "a repository, or a path", fm.Repo)
		if out == "" {
			return
		}
		d := pickValue(out)
		switch {
		case d == "~":
			d = os.Getenv("HOME")
		case strings.HasPrefix(d, "~/"):
			d = os.Getenv("HOME") + d[1:]
		}
		if st, err := os.Stat(d); d == "" || err != nil || !st.IsDir() {
			return
		}
		if r, c, ok := git.RepoOf(d); ok {
			fm.Repo, fm.Here = r, c
		} else {
			fm.Repo = ""
			if p, err := filepath.EvalSymlinks(d); err == nil {
				fm.Here = p
			} else {
				fm.Here = d
			}
		}
		// Another repository's bases are its own.
		fm.Base = ""
		fm.save(sf)
		if fm.Where == WhereWorktree {
			e.fetchStart(fm.Repo, sf)
		}
	case "editor":
		first, err := files.FirstLine(sf + ".task")
		if err != nil || first != q {
			os.WriteFile(sf+".task", []byte(q+"\n"), 0o644)
		}
		ed := os.Getenv("VISUAL")
		if ed == "" {
			ed = os.Getenv("EDITOR")
		}
		if ed == "" {
			ed = "vi"
		}
		// $EDITOR may carry arguments ("code -w").
		w := strings.Fields(ed)
		c := exec.Command(w[0], append(w[1:], sf+".task")...)
		if t, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
			c.Stdin, c.Stdout = t, t
			defer t.Close()
		}
		c.Stderr = os.Stderr
		c.Run()
	case "query":
		first := q
		if l, err := files.FirstLine(sf + ".task"); err == nil {
			first = l
		}
		fmt.Println(first)
	}
}

// pick runs a picker over items ("value<TAB>display" lines): fzf's output
// (the query, then the pick). It keeps the form's grid (its prompt where
// the form's labels end, its rows under the form's values, a line between
// them as the form has) and opens on cur, the value chosen now.
func (e *Env) pick(items, label, prompt, ghost, cur string) string {
	var b strings.Builder
	at, n := 0, 0
	for _, l := range strings.Split(strings.TrimRight(items, "\n"), "\n") {
		v, d, _ := strings.Cut(l, "\t")
		n++
		if at == 0 && cur != "" && (v == cur || strings.HasPrefix(v, "~/") && os.Getenv("HOME")+v[1:] == cur) {
			at = n
		}
		b.WriteString(v + "\t" + formIndent + d + "\n")
	}
	args := append([]string{"--reverse", "--sync"}, formFzf...)
	args = append(args, "--border-label", label, "--delimiter", "\t", "--with-nth", "2", "--print-query", "--prompt", prompt,
		"--ghost", ghost, "--color", FzfColors, "--footer", pickFooter, "--header", "",
		// A menu: a click picks, as on the form's rows; the footer's hints
		// are buttons.
		"--bind", "left-click:accept", "--bind", "click-footer:transform("+session.Quote(e.Self)+" _click pick)")
	if at > 0 {
		args = append(args, "--bind", "start:pos("+strconv.Itoa(at)+")")
	}
	args = append(args, roomy()...)
	c := fzfCmd(args, "SHELL=/bin/sh")
	c.Stdin = strings.NewReader(b.String())
	c.Stderr = os.Stderr
	out, _ := c.Output()
	return strings.TrimRight(string(out), "\n")
}

// formIndent puts a picker's rows under the form's values (its labels'
// column, six wide, and two spaces).
const formIndent = "        "

// formPrompt is a prompt that ends where the form's labels do, so what is
// typed starts under the form's values.
func formPrompt(label string) string { return fmt.Sprintf("%8s  ", label) }

// whereValue is the where picker's value for what is chosen now.
func whereValue(fm form) string {
	if fm.Where == WhereHere {
		return fm.Here
	}
	return "new"
}

// baseValue is the base picker's value for what is chosen now ("=": the
// default branch).
func baseValue(fm form) string {
	if fm.Base == "" {
		return "="
	}
	return fm.Base
}

// roomy is fzf's layout for a dialog when the terminal has room: a margin
// inside its border. Rows stay dense (groups have their own gaps).
func roomy() []string {
	if lines, cols, ok := ttySize(); ok && lines >= 20 && cols >= 100 {
		return []string{"--padding", "1,2"}
	}
	return nil
}

// pickValue is a pick's value: the picked item's, else the query typed.
func pickValue(out string) string {
	q, sel, ok := strings.Cut(out, "\n")
	if ok && sel != "" {
		v, _, _ := strings.Cut(sel, "\t")
		return v
	}
	return q
}

// formWheres are where a thread can work, to offer: a new worktree, then
// the repository's checkouts (its main one, its worktrees), each by its
// branch.
func (e *Env) formWheres(fm form) string {
	var b strings.Builder
	b.WriteString("new\tnew worktree  " + CGrey + "a branch of its own, from the base" + COff + "\n")
	// The session's threads, by where they work: another agent there
	// shares its diffs.
	busy := map[string]int{}
	for _, f := range e.SessionFiles("thread") {
		if t, err := store.ReadThread(f); err == nil && t.Worktree != "" {
			busy[t.Worktree] = t.Num
		}
	}
	out, _ := exec.Command("git", "-C", fm.Repo, "worktree", "list", "--porcelain").Output()
	path, br := "", ""
	flush := func() {
		if path == "" {
			return
		}
		if st, err := os.Stat(path); err == nil && st.IsDir() {
			if br == "" {
				br = "detached"
			}
			note := ""
			if path == fm.Repo {
				note = "  " + CDim + "the main checkout" + COff
			}
			if n, ok := busy[path]; ok {
				note += "  " + CDim + fmt.Sprintf("thread %02d works here", n) + COff
			}
			b.WriteString(path + "\t" + br + "  " + CGrey + formPath(path) + COff + note + "\n")
		}
		path, br = "", ""
	}
	for _, l := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(l, "worktree "):
			flush()
			path = strings.TrimPrefix(l, "worktree ")
			if p, err := filepath.EvalSymlinks(path); err == nil {
				path = p
			}
		case strings.HasPrefix(l, "branch refs/heads/"):
			br = strings.TrimPrefix(l, "branch refs/heads/")
		}
	}
	flush()
	return b.String()
}

// formBases are the bases to offer ("=" is the default: the remote's
// default branch, fetched).
func (e *Env) formBases(fm form) string {
	var b strings.Builder
	if rm := git.PreferredRemote(fm.Repo); rm != "" && git.RemoteDefault(fm.Repo, rm) != "" {
		b.WriteString("=\t" + git.RemoteDefault(fm.Repo, rm) + "  " + CGrey + "the remote default, fetched" + COff + "\n")
	} else {
		d := git.LocalDefault(fm.Repo)
		if d == "" {
			d = "default"
		}
		b.WriteString("=\t" + d + "  " + CGrey + "the local default branch" + COff + "\n")
	}
	h := fm.Here
	if h == "" {
		h = fm.Repo
	}
	cur, _ := git.Line(h, "symbolic-ref", "--short", "-q", "HEAD")
	if cur == "" {
		cur = "this checkout"
	}
	b.WriteString("@\t@  " + CGrey + "the HEAD of " + cur + COff + "\n")
	out, _ := exec.Command("git", "-C", fm.Repo, "for-each-ref", "--format=%(refname:short)", "--sort=-committerdate", "refs/heads", "refs/remotes").Output()
	n := 0
	for _, r := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if r == "" || strings.HasSuffix(r, "/HEAD") {
			continue
		}
		if n++; n > 100 {
			break
		}
		b.WriteString(r + "\t" + r + "\n")
	}
	return b.String()
}

// recentRepos are the repositories to offer: first's first, then those of
// recent threads and running agents, then git repositories zoxide knows.
func (e *Env) recentRepos(first string) string {
	var ds []string
	if first != "" {
		ds = append(ds, first)
	}
	if b, err := os.ReadFile(e.StateDir + "/repos"); err == nil {
		ds = append(ds, strings.Split(strings.TrimRight(string(b), "\n"), "\n")...)
	}
	fs, _ := filepath.Glob(e.StateDir + "/*_*.json")
	ts, _ := filepath.Glob(e.StateDir + "/*_*.thread")
	seen := map[string]bool{}
	for _, f := range append(fs, ts...) {
		d := ""
		if strings.HasSuffix(f, ".thread") {
			if t, err := store.ReadThread(f); err == nil {
				d = t.Repo
			}
		} else if s, err := store.ReadState(f); err == nil {
			d = s.Root
			if d == "" {
				d = s.Cwd
			}
		}
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			if r, _, ok := git.RepoOf(d); ok {
				ds = append(ds, r)
			}
		}
	}
	if _, err := exec.LookPath("zoxide"); err == nil {
		out, _ := exec.Command("zoxide", "query", "-l").Output()
		for i, d := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			if i >= 100 {
				break
			}
			if st, err := os.Stat(d + "/.git"); err == nil && st.IsDir() {
				ds = append(ds, d)
			}
		}
	}
	var b strings.Builder
	done := map[string]bool{}
	for _, d := range ds {
		if d == "" || done[d] {
			continue
		}
		done[d] = true
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			continue
		}
		b.WriteString(d + "\t" + filepath.Base(d) + "  " + CGrey + formPath(d) + COff + "\n")
	}
	return b.String()
}
