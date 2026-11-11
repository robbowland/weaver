package wvr

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/cli"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/git"
	"github.com/robbowland/weaver/internal/items"
	"github.com/robbowland/weaver/internal/jsonx"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/oneline"
	"github.com/robbowland/weaver/internal/process"
	"github.com/robbowland/weaver/internal/rex"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
	"golang.org/x/sys/unix"
)

// Threads: a task, its worktree and a window with its review and the agent
// (`wvr new`); brought back when lost (`wvr resume`); closed (`wvr
// close`); and the session quit (`wvr quit`).

func (e *Env) threadsNew() string { return e.StateDir + "/new" }

// opening is the meeting place of a thread window being opened, token
// tok's: new/<skey>_<tok>/, where its agent's pane finds what to start
// (task, or resume) and its record (docs/design.md, Threads).
func (e *Env) opening(tok string) string { return e.threadsNew() + "/" + e.SKey + "_" + tok }

// openingRecords are the thread records waiting in this session's meeting
// places: <skey>_<digits> only, so session "a" never counts "a_b"'s.
func (e *Env) openingRecords() []string {
	ms, _ := filepath.Glob(e.threadsNew() + "/" + e.SKey + "_*/thread.json")
	var out []string
	for _, m := range ms {
		if tok := strings.TrimPrefix(filepath.Base(filepath.Dir(m)), e.SKey+"_"); digits(tok) {
			out = append(out, m)
		}
	}
	return out
}

func token() string {
	var b [2]byte
	rand.Read(b[:])
	return strconv.FormatInt(time.Now().Unix(), 10) + strconv.Itoa(os.Getpid()) + strconv.Itoa(int(b[0])<<8|int(b[1]))
}

func uuid() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// numLock serialises numbering threads; a lock older than 10s was left by a
// killed process.
func (e *Env) numLock() func() {
	d := e.StateDir + "/" + e.SKey + ".num.lock"
	os.MkdirAll(e.StateDir, 0o755)
	for i := 0; i < 50; i++ {
		if os.Mkdir(d, 0o755) == nil {
			break
		}
		now := time.Now().Unix()
		t := now
		if l, err := files.FirstLine(d + "/at"); err == nil {
			if n, ok := atoi(l); ok {
				t = int64(n)
			}
		}
		if now-t > 10 {
			os.RemoveAll(d)
			continue
		}
		time.Sleep(100 * time.Millisecond)
	}
	os.WriteFile(d+"/at", []byte(strconv.FormatInt(time.Now().Unix(), 10)+"\n"), 0o644)
	return func() { os.RemoveAll(d) }
}

// threadNum is the lowest number none of this session's threads has (those
// being opened included), and the numbers taken. A thread being opened
// holds its number for 30 s at most: one older was left by a pane closed
// before its agent took its record, and holds no number.
func (e *Env) threadNum() (int, map[int]bool) {
	used := map[int]bool{}
	fs := e.SessionFiles("thread")
	for _, f := range e.openingRecords() {
		if s, err := os.Stat(f); err == nil && time.Since(s.ModTime()) < 30*time.Second {
			fs = append(fs, f)
		}
	}
	for _, f := range fs {
		if t, err := store.ReadThread(f); err == nil && t.Num > 0 {
			used[t.Num] = true
		}
	}
	n := 1
	for used[n] {
		n++
	}
	return n, used
}

// threadTab opens a thread's session, labelled name (its branch) for good:
// its first window, labelled label, in dir, its agent first (`wvr _agent
// tok`), then its review panel on the agent's left, bound to it
// before this returns; the agent at its width, with the focus (not gone
// to when nofocus). Its window id. The window is made whole behind you,
// then shown: the Rex app evens out a split made in the window it shows
// (its balancing on creation), not one it is shown made.
func (e *Env) threadTab(tok, dir, name, label string, nofocus bool) (mux.WindowID, bool) {
	tab, agentPane, total, err := e.Mux().OpenSession(name, mux.NewWindow{
		Spawn: e.agentSpawn(tok, dir), Label: label, Background: true,
	})
	if err != nil {
		return "", false
	}
	if total <= 0 {
		total = 200
	}
	if !e.splitPanel(agentPane, dir, total) {
		e.Mux().CloseSession(agentPane)
		return "", false
	}
	if !nofocus {
		e.Mux().Focus(agentPane)
		// Rex sizes a window no app has shown yet at 80 columns: its
		// widths once it is.
		e.settleWidths(agentPane)
	}
	return tab, true
}

// splitPanel opens the agent's review panel (`wvr _panel AGENT`) on the
// left of agentPane, in dir, the agent keeping its width of the total
// columns; bound to the agent before this returns.
func (e *Env) splitPanel(agentPane mux.PaneID, dir string, total int) bool {
	rp, pid, err := e.Mux().SplitLeft(agentPane, mux.NewSplit{
		Spawn: mux.Spawn{Argv: []string{e.Self, "_panel", string(agentPane)}, Dir: dir},
		Cols:  max(total-agentCols(total)-1, 1), Title: panelTitle(dir), Background: true,
	})
	if err != nil {
		return false
	}
	e.bindPanel(agentPane, rp, pid)
	return true
}

// rememberRepo keeps the repositories used for threads, most recent first,
// at most 20.
func (e *Env) rememberRepo(r string) {
	if r == "" {
		return
	}
	os.MkdirAll(e.StateDir, 0o755)
	f := e.StateDir + "/repos"
	ls := []string{r}
	if b, err := os.ReadFile(f); err == nil {
		for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
			if l != r && l != "" {
				ls = append(ls, l)
			}
		}
	}
	if len(ls) > 20 {
		ls = ls[:20]
	}
	tmp := f + "." + strconv.Itoa(os.Getpid())
	if os.WriteFile(tmp, []byte(strings.Join(ls, "\n")+"\n"), 0o644) == nil {
		os.Rename(tmp, f)
	}
}

// fetchResult is how a wait for a fetch ended.
type fetchResult int

const (
	fetchDone     fetchResult = iota // fetched
	fetchTimedOut                    // still fetching when the wait ended: stopped
	fetchFailed                      // failed (offline, no access, no remote)
)

