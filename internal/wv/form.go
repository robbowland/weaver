package wv

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// The new-thread form (`wvr new`, n on the board, Alt N): fzf with the
// task as its query and the thread's choices as its header. While it is
// open its fields live in a file, one per line: repo (the main checkout;
// empty: not in one), here (the checkout "this checkout" means), branch (as
// you edited it; empty: from the task), where (worktree | here), agent
// (claude | codex), base (empty: the default), mode (review: read-only;
// empty: work), item (the work item's reference, its details in
// FILE.item); a long task written in $EDITOR in FILE.task.

type form struct{ repo, here, branch, where, agent, base, mode, item string }

func loadForm(f string) form {
	fm := form{where: "worktree", agent: "claude"}
	b, err := os.ReadFile(f)
	if err != nil {
		return fm
	}
	ls := strings.Split(string(b), "\n")
	get := func(i int) string {
		if i < len(ls) {
			return ls[i]
		}
		return ""
	}
	fm.repo, fm.here, fm.branch, fm.where, fm.agent, fm.base = get(0), get(1), get(2), get(3), get(4), get(5)
	fm.mode, fm.item = get(6), get(7)
	return fm
}

func (fm form) save(f string) {
	tmp := f + "." + strconv.Itoa(os.Getpid())
	if os.WriteFile(tmp, []byte(strings.Join([]string{fm.repo, fm.here, fm.branch, fm.where, fm.agent, fm.base, fm.mode, fm.item}, "\n")+"\n"), 0o644) == nil {
		os.Rename(tmp, f)
	}
}

// git runs git -C dir with args; its output's first line, and whether it
// succeeded.
func git(dir string, args ...string) (string, bool) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	s := strings.TrimRight(string(out), "\n")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s, err == nil
}

func gitOK(dir string, args ...string) bool {
	return exec.Command("git", append([]string{"-C", dir}, args...)...).Run() == nil
}

// repoOf is the repository holding directory d: its main checkout and the
// checkout d is in; ok false outside one.
func repoOf(d string) (repo, checkout string, ok bool) {
	out, err := exec.Command("git", "-C", d, "rev-parse", "--show-toplevel", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return "", "", false
	}
	ls := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(ls) < 2 {
		return "", "", false
	}
	checkout, common := ls[0], ls[1]
	repo = checkout
	if strings.HasSuffix(common, "/.git") {
		repo = strings.TrimSuffix(common, "/.git")
	}
	if st, err := os.Stat(repo); err != nil || !st.IsDir() {
		repo = checkout
	}
	return repo, checkout, true
}

var remotes sync.Map

// preferredRemote is the remote to start from: origin, else the first. Asked
// once per repository (the header asks several times).
func preferredRemote(repo string) string {
	if r, ok := remotes.Load(repo); ok {
		return r.(string)
	}
	r := preferredRemote1(repo)
	remotes.Store(repo, r)
	return r
}

func preferredRemote1(repo string) string {
	out, _ := exec.Command("git", "-C", repo, "remote").Output()
	first := ""
	for _, r := range strings.Fields(string(out)) {
		if first == "" {
			first = r
		}
		if r == "origin" {
			return r
		}
	}
	return first
}

// refs are what a repository's refs say for the form, from one git call:
// each ref that matched (its symref target, or "" for a plain ref).
type refs map[string]string

// refFacts reads the refs the form asks about for branch br: br, locally
// and on any remote, the remotes' HEADs, and main and master. git remote
// runs alongside (preferredRemote is memoized).
func refFacts(repo, br string) refs {
	go preferredRemote(repo)
	pats := []string{"refs/remotes/*/HEAD", "refs/heads/main", "refs/heads/master", "refs/remotes/*/main", "refs/remotes/*/master"}
	if br != "" {
		pats = append(pats, "refs/heads/"+br, "refs/remotes/*/"+br)
	}
	out, _ := exec.Command("git", append([]string{"-C", repo, "for-each-ref", "--format=%(refname)%09%(symref)"}, pats...)...).Output()
	m := refs{}
	for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if r, sym, ok := strings.Cut(l, "\t"); ok {
			m[r] = sym
		}
	}
	return m
}