// fetchWait waits for repo's fetch (started if need be), at most wait.
func (e *Env) fetchWait(repo string, wait time.Duration) fetchResult {
	e.fetchStart(repo, "")
	f := e.fetchFile(repo)
	said := false
	pid := ""
	start := time.Now()
	for time.Since(start) < wait {
		if files.Exists(f + ".rc") {
			break
		}
		pid, _ = files.FirstLine(f + ".pid")
		if n, ok := atoi(pid); !ok || !process.Alive(n) {
			break
		}
		if !said && time.Since(start) >= 300*time.Millisecond {
			fmt.Fprintf(os.Stderr, "%sweaver: fetching %s…%s\n", CDim, git.PreferredRemote(repo), COff)
			said = true
		}
		time.Sleep(100 * time.Millisecond)
	}
	switch rc, _ := files.FirstLine(f + ".rc"); rc {
	case "0":
		return fetchDone
	case "":
		// Its group (a pid of 1 or less names none: kill(-1) is every
		// process).
		if n, ok := atoi(pid); ok && n > 1 {
			bestEffort(syscall.Kill(-n, syscall.SIGTERM))
		}
		os.Remove(f + ".pid")
		return fetchTimedOut
	}
	return fetchFailed
}

// threadBase is the base for a new branch of repo (empty: Worktrunk's
// default, or git's HEAD), and why it is not the fetched remote default
// when it is not; fm's base, when set, as it is.
func (e *Env) threadBase(repo string, fm form) (string, string) {
	if fm.Base != "" {
		return fm.Base, ""
	}
	wait := cli.EnvSeconds("WEAVER_FETCH_TIMEOUT", 10*time.Second)
	ld := git.LocalDefault(repo)
	from := "the local default branch"
	if ld != "" {
		from = "the local " + ld
	}
	rm := git.PreferredRemote(repo)
	if rm == "" {
		return ld, "no remote: starts from " + from
	}
	switch e.fetchWait(repo, wait) {
	case fetchDone:
		if d := git.RemoteDefault(repo, rm); d != "" {
			return d, ""
		}
		return ld, rm + " has no default branch: starts from " + from
	case fetchTimedOut:
		return ld, rm + " took over " + wait.String() + " to fetch: starts from " + from
	case fetchFailed:
	}
	return ld, "could not fetch " + rm + " (offline?): starts from " + from
}

// prHead puts pull request pr's head where its thread works, branch of
// repo: fetched, with what it merges into, from the remote it is on
// (pull/N/head, a fork's too, kept at refs/weaver/pr/N); branch made there
// (tracking the pull request's own branch when it is in this repository),
// or moved up to it when it is behind. A branch with commits the head has
// not, or checked out elsewhere, stays as it is, and the note says so (as
// it does a fetch that failed). ok false: no head to work on.
func (e *Env) prHead(repo string, pr items.PullRequest, branch string) (note string, ok bool) {
	n, rm := strconv.Itoa(pr.Num), git.PreferredRemote(repo)
	ref := "refs/weaver/pr/" + n
	var head string
	if rm != "" {
		fmt.Fprintf(os.Stderr, "%sweaver: fetching %s's head%s\n", CGrey, pr.Label(), COff)
		specs := []string{"+pull/" + n + "/head:" + ref}
		if pr.Base != "" {
			specs = append(specs, "+refs/heads/"+pr.Base+":refs/remotes/"+rm+"/"+pr.Base)
		}
		if !pr.Fork && pr.Head != "" {
			specs = append(specs, "+refs/heads/"+pr.Head+":refs/remotes/"+rm+"/"+pr.Head)
		}
		if e.fetchRefs(repo, rm, specs...) {
			head, _ = git.Line(repo, "rev-parse", "--verify", "-q", ref)
		}
	}
	behind := func(why string) (string, bool) {
		if !git.HasBranch(repo, branch) {
			return "", false
		}
		return why + ": " + branch + " as it is here, which may be behind it", true
	}
	switch {
	case head == "" && rm == "":
		return behind("no remote to fetch " + pr.Label() + " from")
	case head == "":
		return behind("could not fetch " + pr.Label() + "'s head")
	case !git.HasBranch(repo, branch):
		if !git.OK(repo, "branch", "--quiet", branch, head) {
			return "", false
		}
		if !pr.Fork && git.HasRemoteBranch(repo, rm, pr.Head) {
			bestEffort(exec.Command("git", "-C", repo, "branch", "--quiet", "--set-upstream-to", rm+"/"+pr.Head, branch).Run())
		}
		return "", true
	}
	local, _ := git.Line(repo, "rev-parse", "--verify", "-q", "refs/heads/"+branch)
	switch {
	case local == head:
		return "", true
	case !git.OK(repo, "merge-base", "--is-ancestor", local, head):
		return branch + " has commits " + pr.Label() + "'s head has not: kept as it is", true
	}
	if dir := checkedOut(repo, branch); dir != "" {
		return branch + " is behind " + pr.Label() + "'s head, and checked out in " + tildify(dir) + ": pull it there", true
	}
	if !git.OK(repo, "branch", "--quiet", "--force", branch, head) {
		return "could not move " + branch + " up to " + pr.Label() + "'s head: it stays behind", true
	}
	return "", true
}

// fetchRefs fetches refspecs from repo's remote rm, as the form's fetch does
// (its own session, so ssh can never ask on this terminal), within
// WEAVER_FETCH_TIMEOUT.
func (e *Env) fetchRefs(repo, rm string, specs ...string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), cli.EnvSeconds("WEAVER_FETCH_TIMEOUT", 10*time.Second))
	defer cancel()
	c := exec.CommandContext(ctx, "git", append([]string{"-C", repo, "fetch", "--quiet", "--no-write-fetch-head", rm}, specs...)...)
	c.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return c.Run() == nil
}

// checkedOut is the checkout of repo that has branch checked out; "" for
// none.
func checkedOut(repo, branch string) string {
	out, _ := exec.Command("git", "-C", repo, "worktree", "list", "--porcelain").Output()
	dir := ""
	for _, l := range strings.Split(string(out), "\n") {
		if d, ok := strings.CutPrefix(l, "worktree "); ok {
			dir = d
		}
		if l == "branch refs/heads/"+branch {
			return dir
		}
	}
	return ""
}

// threadWorktree is the worktree for branch of repo, made unless it exists
// (from base if given; a branch only on the remote is checked out to track
// it), working from checkout from (where @ is): with Worktrunk when it is
// installed, else with git at Worktrunk's default path. Its directory,
// whether this made it, and the base it was made from.
func threadWorktree(repo, branch, base, from, note string) (string, bool, string, bool) {
	create := !git.HasBranch(repo, branch)
	if create {
		if rm := git.PreferredRemote(repo); rm != "" && git.HasRemoteBranch(repo, rm, branch) {
			create = false
		}
	}
	if !create {
		base = ""
	}
	if create && note != "" {
		fmt.Fprintf(os.Stderr, "%sweaver: %s%s\n", CDim, note, COff)
	}
	fb := ""
	if base != "" {
		fb = " from " + base
	}
	fmt.Fprintf(os.Stderr, "%sweaver: worktree for %s%s%s\n", CGrey, branch, fb, COff)
	if os.Getenv("WEAVER_WORKTRUNK") != "0" {
		if _, err := exec.LookPath("wt"); err == nil {
			args := []string{"-C", from, "switch"}
			if create {
				args = append(args, "--create")
			}
			if base != "" {
				args = append(args, "--base", base)
			}
			args = append(args, "--no-cd", "--format", "json", branch)
			c := exec.Command("wt", args...)
			c.Stderr = os.Stderr
			out, err := c.Output()
			if err != nil {
				return "", false, base, false
			}
			var r struct{ Path, Action string }
			json.Unmarshal(out, &r)
			if st, err := os.Stat(r.Path); r.Path == "" || err != nil || !st.IsDir() {
				fmt.Fprintln(os.Stderr, "weaver: Worktrunk did not say where the worktree is")
				return "", false, base, false
			}
			return r.Path, r.Action == "created", base, true
		}
	}
	// The branch's own worktree, when it has one.
	out, _ := exec.Command("git", "-C", repo, "worktree", "list", "--porcelain").Output()
	wtp := ""
	for _, l := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(l, "worktree "):
			wtp = strings.TrimPrefix(l, "worktree ")
		case l == "branch refs/heads/"+branch:
			return wtp, false, base, true
		}
	}
	// Worktrunk's shortcuts, for git: @ is the checkout's HEAD, ^ the
	// default branch.
	switch base {
	case "@":
		base, _ = git.Line(from, "rev-parse", "--verify", "--quiet", "HEAD")
	case "^":
		base = git.LocalDefault(repo)
	}
	path := filepath.Dir(repo) + "/" + filepath.Base(repo) + "." + strings.ReplaceAll(branch, "/", "-")
	var c *exec.Cmd
	if create {
		args := []string{"-C", repo, "worktree", "add", "-b", branch, path}
		if base != "" {
			args = append(args, base)
		}
		c = exec.Command("git", args...)
	} else {
		c = exec.Command("git", "-C", repo, "worktree", "add", path, branch)
	}
	c.Stdout, c.Stderr = os.Stderr, os.Stderr
	if c.Run() != nil {
		return "", false, base, false
	}
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		p = path
	}
	return p, true, base, true
}

// threadItem is work item it as its thread's record keeps it (nil for
// none).
func threadItem(it items.Item) *store.ThreadItem {
	if it == nil {
		return nil
	}
	return &store.ThreadItem{Kind: it.Kind(), Ref: it.Ref(), URL: it.Content().URL, Title: it.Content().Title}
}

// threadCreate starts the thread for task and the form's fields: its
// worktree (unless here), then its window. Prints where it went.
func (e *Env) threadCreate(task string, fm form, nofocus bool) error {
	var it items.Item
	if fm.Item != nil {
		it = fm.Item.Item
	}
	pr, isPR := it.(items.PullRequest)
	ti := threadItem(it)
	// What names its branch: its task, else (an item's, told nothing) the
	// item. Its window: its name, else that.
	about := store.Thread{Task: task, Item: ti}.About()
	title := formName(fm)
	label := cmp.Or(title, about)
	var dir, name, branch, base string
	created := false
	if fm.Where == WhereHere {
		dir = fm.Here
		name = items.Slugify(about)
		if name == "" {
			name = "thread-" + time.Now().Format("150405")
		}
		branch, _ = git.Line(dir, "symbolic-ref", "--short", "-q", "HEAD")
		// A pull request's thread works on its branch: in a checkout on
		// another, it would review (and set the review base of) the wrong one.
		if isPR && branch != pr.BranchName() {
			return fmt.Errorf("%s is on %s, not %s's branch %s: start it in a worktree of its own",
				tildify(dir), cmp.Or(branch, "a detached HEAD"), pr.Label(), pr.BranchName())
		}
	} else {
		branch = fm.Branch
		if branch == "" {
			branch = items.Slugify(about)
		}
		// The checkout of the repository that @ means: the one you are in
		// when it belongs to it, else the main one.
		from := fm.Repo
		if fm.Here != "" && fm.Here != fm.Repo {
			if st, err := os.Stat(fm.Here); err == nil && st.IsDir() {
				if r, c, ok := git.RepoOf(fm.Here); ok && r == fm.Repo {
					from = c
				}
			}
		}
		note := ""
		switch {
		case isPR:
			// A pull request's thread: on its head as it is now, or said
			// (here, and to its agent) why not.
			var ok bool
			if pr.Behind, ok = e.prHead(fm.Repo, pr, branch); !ok {
				return fmt.Errorf("could not fetch %s's head, and %s is not here: nothing to work on", pr.Label(), branch)
			}
			if pr.Behind != "" {
				fmt.Fprintf(os.Stderr, "%sweaver: %s%s\n", CDim, pr.Behind, COff)
			}
			it = pr
		case !git.HasBranch(fm.Repo, branch):
			// A branch that is not here yet: fetch first (it may be on the
			// remote), and find where it would start.
			base, note = e.threadBase(fm.Repo, fm)
		}
		var ok bool
		if dir, created, base, ok = threadWorktree(fm.Repo, branch, base, from, note); !ok {
			return cli.ErrSaid
		}
		if created {
			e.noteWorktree(dir, fm.Repo)
		}
		name = branch
	}
	// A pull request's thread reviews against what it merges into (its
	// panel's base: git.ReviewBase).
	prBase := ""
	if isPR && pr.Base != "" {
		prBase = pr.Base
		if rm := git.PreferredRemote(fm.Repo); rm != "" {
			prBase = rm + "/" + pr.Base
		}
		if branch != "" {
			exec.Command("git", "-C", fm.Repo, "config", "branch."+branch+".weaverbase", prBase).Run()
		}
	}
	// The agent's first prompt: the task, with the item it is on; with no
	// task, nothing (it waits for you).
	prompt := task
	if it != nil && strings.TrimSpace(task) != "" {
		prompt = items.Prompt(it, task, fm.Mode, prBase)
	}
	tok := token()
	d := e.opening(tok)
	if os.MkdirAll(d, 0o755) != nil {
		return errors.New("cannot create " + d)
	}
	os.WriteFile(d+"/task", []byte(prompt), 0o644)
	// Its stable number: the lowest free one (two at once must not share).
	unlock := e.numLock()
	n, _ := e.threadNum()
	// The command its agent runs, from here (the pane will not have this
	// environment); its mode holds it each time it starts (agent.Command).
	rec := store.Thread{Num: n, Cmd: agentCmd(), Name: name, Task: task, Repo: fm.Repo, Branch: branch, Worktree: dir,
		Here: fm.Where == WhereHere, WorktreeCreated: created, Created: time.Now().Unix(), Base: base, Title: title,
		Item: ti, Mode: fm.Mode}
	if store.Write(d+"/thread.json", rec) != nil {
		unlock()
		return errors.New("cannot write the thread record")
	}
	unlock()
	// Its window has its label from the start, not once its agent first
	// reports.
	tab, ok := e.threadTab(tok, dir, name, session.ThreadLabel(n, label, dir), nofocus)
	if !ok {
		os.RemoveAll(d)
		if created {
			return fmt.Errorf("Rex could not open the session for %s (its new worktree stays: %s)", name, dir)
		}
		return fmt.Errorf("Rex could not open the session for %s", name)
	}
	e.rememberRepo(fm.Repo)
	// The boards and the GITHUB view show it now, not at its agent's first
	// hook.
	e.PushStatus()
	fmt.Printf("weaver: thread %s in %s (window %s)\n", name, tildify(dir), tab)
	return nil
}