func (m refs) has(r string) bool { _, ok := m[r]; return ok }

// remoteDefaultOf is remote's default branch (origin/main) from refs: its
// HEAD, else its main or master.
func (m refs) remoteDefault(remote string) string {
	if sym := m["refs/remotes/"+remote+"/HEAD"]; strings.HasPrefix(sym, "refs/remotes/") {
		return strings.TrimPrefix(sym, "refs/remotes/")
	}
	for _, b := range []string{"main", "master"} {
		if m.has("refs/remotes/" + remote + "/" + b) {
			return remote + "/" + b
		}
	}
	return ""
}

// localDefault is the local default branch from refs: the one the remote's
// HEAD names, else main or master.
func (m refs) localDefault(remote string) string {
	r := ""
	if remote != "" {
		if sym := m["refs/remotes/"+remote+"/HEAD"]; strings.HasPrefix(sym, "refs/remotes/"+remote+"/") {
			r = strings.TrimPrefix(sym, "refs/remotes/"+remote+"/")
		}
	}
	for _, b := range []string{r, "main", "master"} {
		if b != "" && m.has("refs/heads/"+b) {
			return b
		}
	}
	return ""
}

// remoteDefault is remote's default branch (origin/main): its HEAD, else
// its main or master.
func remoteDefault(repo, remote string) string {
	if r, ok := git(repo, "symbolic-ref", "--quiet", "--short", "refs/remotes/"+remote+"/HEAD"); ok {
		return r
	}
	for _, b := range []string{"main", "master"} {
		if gitOK(repo, "show-ref", "--verify", "--quiet", "refs/remotes/"+remote+"/"+b) {
			return remote + "/" + b
		}
	}
	return ""
}

// localDefault is the local default branch: the one the remote's HEAD
// names, else main or master; "" when there is none.
func localDefault(repo string) string {
	r := ""
	if rm := preferredRemote(repo); rm != "" {
		if h, ok := git(repo, "symbolic-ref", "--quiet", "--short", "refs/remotes/"+rm+"/HEAD"); ok {
			r = h[strings.IndexByte(h, '/')+1:]
		}
	}
	for _, b := range []string{r, "main", "master"} {
		if b != "" && gitOK(repo, "show-ref", "--verify", "--quiet", "refs/heads/"+b) {
			return b
		}
	}
	return ""
}