// agentSpawn is a thread's agent pane (`wvr _agent tok`, in dir),
// kept when its agent exits, as its thread lasts until you close it: what
// the agent said last (a broken install, a bad flag, a goodbye) stays to be
// read, and the board shows it exited, R to start it again, x to close the
// thread, rather than its pane going and leaving its review panel alone.
func (e *Env) agentSpawn(tok, dir string) mux.Spawn {
	return mux.Spawn{Argv: []string{e.Self, "_agent", tok}, Dir: dir,
		Keep: &mux.Exit{What: "weaver: " + agent.Claude + " exited", Hint: "on the board, R starts it again, x closes the thread"}}
}

// AgentPane is `wvr _agent TOKEN`, the agent's pane of a new
// thread. First run: record the thread for this pane, and start the agent
// with the task. A lost thread
// brought back: its records for this pane, then its conversation, resumed.
// Run again: resume the agent's conversation, never send the task twice.
func (e *Env) AgentPane(tok string) error {
	pane := e.MyPane
	d := e.opening(tok)
	cmd, mode := agentCmd(), agent.ModeWork
	cmdOf := func(t store.Thread) {
		if t.Cmd != "" {
			cmd = t.Cmd
		}
		mode = t.Mode
	}
	paneNum, _ := atoi(string(pane))
	pn := jsonx.Int(paneNum)
	run := func(args ...string) error {
		w := agent.Command(cmd, mode)
		if len(w) == 0 {
			return errors.New("no command for " + agent.Claude)
		}
		p, err := exec.LookPath(w[0])
		if err != nil {
			return &cli.Exit{Code: 127, Err: errors.New(w[0] + " is not installed")}
		}
		return fmt.Errorf("%s: %w", w[0], syscall.Exec(p, append(w, args...), os.Environ()))
	}
	if tok != "" && pane != "" && files.Exists(d+"/task") {
		task, _ := os.ReadFile(d + "/task")
		th, therr := store.ReadThread(d + "/thread.json")
		cmdOf(th)
		// Claude Code starts on a session id weaver chose, named for the
		// thread: the thread knows its conversation before its first hook,
		// so it can be resumed (never --continue).
		var ident []string
		sid := uuid()
		if sid != "" {
			ident = append(ident, "--session-id", sid)
		}
		if th.Name != "" {
			ident = append(ident, "-n", th.Name)
		}
		if therr == nil {
			th.Pane = &pn
			if sid != "" {
				th.SessionID = sid
			}
			// Unwritten, the agent runs all the same: its state file tells.
			bestEffort(store.Write(e.ThreadFile(pane), th))
		}
		if os.Remove(d+"/task") != nil {
			return errors.New("cannot take the task from " + d)
		}
		os.RemoveAll(d)
		t := string(task)
		if strings.TrimSpace(t) == "" {
			return run(ident...)
		}
		// A task starting with "-" is not an option.
		if strings.HasPrefix(t, "-") {
			t = " " + t
		}
		return run(append(ident, t)...)
	}
	if tok != "" && pane != "" && files.Exists(d+"/resume") {
		sid, _ := files.FirstLine(d + "/resume")
		th, therr := store.ReadThread(d + "/thread.json")
		cmdOf(th)
		// Files under this pane id from another life of the session (an
		// earlier server's, its pane numbers taken again) go first: kept as
		// lost when they are, else forgotten. This life's are the thread's
		// own, restarted in its pane: they stay (its review panel stays bound).
		sf := e.StateFile(pane)
		if old, err := store.ReadState(sf); err == nil && !sameLife(old.Where, e.Where) {
			e.Retire(sf, string(old.Pid))
		}
		if therr == nil {
			th.Pane = &pn
			bestEffort(store.Write(e.ThreadFile(pane), th))
		}
		if st, err := store.ReadState(d + "/state.json"); err == nil {
			st.Pane = pn
			// Without it the agent's first hook starts its state afresh.
			bestEffort(store.Write(sf, st))
		}
		os.RemoveAll(d)
		return run(resumeArgs(sid)...)
	}
	// Its pane again (enter on an exited agent): the conversation it was
	// in, else the one weaver started it on.
	sid := ""
	if pane != "" {
		st, _ := store.ReadState(e.StateFile(pane))
		th, _ := store.ReadThread(e.ThreadFile(pane))
		sid = cmp.Or(st.SessionID, th.SessionID)
		cmdOf(th)
	}
	return run(resumeArgs(sid)...)
}

// sameLife: is a state of life w (older ones have no session id) of life
// here?
func sameLife(w, here agent.Where) bool {
	return w == here || w.Session == "" && w.Server == here.Server
}

// resumeArgs are the arguments that resume conversation sid; none, a
// fresh start, when it is not known (never another conversation's guess:
// --continue, resume --last).
func resumeArgs(sid string) []string {
	if sid == "" {
		return nil
	}
	return []string{"--resume", sid}
}

// threadRestore brings lost thread lf back (threadReopen), its record
// claimed first, so of two restores at once one takes it; put back when it
// cannot be.
func (e *Env) threadRestore(lf string, nofocus bool) (string, error) {
	tok := token()
	d := e.opening(tok)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", fmt.Errorf("cannot create %s", d)
	}
	if os.Rename(lf, d+"/lost.json") != nil {
		os.RemoveAll(d)
		return "", fmt.Errorf("%s: already resumed", filepath.Base(lf))
	}
	lo, err := store.ReadLost(d + "/lost.json")
	if err != nil {
		err = fmt.Errorf("%s: unreadable: %v", filepath.Base(lf), err)
	} else {
		var msg string
		if msg, err = e.threadReopen(tok, lo, nofocus); err == nil {
			os.Remove(d + "/lost.json")
			return msg, nil
		}
	}
	os.Rename(d+"/lost.json", lf)
	os.RemoveAll(d)
	return "", err
}

// threadReopen opens thread lo in a window like `wvr new` makes (its
// records in the meeting place new/<skey>_<tok>, made by the caller), in
// the directory its agent started in, whose agent resumes its conversation.
// It keeps its number when free; its state carries on.
func (e *Env) threadReopen(tok string, lo store.Lost, nofocus bool) (string, error) {
	d := e.opening(tok)
	st, th := lo.State, lo.Thread
	sid := oneline.Line(st.SessionID)
	dir := oneline.Line(lo.Dir())
	nm := oneline.Line(th.Name)
	repo := oneline.Line(th.Repo)
	// Its worktree gone: its repository's main checkout, from where Claude
	// Code finds a session in any worktree of it.
	if s, err := os.Stat(dir); err != nil || !s.IsDir() {
		if s, err := os.Stat(repo); repo != "" && err == nil && s.IsDir() {
			dir = repo
		}
	}
	if nm == "" {
		nm = "thread"
	}
	if s, err := os.Stat(dir); err != nil || !s.IsDir() {
		t := tildify(dir)
		if t != "" {
			t += " "
		}
		return "", fmt.Errorf("%s: its directory %sis gone", nm, t)
	}
	// One running elsewhere is refused, not resumed beside it: two
	// processes would interleave one conversation.
	if pid := e.ClaudeRunning()[sid]; sid != "" && pid != "" {
		return "", fmt.Errorf("%s runs in Claude Code (pid %s) outside this session: /exit it there, then resume it", nm, pid)
	}
	unlock := e.numLock()
	n, used := e.threadNum()
	if th.Num > 0 && !used[th.Num] {
		n = th.Num
	}
	t2 := th
	t2.Cmd, t2.Num, t2.Pane = agentCmd(), n, nil
	if err := e.writeResume(d, t2, st, sid); err != nil {
		unlock()
		return "", fmt.Errorf("%s: %v", nm, err)
	}
	unlock()
	t := cmp.Or(oneline.Line(cmp.Or(th.Title, st.Title)), oneline.Line(th.About()), nm)
	tab, ok := e.threadTab(tok, dir, nm, session.ThreadLabel(n, t, dir), nofocus)
	if !ok {
		return "", fmt.Errorf("%s: Rex could not open its session", nm)
	}
	return fmt.Sprintf("weaver: resumed %s in %s (window %s)", nm, tildify(dir), tab), nil
}

// writeResume leaves thread th's records in meeting place d for its agent's
// pane (AgentPane) to take as it resumes conversation sid: the record, its
// state as a resumed agent starts it (agent.Record.Resumed) in this
// session, and the session id.
func (e *Env) writeResume(d string, th store.Thread, st agent.Record, sid string) error {
	if store.Write(d+"/thread.json", th) != nil ||
		store.Write(d+"/state.json", st.Resumed(time.Now().Unix(), e.Mux().Life())) != nil ||
		os.WriteFile(d+"/resume", []byte(sid+"\n"), 0o644) != nil {
		return errors.New("cannot write its records")
	}
	return nil
}

// ResumeSession brings every thread this session lost back, in their
// numbers' order, leaving you where you are.
func (e *Env) ResumeSession() {
	type l struct {
		n int
		f string
	}
	var ls []l
	for _, r := range e.LostRows() {
		// A thread you put on hold waits for you.
		if r.Held == "" && r.Skey == e.SKey {
			n := 999
			if v, ok := atoi(r.Num); ok {
				n = v
			}
			ls = append(ls, l{n, r.File})
		}
	}
	sort.SliceStable(ls, func(i, j int) bool { return ls[i].n < ls[j].n })
	for _, x := range ls {
		if _, err := e.threadRestore(x.f, true); err != nil {
			fmt.Fprintln(os.Stderr, "weaver: "+err.Error())
		}
	}
}

// Resume is `wvr resume [--all | --forget [--yes]] [WHICH]`.
func (e *Env) Resume(args []string) error {
	all, drop, yes, which := false, false, false, ""
	usage := "usage: wvr resume [--all | --forget [--yes]] [session-id | name | number]"
	for _, a := range args {
		switch a {
		case "--all":
			all = true
		case "--forget":
			drop = true
		case "--yes", "-y":
			yes = true
		default:
			if strings.HasPrefix(a, "-") {
				return errors.New(usage)
			}
			which = a
		}
	}
	rows := e.LostRows()
	if !all && which == "" {
		if len(rows) == 0 {
			fmt.Println("weaver: no lost threads")
			return nil
		}
		now := time.Now().Unix()
		for _, r := range rows {
			w := git.WhereOf(r.Dir)
			wh := w.Repo
			if w.Branch != "" {
				wh += "/" + w.Branch
			}
			lost, _ := strconv.ParseInt(r.Lost, 10, 64)
			held := ""
			if r.Held != "" {
				held = "  held: " + cmp.Or(r.Note, "-")
			}
			fmt.Printf("%3s  %-32s %-28s %4s ago  %s%s\n", r.Num, r.Name, wh, age(now-lost), oneline.Cut(r.Sid, 8), held)
		}
		fmt.Println("wvr resume NAME (a number, or a session id) brings one back; --all, every one")
		return nil
	}
	var match []string
	if all {
		// What the session lost, not what you put on hold.
		for _, r := range rows {
			if r.Held == "" {
				match = append(match, r.File)
			}
		}
	} else {
		for _, r := range rows {
			if which != r.Sid && which != r.Name && which != r.Num {
				if len(which) < sidPrefix || !strings.HasPrefix(r.Sid, which) {
					continue
				}
			}
			match = append(match, r.File)
		}
	}
	if len(match) == 0 {
		n := ""
		if which != "" {
			n = "named " + which + " "
		}
		return fmt.Errorf("no lost thread %s(wvr resume lists them)", n)
	}
	if !all && len(match) != 1 {
		return fmt.Errorf("%s names %d lost threads: give its session id (wvr resume lists them)", which, len(match))
	}
	if drop {
		if all {
			return errors.New("--forget takes one lost thread")
		}
		f := match[0]
		nm := "the thread"
		if lo, err := store.ReadLost(f); err == nil && lo.Thread.Name != "" {
			nm = oneline.Line(lo.Thread.Name)
		}
		if !yes {
			t, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
			if err != nil {
				return errors.New("--forget needs a terminal to ask, or --yes")
			}
			pad := cardPlace(t, 6)
			fmt.Fprint(t, cardRow(pad, "forget", CBold+nm+COff))
			fmt.Fprint(t, cardRow(pad, "", CGrey+"weaver stops offering it; its conversation stays"+COff))
			fmt.Fprint(t, cardRow(pad, "", CGrey+"its worktree goes once nothing in it is unsaved"+COff))
			fmt.Fprint(t, "\n")
			c := choose(t, pad, []choice{{"y", "forget"}, {"n", "keep"}}, 1)
			t.Close()
			if c != 0 {
				return cli.ErrCancelled
			}
		}
		os.Remove(f)
		// The boards drop its row (forgotten from a popup over one).
		if e.Session != "" {
			e.PushStatus()
		}
		return nil
	}
	// From a terminal outside it, into the weaver session.
	if err := e.toWeaverSession(); err != nil {
		return err
	}
	failed := 0
	for _, f := range match {
		msg, err := e.threadRestore(f, false)
		if err != nil {
			fmt.Fprintln(os.Stderr, "weaver: "+err.Error())
			failed++
			continue
		}
		fmt.Println(msg)
	}
	e.PushStatusAndLabels()
	if failed > 0 {
		return cli.ErrSaid
	}
	return nil
}