// Slugify is a task as a branch name: its first line in lowercase ASCII
// letters and digits joined by '-', at most 48 characters, cut at a '-'.
func Slugify(t string) string {
	if i := strings.IndexByte(t, '\n'); i >= 0 {
		t = t[:i]
	}
	var b strings.Builder
	dash := false
	for i := 0; i < len(t); i++ {
		c := t[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			b.WriteByte(c)
			dash = false
			continue
		}
		if !dash {
			b.WriteByte('-')
		}
		dash = true
	}
	s := strings.TrimSuffix(strings.TrimPrefix(b.String(), "-"), "-")
	if len(s) > 48 {
		s = s[:49]
		if strings.Contains(s[:48], "-") {
			s = s[:strings.LastIndexByte(s, '-')]
		} else {
			s = s[:48]
		}
	}
	return strings.TrimSuffix(s, "-")
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

func agentCmd(a string) string {
	switch a {
	case "claude":
		if c := os.Getenv("WEAVER_CLAUDE"); c != "" {
			return c
		}
		return "claude"
	case "codex":
		if c := os.Getenv("WEAVER_CODEX"); c != "" {
			return c
		}
		return "codex"
	}
	return ""
}

func agentInstalled(a string) bool {
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
	if p, err := firstLine(f + ".pid"); err == nil {
		if n, ok := atoi(p); ok && syscall.Kill(n, 0) == nil {
			return
		}
	}
	if rc, err := firstLine(f + ".rc"); err == nil && rc == "0" {
		if st, err := os.Stat(f + ".rc"); err == nil && time.Since(st.ModTime()) < time.Minute {
			return
		}
	}
	os.Remove(f + ".rc")
	os.Remove(f + ".pid")
	rm := preferredRemote(repo)
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
	if s := e.formSock(sf); exists(s) {
		fzfPost(s, "transform-header("+Quote(e.Self)+" _form header "+Quote(sf)+" {q})")
	}
}

// formSock is form sf's fzf socket (in SockDir: macOS caps a socket's path
// at about 104 bytes).
func (e *Env) formSock(sf string) string {
	return e.SockDir + "/form." + sf[strings.LastIndexByte(sf, '.')+1:] + ".sock"
}

type row struct{ l, plain, col, key string }

// FormHeader is the form's header for form file sf and the task typed so
// far, with err on top: right-aligned labels under the prompt's, then the
// key for each.
func (e *Env) FormHeader(sf, task, errs string) string {
	fm := loadForm(sf)
	var b strings.Builder
	if errs != "" {
		b.WriteString("        " + CRed + errs + COff + "\n")
	}
	var rs []row
	add := func(l, p, c, k string) { rs = append(rs, row{l, p, c, k}) }
	if first, err := firstLine(sf + ".task"); err == nil {
		bs, _ := os.ReadFile(sf + ".task")
		n := strings.Count(string(bs), "\n")
		if len(bs) > 0 && bs[len(bs)-1] != '\n' {
			n++
		}
		if lines := n - 1; first == task && lines > 0 {
			t := "+" + strconv.Itoa(lines) + " more lines"
			add("", t, CGrey+t+COff, "^e")
		}
	}
	if fm.item != "" {
		it, _ := loadItem(sf)
		title := it.Title
		if rlen(title) > 56 {
			title = runeSlice(title, 55) + "…"
		}
		add("from", it.Label()+"  "+title, CBold+it.Label()+COff+"  "+title, "")
		if fm.mode == "review" {
			add("mode", "review  read-only", "review  "+CGrey+"read-only"+COff, "alt-r")
		} else {
			add("mode", "work", "work", "alt-r")
		}
	}
	if fm.repo != "" {
		t := formPath(fm.repo)
		add("repo", filepath.Base(fm.repo)+"  "+t, CBold+filepath.Base(fm.repo)+COff+"  "+CGrey+t+COff, "^r")
	} else {
		add("repo", "not in a git repository", CGrey+"not in a git repository"+COff, "^r")
	}
	if fm.where == "here" {
		cur, _ := git(fm.here, "symbolic-ref", "--short", "-q", "HEAD")
		w := "this checkout"
		if cur != "" {
			w += " · " + cur
		}
		if fm.here == fm.repo {
			add("where", w, w, "tab")
		} else {
			t := formPath(fm.here)
			add("where", w+"  "+t, w+"  "+CGrey+t+COff, "tab")
		}
	} else {
		add("where", "new worktree", "new worktree", "tab")
		br := fm.branch
		if br == "" {
			br = Slugify(task)
		}
		var m refs
		if fm.repo != "" {
			m = refFacts(fm.repo, br)
		}
		switch {
		case br == "":
			add("branch", "from the task", CGrey+"from the task"+COff, "alt-b")
			e.baseRow(fm, m, add)
		case fm.repo != "" && (m.has("refs/heads/"+br) ||
			func() bool {
				rm := preferredRemote(fm.repo)
				return rm != "" && m.has("refs/remotes/"+rm+"/"+br)
			}()):
			add("branch", br+"  exists: continue on it", br+"  "+CGrey+"exists: continue on it"+COff, "alt-b")
		default:
			add("branch", br, br, "alt-b")
			e.baseRow(fm, m, add)
		}
	}
	if agentInstalled(fm.agent) {
		add("agent", fm.agent, fm.agent, "shift-tab")
	} else {
		add("agent", fm.agent+"  not installed", fm.agent+"  "+CGrey+"not installed"+COff, "shift-tab")
	}
	// The keys in a column of their own at the right edge: fzf's width (its
	// header's, less its border and margins).
	w, ok := envInt("FZF_COLUMNS")
	if !ok {
		if _, c, ok2 := ttySize(); ok2 {
			w = c
		} else {
			w = 96
		}
	}
	room := w - 8 - 8 - 10
	for _, r := range rs {
		pad := max(room-rlen(r.plain), 1)
		fmt.Fprintf(&b, "%s%6s%s  %s%*s %s%9s%s\n", CGrey, r.l, COff, r.col, pad, "", CDim, r.key, COff)
	}
	return b.String()
}

// baseRow is the form's base row: where the new branch starts, and how the
// fetch is going.
func (e *Env) baseRow(fm form, m refs, add func(l, p, c, k string)) {
	if fm.base != "" {
		note := ""
		switch fm.base {
		case "@":
			cur, ok := git(fm.here, "symbolic-ref", "--short", "-q", "HEAD")
			if fm.here == "" {
				cur, ok = git(fm.repo, "symbolic-ref", "--short", "-q", "HEAD")
			}
			if !ok || cur == "" {
				cur = "this checkout"
			}
			note = "the HEAD of " + cur
		case "^":
			note = "the local default branch"
		}
		p, c := fm.base, fm.base
		if note != "" {
			p += "  " + note
			c += "  " + CGrey + note + COff
		}
		add("base", p, c, "alt-B")
		return
	}
	if fm.repo == "" {
		return
	}
	v, note := "", ""
	if rm := preferredRemote(fm.repo); rm == "" {
		v, note = m.localDefault(""), "local: no remote"
		if v == "" {
			v = "the default branch"
		}
	} else {
		v = m.remoteDefault(rm)
		if v == "" {
			v = rm
		}
		f := e.fetchFile(fm.repo)
		rc, _ := firstLine(f + ".rc")
		pid, _ := firstLine(f + ".pid")
		switch {
		case rc == "0":
			note = "fetched"
		case rc != "":
			v, note = m.localDefault(rm), "local: could not fetch "+rm
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
	add("base", p, c, "alt-B")
}

const formFooter = CGrey + "⏎" + COff + " " + CDim + "START" + COff + "   " + CGrey + "^e" + COff + " " + CDim + "WRITE IT IN $EDITOR" + COff +
	"   " + CGrey + "esc" + COff + " " + CDim + "CANCEL" + COff

// newFormLabel is the new-thread form's label: a review says so.
func newFormLabel(fm form) string {
	if fm.mode == "review" {
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

var formFzf = []string{"--border", "sharp", "--border-label-pos", "2", "--no-info", "--no-separator", "--ansi", "--height", "100%", "--highlight-line"}

const pickFooter = CGrey + "⏎" + COff + " " + CDim + "PICK" + COff + "   " + CGrey + "esc" + COff + " " + CDim + "BACK" + COff

// checkThread: can a thread start from task and the form's fields? Why
// not, or "".
func checkThread(task string, fm form) string {
	if strings.TrimSpace(task) == "" {
		return "say what the agent should do"
	}
	if !agentInstalled(fm.agent) {
		return fm.agent + " is not installed"
	}
	if fm.where == "here" {
		if st, err := os.Stat(fm.here); err != nil || !st.IsDir() {
			return "no such directory: " + fm.here
		}
		return ""
	}
	if fm.repo == "" {
		return "not in a git repository: pick one (^r), or work in this checkout (tab)"
	}
	br := fm.branch
	if br == "" {
		br = Slugify(task)
	}
	switch {
	case br == "":
		return "name the branch (alt-b): the task has no letters to name it after"
	case exec.Command("git", "check-ref-format", "--branch", br).Run() != nil:
		return "not a valid branch name: " + br
	}
	switch {
	case fm.base == "" || fm.base == "@" || fm.base == "^" || strings.HasPrefix(fm.base, "pr:") || strings.HasPrefix(fm.base, "mr:"):
	case !gitOK(fm.repo, "rev-parse", "--verify", "--quiet", fm.base+"^{commit}"):
		return "no such base: " + fm.base
	}
	return ""
}

// New is `wvr new [options] [TASK]`: a thread for TASK (or, without
// one, the form's), its worktree and its window. ok false for --help.
func (e *Env) New(args []string) (int, bool) {
	var repoArg, from, branch, base, agent, where, itemArg, itemKind string
	var task []string
	nofocus, edit, review := false, false, false
	die := func(m string) (int, bool) {
		fmt.Fprintln(os.Stderr, "weaver: "+m)
		return 1, true
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--repo", "--from", "--branch", "-b", "--base", "--agent", "--pr", "--issue":
			if i+1 >= len(args) {
				return die(a + " needs a value")
			}
			v := args[i+1]
			switch a {
			case "--repo":
				repoArg = v
			case "--from":
				from = strings.TrimPrefix(v, "terminal_")
			case "--branch", "-b":
				branch = v
			case "--base":
				base = v
			case "--agent":
				agent = v
			case "--pr", "--issue":
				itemArg, itemKind = v, strings.TrimPrefix(a, "--")
			}
			i++
		case "--review":
			review = true
		case "--here":
			where = "here"
		case "--worktree":
			where = "worktree"
		case "--no-focus":
			nofocus = true
		case "--edit", "-e":
			edit = true
		case "--":
			task = append(task, args[i+1:]...)
			i = len(args)
		case "-h", "--help":
			return 0, false
		default:
			if strings.HasPrefix(a, "-") {
				return die("unknown option: " + a + " (see wvr help)")
			}
			task = append(task, a)
		}
	}
	if e.Session == "" {
		return die("not inside tmux: start it with `wvr`, then Alt N (or wvr new) there")
	}
	if agent == "" {
		agent = "claude"
	}
	if agent != "claude" && agent != "codex" {
		return die("unknown agent: " + agent + " (claude or codex)")
	}
	// Where it starts: --repo; the agent of --from's repository; else here.
	start, _ := os.Getwd()
	switch {
	case repoArg != "":
		start = repoArg
		if st, err := os.Stat(start); err != nil || !st.IsDir() {
			return die("no such directory: " + repoArg)
		}
	case from != "":
		f := ""
		if b, err := os.ReadFile(e.StateFile(from)); err == nil {
			if o, ok := readObj(b); ok {
				f = tostring(firstOf(o.get("root", nil), o.get("cwd", nil), ""))
			}
		}
		if st, err := os.Stat(f); f == "" || err != nil || !st.IsDir() {
			f = ""
			if b, err := os.ReadFile(e.StateDir + "/" + e.SKey + "_" + from + ".thread"); err == nil {
				if o, ok := readObj(b); ok {
					f = tostring(o.get("worktree", ""))
				}
			}
		}
		if st, err := os.Stat(f); f != "" && err == nil && st.IsDir() {
			start = f
		}
	}
	if where == "" {
		where = "worktree"
	}
	fm := form{branch: branch, where: where, agent: agent, base: base}
	if review {
		fm.mode = "review"
	}
	if r, c, ok := repoOf(start); ok {
		fm.repo, fm.here = r, c
	} else {
		if repoArg != "" {
			return die("not a git repository: " + repoArg)
		}
		if d, err := filepath.EvalSymlinks(start); err == nil {
			fm.here = d
		} else {
			fm.here = start
		}
		// Not in a repository: the one you used last, for a new worktree.
		if l, err := firstLine(e.StateDir + "/repos"); err == nil && l != "" {
			if st, err := os.Stat(l); err == nil && st.IsDir() {
				if r, _, ok := repoOf(l); ok {
					fm.repo = r
				}
			}
		}
	}
	t := strings.Join(task, " ")
	// A work item: its repository's checkout, its branch, and the task for
	// it, read before the form opens.
	var it *item
	repoErr := ""
	if itemArg != "" {
		x, err := parseItem(itemKind, itemArg)
		if err != nil {
			return die(err.Error())
		}
		if x.Kind != "linear" {
			if x.Repo == "" {
				x.Repo = ghRepo(start)
			}
			if x.Repo != "" && (fm.repo == "" || !strings.EqualFold(ghRepo(fm.repo), x.Repo)) {
				if c := e.checkoutFor(x.Repo, fm.repo); c != "" {
					fm.repo, fm.here = c, c
				} else {
					fm.repo = ""
					repoErr = "no checkout of " + x.Repo + " known: pick it (^r)"
				}
			}
		}
		if isTTY(2) {
			fmt.Fprintf(os.Stderr, "%sweaver: reading %s…%s", CGrey, x.Ref(), COff)
		}
		x, err = fetchItem(x, start)
		if isTTY(2) {
			fmt.Fprint(os.Stderr, "\r\033[K")
		}
		if err != nil {
			return die(err.Error())
		}
		it = &x
		if branch == "" {
			fm.branch = x.branchName()
		}
		fm.where = "worktree"
		fm.item = x.Ref()
		// Already a thread on its branch: go to it.
		if fm.repo != "" {
			if a := e.threadOnBranch(fm.repo, fm.branch); a != "" {
				e.focus(a)
				e.MarkSeen(a)
				fmt.Printf("weaver: %s already has a thread (%s): went to it\n", x.Ref(), fm.branch)
				return 0, true
			}
		}
	}
	if edit {
		f, err := os.CreateTemp("", "weaver-task.")
		if err != nil {
			return die("cannot make a temporary file")
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
		b, _ := readTrim(f.Name())
		if strings.TrimSpace(b) == "" {
			return 130, true
		}
		t = b
	}
	if t != "" {
		if repoErr != "" {
			return die(repoErr + " (or --repo DIR)")
		}
		if m := checkThread(t, fm); m != "" {
			return die(m)
		}
		if !e.threadCreate(t, fm, it, nofocus) {
			return 1, true
		}
		return 0, true
	}
	if _, err := exec.LookPath("fzf"); err != nil || !isTTY(0) || !isTTY(1) {
		return die(`what should the agent do? weaver new "<task>" (the form needs a terminal and fzf)`)
	}
	os.MkdirAll(e.StateDir, 0o755)
	tf, err := os.CreateTemp(e.StateDir, "form.")
	if err != nil {
		return die("cannot make the form's file")
	}
	sf := tf.Name()
	tf.Close()
	fm.save(sf)
	defer func() {
		os.Remove(sf)
		os.Remove(sf + ".task")
		os.Remove(sf + ".item")
	}()
	query := ""
	if it != nil {
		saveItem(sf, *it)
		query = it.defaultTask(review)
	}
	for {
		t, rc := e.newForm(sf, query, repoErr)
		if rc != 0 {
			return rc, true
		}
		query, repoErr = t, ""
		if i := strings.IndexByte(query, '\n'); i >= 0 {
			query = query[:i]
		}
		if e.threadCreate(t, loadForm(sf), it, nofocus) {
			return 0, true
		}
		// The worktree could not be made: its message is on screen.
		fmt.Fprintf(os.Stderr, "\n%sweaver: no thread started.%s enter: back to the form   esc: cancel ", CRed, COff)
		if readKey() == 0x1b {
			return 130, true
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
func (e *Env) newForm(sf, query, errs string) (string, int) {
	q := Quote(e.Self)
	qs := Quote(sf)
	hd := q + " _form header " + qs + " {q}"
	fm := loadForm(sf)
	// The fetch for a new branch's base starts now, while you type (and
	// while the header is made).
	if fm.where == "worktree" && fm.base == "" {
		go e.fetchStart(fm.repo, sf)
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
		args = append(args, "--border-label", newFormLabel(loadForm(sf)), "--prompt", "      task  ", "--ghost", "what should the agent do?",
			"--query", query, "--header", h, "--footer", formFooter, "--color", FzfColors,
			"--bind", "change:bg-cancel+bg-transform-header("+hd+")",
			"--bind", "tab:execute-silent("+q+" _form where "+qs+")+transform-header("+hd+")",
			"--bind", "btab:execute-silent("+q+" _form agent "+qs+")+transform-header("+hd+")",
			"--bind", "alt-r:execute-silent("+q+" _form mode "+qs+")+transform-header("+hd+")+transform-border-label("+q+" _form label "+qs+")",
			"--bind", "alt-b:execute("+q+" _form branch "+qs+" {q})+transform-header("+hd+")",
			"--bind", "alt-B:execute("+q+" _form base "+qs+")+transform-header("+hd+")",
			"--bind", "ctrl-r:execute("+q+" _form repo "+qs+")+transform-header("+hd+")",
			"--bind", "ctrl-e:execute("+q+" _form editor "+qs+" {q})+transform-query("+q+" _form query "+qs+" {q})+transform-header("+hd+")",
			"--bind", "enter:print-query")
		c := exec.Command("fzf", args...)
		c.Env = append(os.Environ(), "SHELL=/bin/sh")
		c.Stderr = os.Stderr
		out, err := c.Output()
		os.Remove(sock)
		rc := 0
		if x, ok := err.(*exec.ExitError); ok {
			rc = x.ExitCode()
		}
		if rc == 130 {
			return "", 130
		}
		if rc > 1 {
			fmt.Fprintln(os.Stderr, "weaver: fzf failed (the form needs fzf 0.62 or newer)")
			return "", 1
		}
		query = strings.TrimRight(string(out), "\n")
		if i := strings.IndexByte(query, '\n'); i >= 0 {
			query = query[:i]
		}
		task := query
		if first, err := firstLine(sf + ".task"); err == nil && first == query {
			if b, err := readTrim(sf + ".task"); err == nil {
				task = b
			}
		}
		if errs = checkThread(task, loadForm(sf)); errs == "" {
			return task, 0
		}
	}
}

// Form is `wvr _form OP FILE [QUERY]`: what the form's keys do, run by fzf.
func (e *Env) Form(op, sf, q string) {
	fm := loadForm(sf)
	switch op {
	case "header":
		fmt.Print(e.FormHeader(sf, q, ""))
	case "where":
		if fm.where == "here" {
			fm.where = "worktree"
		} else {
			fm.where = "here"
		}
		fm.save(sf)
		if fm.where == "worktree" && fm.base == "" {
			e.fetchStart(fm.repo, sf)
		}
	case "label":
		fmt.Print(newFormLabel(fm))
	case "mode":
		if fm.mode == "review" {
			fm.mode = ""
		} else {
			fm.mode = "review"
		}
		fm.save(sf)
	case "agent":
		if fm.agent == "claude" {
			fm.agent = "codex"
		} else {
			fm.agent = "claude"
		}
		fm.save(sf)
	case "base":
		// A base means a new worktree.
		if fm.where == "here" {
			fm.where = "worktree"
			fm.save(sf)
		}
		if fm.repo == "" {
			return
		}
		out := e.pick(e.formBases(fm), formLabel("NEW THREAD", "BASE"), "      base  ", "where the new branch starts")
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
		fm.base = d
		fm.save(sf)
		if fm.base == "" {
			e.fetchStart(fm.repo, sf)
		}
	case "branch":
		// A branch means a new worktree.
		if fm.where == "here" {
			fm.where = "worktree"
			fm.save(sf)
		}
		br := fm.branch
		if br == "" {
			br = Slugify(q)
		}
		args := append([]string{"--disabled", "--reverse"}, formFzf...)
		args = append(args, "--border-label", formLabel("NEW THREAD", "BRANCH"), "--prompt", "    branch  ", "--query", br, "--ghost", "branch name",
			"--color", FzfColors, "--header", "        "+CGrey+"names the branch and its worktree; an existing branch continues"+COff,
			"--footer", CGrey+"⏎"+COff+" "+CDim+"KEEP"+COff+"   "+CGrey+"esc"+COff+" "+CDim+"BACK"+COff, "--bind", "enter:print-query")
		c := exec.Command("fzf", args...)
		c.Env = append(os.Environ(), "SHELL=/bin/sh")
		c.Stderr = os.Stderr
		b, err := c.Output()
		if err != nil {
			return
		}
		out := strings.TrimRight(string(b), "\n")
		if out != "" && exec.Command("git", "check-ref-format", "--branch", out).Run() == nil {
			fm.branch = out
			if out == Slugify(q) {
				fm.branch = ""
			}
			fm.save(sf)
		}
	case "repo":
		out := e.pick(e.recentRepos(fm.repo), formLabel("NEW THREAD", "REPOSITORY"), "      repo  ", "a repository, or a path")
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
		if r, c, ok := repoOf(d); ok {
			fm.repo, fm.here = r, c
		} else {
			fm.repo = ""
			if p, err := filepath.EvalSymlinks(d); err == nil {
				fm.here = p
			} else {
				fm.here = d
			}
		}
		// Another repository's bases are its own.
		fm.base = ""
		fm.save(sf)
		if fm.where == "worktree" {
			e.fetchStart(fm.repo, sf)
		}
	case "editor":
		first, err := firstLine(sf + ".task")
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
		if l, err := firstLine(sf + ".task"); err == nil {
			first = l
		}
		fmt.Println(first)
	}
}

// pick runs a picker over items ("value<TAB>display" lines): fzf's output
// (the query, then the pick).
func (e *Env) pick(items, label, prompt, ghost string) string {
	args := append([]string{"--reverse"}, formFzf...)
	args = append(args, "--border-label", label, "--delimiter", "\t", "--with-nth", "2", "--print-query", "--prompt", prompt,
		"--ghost", ghost, "--color", FzfColors, "--footer", pickFooter)
	c := exec.Command("fzf", args...)
	c.Env = append(os.Environ(), "SHELL=/bin/sh")
	c.Stdin = strings.NewReader(items)
	c.Stderr = os.Stderr
	b, _ := c.Output()
	return strings.TrimRight(string(b), "\n")
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

// formBases are the bases to offer ("=" is the default: the remote's
// default branch, fetched).
func (e *Env) formBases(fm form) string {
	var b strings.Builder
	if rm := preferredRemote(fm.repo); rm != "" && remoteDefault(fm.repo, rm) != "" {
		b.WriteString("=\t" + remoteDefault(fm.repo, rm) + "  " + CGrey + "the remote default, fetched" + COff + "\n")
	} else {
		d := localDefault(fm.repo)
		if d == "" {
			d = "default"
		}
		b.WriteString("=\t" + d + "  " + CGrey + "the local default branch" + COff + "\n")
	}
	h := fm.here
	if h == "" {
		h = fm.repo
	}
	cur, _ := git(h, "symbolic-ref", "--short", "-q", "HEAD")
	if cur == "" {
		cur = "this checkout"
	}
	b.WriteString("@\t@  " + CGrey + "the HEAD of " + cur + COff + "\n")
	out, _ := exec.Command("git", "-C", fm.repo, "for-each-ref", "--format=%(refname:short)", "--sort=-committerdate", "refs/heads", "refs/remotes").Output()
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
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		o, ok := readObj(b)
		if !ok {
			continue
		}
		d := tostring(firstOf(o.get("repo", nil), o.get("root", nil), o.get("cwd", nil), ""))
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			if r, _, ok := repoOf(d); ok {
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