// cardPlace clears the terminal and gives the pad that sets a card about
// rows rows and 64 columns in its middle (a third of the way down), when
// fzf's place is to be taken (the dashboard ran this); else "  ".
func cardPlace(t *os.File, rows int) string {
	if os.Getenv("FZF_COLUMNS") == "" {
		return "  "
	}
	return placeCard(t, rows)
}

// placeCard clears terminal t and places a card of rows lines a third of
// the way down, centred: the left margin to print it at.
func placeCard(t *os.File, rows int) string {
	ws, err := unix.IoctlGetWinsize(int(t.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return "  "
	}
	top := max((int(ws.Row)-rows)/3, 0)
	// Cursor moves, not newlines: nothing scrolls into the pane's history.
	fmt.Fprintf(t, "\033[H\033[2J\033[%d;1H", top+1)
	if ws.Col > 72 {
		return strings.Repeat(" ", (int(ws.Col)-64)/2)
	}
	return "  "
}

// cardRow is a card's row: label (grey, right-aligned like the form's), text.
func cardRow(pad, label, text string) string {
	return fmt.Sprintf("%s%s%6s%s  %s\n", pad, CGrey, label, COff, text)
}

// cardKeys are the keys under a card: pairs of key and what it does.
func cardKeys(pad string, kv ...string) string {
	var ps []string
	for i := 0; i+1 < len(kv); i += 2 {
		ps = append(ps, CGrey+kv[i]+COff+" "+CDim+kv[i+1]+COff)
	}
	return pad + "        " + strings.Join(ps, "   ") + "\n"
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// editing: does nvim run in agent's review panel (tuicr's e opened a file
// there), whose unsaved edits closing it would lose? Its process, under
// the panel's: no nvim weaver could ask has a socket.
func (e *Env) editing(agent mux.PaneID) bool {
	l, _ := os.ReadFile(e.PaneFile(agent, "panel"))
	f := strings.Fields(string(l))
	if len(f) < 2 {
		return false
	}
	pid, _ := strconv.Atoi(f[1])
	return process.Runs(pid, "nvim")
}

// worktreeRemovable: may worktree dir be removed? Only a linked worktree,
// not locked, with nothing uncommitted or untracked, whose HEAD is merged
// into the default branch or pushed to its upstream. Why not.
func worktreeRemovable(dir string) string {
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "it is gone"
	}
	gd, ok1 := git.Line(dir, "rev-parse", "--path-format=absolute", "--git-dir")
	cd, ok2 := git.Line(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if !ok1 || !ok2 {
		return "it is not a git worktree"
	}
	if gd == cd {
		return "it is the main checkout"
	}
	if files.Exists(gd + "/locked") {
		return "it is locked"
	}
	// Untracked files however status is set to show them (ignored ones
	// are kept aside as it goes: removeWorktree).
	out, _ := exec.Command("git", "-C", dir, "status", "--porcelain", "--untracked-files=all").Output()
	if n := strings.Count(string(out), "\n"); n > 0 {
		return strconv.Itoa(n) + " uncommitted file" + plural(n)
	}
	for _, ref := range []string{"origin/HEAD", "upstream/HEAD", "main", "master"} {
		if !git.OK(dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}") {
			continue
		}
		if git.OK(dir, "merge-base", "--is-ancestor", "HEAD", ref) {
			return ""
		}
	}
	if up, ok := git.Line(dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); ok {
		ahead, _ := git.Line(dir, "rev-list", "--count", "@{u}..HEAD")
		if ahead == "0" {
			return ""
		}
		n, _ := atoi(ahead)
		if ahead == "" {
			ahead, n = "its", 2
		}
		return ahead + " commit" + plural(n) + " not pushed to " + up
	}
	return "its commits are neither merged nor pushed"
}

// threadPanel is the review panel of a thread's agent before it is
// recorded as bound: the pane in the agent's window started as `wvr _panel
// AGENT`; and TOK, from the
// agent's own start command (`wvr _agent TOK`), whose record of the
// thread being opened goes with it. "" when there is none.
func threadPanel(panes []mux.Pane, agent mux.Pane) (mux.PaneID, string) {
	f := strings.Fields(agent.Cmd)
	tok := ""
	for i := 0; i+1 < len(f); i++ {
		if f[i] == "_agent" {
			tok = f[i+1]
			break
		}
	}
	if !digits(tok) {
		return "", ""
	}
	for _, p := range panes {
		if p.Window == agent.Window && p.ID != agent.ID &&
			strings.HasSuffix(p.Cmd, " _panel "+string(agent.ID)) {
			return p.ID, tok
		}
	}
	return "", ""
}

// Close is `wvr close [--yes] [--remove-worktree] [PANE]`: close an
// agent (the focused one) with its review panel, and a thread's session
// with every pane in it (its helpers'); offers to remove a thread's
// worktree when it is clean and merged or pushed.
func (e *Env) Close(args []string) error {
	yes, rmwt, hold, target := false, false, false, mux.PaneID("")
	for _, a := range args {
		switch {
		case a == "--yes" || a == "-y":
			yes = true
		case a == "--hold":
			// wvr hold's: unasked and unsaid, its worktree left as it is.
			yes, hold = true, true
		case a == "--remove-worktree":
			rmwt = true
		case strings.HasPrefix(a, "-"):
			return errors.New("usage: wvr close [--yes] [--remove-worktree] [pane-id]")
		default:
			target, _ = mux.ParsePane(a)
		}
	}
	panes, ok := e.Panes()
	if !ok {
		return errors.New("Rex would not list the panes")
	}
	byID := map[mux.PaneID]mux.Pane{}
	for _, p := range panes {
		byID[p.ID] = p
	}
	if target == "" {
		// The focused pane's agent; else this pane's; else the one agent in
		// this pane's window (run from a shell beside a thread).
		target = e.focusedPane()
		if e.agentOf(target) == "" {
			target = e.MyPane
		}
		if e.agentOf(target) == "" {
			target = ""
			if me, ok := byID[e.MyPane]; ok {
				found, n := mux.PaneID(""), 0
				for _, p := range panes {
					id := p.ID
					if p.Window == me.Window && (files.Exists(e.StateFile(id)) || files.Exists(e.ThreadFile(id))) {
						found, n = id, n+1
					}
				}
				if n == 1 {
					target = found
				}
			}
		}
	}
	agentPane := e.agentOf(target)
	if agentPane == "" {
		t := string(target)
		if t == "" {
			t = "?"
		}
		return errors.New("no agent in pane " + t)
	}
	nv, pending := e.PanelPane(agentPane), ""
	if nv == "" {
		nv, pending = threadPanel(panes, byID[agentPane])
	}
	th, _ := store.ReadThread(e.ThreadFile(agentPane))
	var st agent.State
	var pid, start, title, kind string
	if s, err := store.ReadState(e.StateFile(agentPane)); err == nil {
		st, pid, start = s.State, oneline.Line(string(s.Pid)), oneline.Line(s.ProcStart)
		title, kind = oneline.Line(s.Title), oneline.Line(s.Agent)
	}
	tab, in := byID[agentPane].Window, byID[agentPane].Session
	// A thread's session goes whole, the other panes in it (its helpers')
	// with it; another agent's window keeps its other panes, which keep it
	// open.
	ownSession := th.Name != "" && in != "" && in != e.RexID
	others, rest := false, 0
	for _, p := range panes {
		if p.ID == agentPane || p.ID == nv {
			continue
		}
		if p.Window == tab {
			others = true
		}
		if ownSession && p.Session == in {
			rest++
		}
	}
	name := oneline.Line(th.Name)
	if name == "" {
		name = title
	}
	if name == "" {
		k := kind
		if k == "" {
			k = "agent"
		}
		name = k + " in pane " + string(agentPane)
	}
	// Its state and name as its row on the board says them.
	called := ""
	exited := false
	if r, _, ok := e.RowOf(agentPane); ok {
		st, called, exited = r.State, r.Called(), r.Exited
	}
	what := CGrey + string(cmp.Or(st, agent.StateStarting)) + COff
	switch {
	case exited:
		what = CGrey + "exited" + COff
	case st == agent.StateNeedsYou:
		what = CRed + "needs you" + COff
	case st == agent.StateDone:
		what = CGreen + string(agent.StateDone) + COff
	case st == agent.StateWorking:
		what = string(agent.StateWorking)
	}
	editing := nv != "" && e.editing(agentPane)
	// Its worktree may go only if weaver made it and nothing would be lost.
	keep, removable, tilde := "", false, ""
	if th.WorktreeCreated && !th.Here && th.Worktree != "" {
		tilde = formPath(th.Worktree)
		switch why, users := worktreeRemovable(th.Worktree), e.worktreeUsers(th.Worktree, agentPane, nv, e.MyPane); {
		case why != "":
			keep = tilde + " " + CDim + "·" + COff + CGrey + " " + why
		case len(users) > 0:
			keep = tilde + " " + CDim + "·" + COff + CGrey + " " + strings.Join(users, ", ") + " in it"
		default:
			removable = true
		}
	}
	var out io.Writer = os.Stdout
	if hold {
		out = io.Discard
	}
	var tty *os.File
	if !yes {
		t, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			return errors.New("close needs a terminal to ask, or --yes")
		}
		tty, out = t, t
		defer t.Close()
	}
	goes := "the agent"
	if nv != "" {
		goes = "the agent and its review"
	}
	if ownSession {
		goes = "the agent"
		if nv != "" {
			goes += ", its review"
		}
		goes += " and its session"
		if rest > 0 {
			goes += fmt.Sprintf(", with the %d other pane%s in it", rest, plural(rest))
		}
	}
	pad := "  "
	if tty != nil {
		pad = cardPlace(tty, 8)
	}
	k := kind
	if k == "" {
		k = "claude"
	}
	// Named as the board names it: its number and name, then its branch.
	head, sub := CBold+name+COff, ""
	if t := oneline.Line(called); t != "" {
		head = CBold + oneline.Cut(t, 56) + COff
		if th.Num > 0 {
			head = CDim + fmt.Sprintf("%02d", th.Num) + COff + " " + head
		}
		if name != t {
			sub = " " + CDim + "·" + COff + " " + CGrey + name + COff
		}
	}
	fmt.Fprint(out, cardRow(pad, "close", head))
	fmt.Fprint(out, cardRow(pad, "", what+" "+CDim+"·"+COff+" "+CGrey+k+COff+sub))
	fmt.Fprint(out, cardRow(pad, "goes", goes))
	if editing {
		fmt.Fprint(out, cardRow(pad, "", CRed+"nvim is open in its review: unsaved edits go"+COff))
	}
	if keep != "" {
		fmt.Fprint(out, cardRow(pad, "stays", CGrey+keep+COff))
	}
	if !yes {
		fmt.Fprint(out, "\n")
		if choose(tty, pad, []choice{{"y", "close"}, {"n", "keep"}}, 1) != 0 {
			return cli.ErrCancelled
		}
	}
	remove := "0"
	if removable && !hold {
		if rmwt {
			remove = "1"
		} else if t, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
			br := th.Branch
			if br == "" {
				br = "?"
			}
			fmt.Fprint(t, cardRow(pad, "remove", "the worktree "+tilde+" "+CGrey+"· branch "+br+" stays"+COff)+"\n")
			if choose(t, pad, []choice{{"y", "remove"}, {"n", "keep"}}, 1) == 0 {
				remove = "1"
			} else {
				// Kept on purpose: yours now, never swept.
				e.forgetWorktree(th.Worktree)
			}
			_ = t.Close()
		}
	}
	tail := []string{string(agentPane), string(nv), pid, start, remove, th.Worktree, th.Repo, pending}
	// Closing a thread's session, or the last panes of a window, closes
	// any popup in it: when this runs in one, finish from outside it.
	me, ok := byID[e.MyPane]
	if ownSession && e.RexHere == in || !ownSession && !others && ok && me.Window == tab {
		e.outside(append([]string{"_close"}, tail...)...)
		return nil
	}
	return e.CloseTail(tail)
}

// outside runs `wvr args...` in a process of its own, out of this pane's
// process group and deaf to the hangup its closing sends, in no pane
// (REX_BLOCK unset), what it says going to close.log: for work that closes
// the pane, popup or session this runs in.
func (e *Env) outside(args ...string) {
	os.MkdirAll(e.StateDir, 0o755)
	signal.Ignore(syscall.SIGHUP, syscall.SIGTERM)
	c := exec.Command(e.Self, args...)
	c.Env = append(os.Environ(), "REX_BLOCK=")
	if f, err := os.OpenFile(e.StateDir+"/close.log", os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644); err == nil {
		c.Stdout, c.Stderr = f, f
	}
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	c.Start()
}

// CloseTail is the closing itself (`wvr _close AGENT PANEL PID START
// REMOVE DIR REPO [TOK]`): close a thread's session, or another agent's
// pane and its review panel's, wait for the agent to exit, forget it, and
// remove its worktree if asked, checking again first. TOK: the thread was
// still being opened (its panel had not been bound), and its record of
// that goes too.
func (e *Env) CloseTail(a []string) error {
	for len(a) < 8 {
		a = append(a, "")
	}
	agent, _ := mux.ParsePane(a[0])
	nv, _ := mux.ParsePane(a[1])
	pid, start, remove, dir, repo, tok := a[2], a[3], a[4], a[5], a[6], a[7]
	// Closing the thread you are looking at: home, where the rest are, not
	// whichever window or session Rex would show next.
	ps, _ := e.Mux().Panes()
	ap, shown := session.PaneIn(ps, agent)
	ownSession := shown && ap.Session != e.RexID && files.Exists(e.ThreadFile(agent))
	if shown {
		if _, cur, home := e.where(); cur.ID == ap.Window && home != ap.Window || ownSession && ap.Session == e.RexHere {
			e.selectHome(home)
		}
	}
	if ownSession {
		e.Mux().CloseSession(agent)
	} else {
		e.Mux().Close(agent)
		if nv != "" {
			e.Mux().Close(nv)
		}
	}
	if digits(tok) {
		os.RemoveAll(e.opening(tok))
	}
	for i := 0; i < 50 && pid != "" && process.StillRuns(pid, start, nil); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if pid != "" && process.StillRuns(pid, start, nil) {
		fmt.Fprintf(os.Stderr, "weaver: the agent (pid %s) is still running; its worktree stays\n", pid)
		remove = "0"
	}
	sf := e.StateFile(agent)
	if files.Exists(sf) {
		e.Forget(sf, pid)
	} else {
		b := strings.TrimSuffix(sf, ".json")
		os.Remove(b + ".panel")
		os.Remove(b + ".thread")
	}
	var rc error
	if remove == "1" {
		t := tildify(dir)
		if why := worktreeRemovable(dir); why != "" {
			rc = fmt.Errorf("kept %s: %s", t, why)
		} else if users := e.worktreeUsers(dir); len(users) > 0 {
			rc = fmt.Errorf("kept %s: %s in it", t, strings.Join(users, ", "))
		} else {
			if kept, err := e.removeWorktree(dir, repo, os.Stdout); err != nil {
				fmt.Println("weaver: " + err.Error())
				rc = cli.ErrSaid
			} else {
				e.forgetWorktree(dir)
				fmt.Println("weaver: removed " + t + keptSays(kept))
			}
		}
	}
	e.PushStatus()
	return rc
}

// toWeaverSession makes the session acted on, outside one, weaver's
// ($WEAVER_SESSION, else weaver); inside one it is the one you are in. An
// error when it is not running.
func (e *Env) toWeaverSession() error {
	if e.Session == "" {
		name := cmp.Or(os.Getenv("WEAVER_SESSION"), "weaver")
		s, running, err := rex.Named(e.RexDir(), name)
		if err != nil {
			return fmt.Errorf("weaver cannot tell whether its Rex session %s runs (Rex does not answer): %w", name, err)
		}
		if !running {
			return errors.New("no Rex session " + name + " of weaver's is running (start it with `wvr`)")
		}
		e.Session, e.SKey, e.RexID = name, rex.Key(name), s.ID
		return nil
	}
	if !e.Mux().Running() {
		return errors.New("no session " + e.Session + " is running (start it with `wvr`)")
	}
	return nil
}

// Quit is `wvr quit [--yes]`: quit the session (outside one, the weaver
// session) with every agent and review in it, after asking. Its threads are
// kept: they come back as the weaver session starts again.
func (e *Env) Quit(args []string) error {
	yes := false
	for _, a := range args {
		switch a {
		case "--yes", "-y":
			yes = true
		default:
			return errors.New("usage: wvr quit [--yes]")
		}
	}
	if err := e.toWeaverSession(); err != nil {
		return err
	}
	name := e.Session
	if !yes {
		t, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			return errors.New("quit needs a terminal to ask, or --yes")
		}
		// What comes back (its threads) and what stops: turns under way,
		// and the nvims open in the agents' reviews.
		threads, work, editing := 0, 0, 0
		rows, now := e.AgentRows()
		for _, r := range rows {
			if r.Tnum > 0 {
				threads++
			}
			if r.MidTurn(now) {
				work++
			}
			if e.editing(r.Pane) {
				editing++
			}
		}
		back := "back when you start weaver"
		pad := cardPlace(t, 7)
		fmt.Fprint(t, cardRow(pad, "quit", CBold+name+COff+" "+CDim+"·"+COff+" "+CGrey+"every agent and review in it"+COff))
		if threads > 0 {
			fmt.Fprint(t, cardRow(pad, "kept", strconv.Itoa(threads)+" thread"+plural(threads)+" "+CDim+"·"+COff+" "+CGrey+back+COff))
		}
		if work > 0 {
			s := "s"
			if work != 1 {
				s = ""
			}
			fmt.Fprint(t, cardRow(pad, "", CGrey+strconv.Itoa(work)+" working: the turn"+plural(work)+" stop"+s+COff))
		}
		if editing > 0 {
			fmt.Fprint(t, cardRow(pad, "", CRed+"nvim is open in "+strconv.Itoa(editing)+" review"+plural(editing)+": unsaved edits go"+COff))
		}
		fmt.Fprint(t, "\n")
		c := choose(t, pad, []choice{{"y", "quit"}, {"n", "keep"}}, 1)
		t.Close()
		if c != 0 {
			return cli.ErrCancelled
		}
	}
	if err := e.Mux().Quit(); err != nil {
		return errors.New("Rex could not quit " + name)
	}
	return nil
}
