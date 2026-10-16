package wvr

import (
	"crypto/rand"
	"encoding/json"
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

	"golang.org/x/sys/unix"
)

// Threads: a task, its worktree and a window with nvim and the agent
// (`wvr new`); brought back when lost (`wvr resume`); closed (`weaver
// close`); told something (`wvr send`); and the session quit (`weaver
// quit`).

func (e *Env) threadsNew() string { return e.StateDir + "/new" }

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
		if l, err := firstLine(d + "/at"); err == nil {
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
// waits there at most 10 s (its nvim gives up on its agent then): one older
// than 30 s was left by panes closed before they met, and holds no number.
func (e *Env) threadNum() (int, map[int]bool) {
	used := map[int]bool{}
	fs, _ := filepath.Glob(e.StateDir + "/" + e.SKey + "_*.thread")
	ns, _ := filepath.Glob(e.threadsNew() + "/" + e.SKey + "_*/thread.json")
	for _, f := range ns {
		if s, err := os.Stat(f); err == nil && time.Since(s.ModTime()) < 30*time.Second {
			fs = append(fs, f)
		}
	}
	for _, f := range fs {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if o, ok := readObj(b); ok {
			if n, ok := num(o.get("num", nil)); ok {
				used[int(n)] = true
			}
		}
	}
	n := 1
	for used[n] {
		n++
	}
	return n, used
}

// threadTab opens a thread's window, named name, in dir: its agent first
// (`wvr _agent tok kind`), then its nvim on the agent's left (`wvr nvim
// --fresh AGENT`), told the agent's pane and bound to it before this
// returns; the agent at its width, with the focus (not gone to when
// nofocus), nvim with nvimEnv too. Its window id.
func (e *Env) threadTab(tok, kind, dir, name string, nofocus bool, nvimEnv ...string) (string, bool) {
	args := []string{"new-window"}
	if nofocus {
		args = append(args, "-d")
	}
	args = append(args, "-P", "-F", "#{window_id} #{pane_id} #{window_width}", "-t", "="+e.Session+":", "-n", name, "-c", dir,
		"-e", "WEAVER_TMUX_SESSION="+e.Session, e.Self, "_agent", tok, kind)
	out, err := e.Tmux(args...)
	f := strings.Fields(string(out))
	if err != nil || len(f) < 3 {
		return "", false
	}
	tab, agent := strings.TrimPrefix(f[0], "@"), strings.TrimPrefix(f[1], "%")
	total, ok := atoi(f[2])
	if !ok {
		total = 200
	}
	nvCols := max(total-agentCols(total)-1, 1)
	sw := []string{"split-window", "-d", "-h", "-b", "-P", "-F", "#{pane_id} #{pane_pid}", "-t", "%" + agent,
		"-l", strconv.Itoa(nvCols), "-c", dir, "-e", "WEAVER_TMUX_SESSION=" + e.Session}
	for _, v := range nvimEnv {
		sw = append(sw, "-e", v)
	}
	out, err = e.Tmux(append(sw, e.Self, "nvim", "--fresh", agent)...)
	f = strings.Fields(string(out))
	if err != nil || len(f) < 2 {
		e.Tmux("kill-window", "-t", "@"+tab)
		return "", false
	}
	nv := strings.TrimPrefix(f[0], "%")
	e.bindNvim(agent, nv, f[1])
	e.Tmux("select-pane", "-t", "%"+nv, "-T", "nvim: "+filepath.Base(dir))
	return tab, true
}

// bindNvim records nvim pane nv (process pid) as agent's:
// <skey>_<agent>.nvim. (`wvr nvim` keeps this pid when it becomes nvim.)
func (e *Env) bindNvim(agent, nv, pid string) {
	os.MkdirAll(e.StateDir, 0o755)
	nf := e.StateDir + "/" + e.SKey + "_" + agent + ".nvim"
	tmp := nf + "." + strconv.Itoa(os.Getpid())
	if os.WriteFile(tmp, []byte(nv+"\n"+pid+"\n"), 0o644) == nil {
		os.Rename(tmp, nf)
	}
}

// threadPlace: in the weaver session a thread's window index is its number
// (home is 0), when free.
func (e *Env) threadPlace(tab string, n int) {
	if e.WeaverSession() {
		e.Tmux("move-window", "-d", "-s", "@"+tab, "-t", "="+e.Session+":"+strconv.Itoa(n))
	}
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

// fetchWait waits for repo's fetch (started if need be), at most t seconds:
// "0", git's failure status, "none" (no remote) or "timeout".
func (e *Env) fetchWait(repo string, t int) string {
	e.fetchStart(repo, "")
	f := e.fetchFile(repo)
	said := false
	pid := ""
	for i := 0; i < t*10; i++ {
		if exists(f + ".rc") {
			break
		}
		pid, _ = firstLine(f + ".pid")
		if n, ok := atoi(pid); !ok || syscall.Kill(n, 0) != nil {
			break
		}
		if !said && i >= 3 {
			fmt.Fprintf(os.Stderr, "%sweaver: fetching %s…%s\n", CDim, preferredRemote(repo), COff)
			said = true
		}
		time.Sleep(100 * time.Millisecond)
	}
	rc, _ := firstLine(f + ".rc")
	if rc == "" {
		rc = "timeout"
		if n, ok := atoi(pid); ok {
			syscall.Kill(-n, syscall.SIGTERM)
		}
		os.Remove(f + ".pid")
	}
	return rc
}

// threadBase is the base for a new branch of repo (empty: Worktrunk's
// default, or git's HEAD), and why it is not the fetched remote default
// when it is not; fm's base, when set, as it is.
func (e *Env) threadBase(repo string, fm form) (string, string) {
	if fm.base != "" {
		return fm.base, ""
	}
	t := 10
	if n, ok := atoi(os.Getenv("WEAVER_FETCH_TIMEOUT")); ok {
		t = n
	}
	ld := localDefault(repo)
	from := "the local default branch"
	if ld != "" {
		from = "the local " + ld
	}
	rm := preferredRemote(repo)
	if rm == "" {
		return ld, "no remote: starts from " + from
	}
	switch rc := e.fetchWait(repo, t); rc {
	case "0":
		if d := remoteDefault(repo, rm); d != "" {
			return d, ""
		}
		return ld, rm + " has no default branch: starts from " + from
	case "timeout":
		return ld, rm + " took over " + strconv.Itoa(t) + "s to fetch: starts from " + from
	default:
		return ld, "could not fetch " + rm + " (offline?): starts from " + from
	}
}

// threadWorktree is the worktree for branch of repo, made unless it exists
// (from base if given; a branch only on the remote is checked out to track
// it), working from checkout from (where @ is): with Worktrunk when it is
// installed, else with git at Worktrunk's default path. Its directory,
// whether this made it, and the base it was made from.
func threadWorktree(repo, branch, base, from, note string) (string, bool, string, bool) {
	create := !gitOK(repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if create {
		if rm := preferredRemote(repo); rm != "" && gitOK(repo, "show-ref", "--verify", "--quiet", "refs/remotes/"+rm+"/"+branch) {
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
		base, _ = git(from, "rev-parse", "--verify", "--quiet", "HEAD")
	case "^":
		base = localDefault(repo)
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

// threadCreate starts the thread for task and the form's fields: its
// worktree (unless here), then its window. Prints where it went.
func (e *Env) threadCreate(task string, fm form, it *item, nofocus bool) bool {
	var dir, name, branch, base string
	created := false
	if fm.where == "here" {
		dir = fm.here
		name = Slugify(task)
		if name == "" {
			name = "thread-" + time.Now().Format("150405")
		}
		branch, _ = git(dir, "symbolic-ref", "--short", "-q", "HEAD")
	} else {
		branch = fm.branch
		if branch == "" {
			branch = Slugify(task)
		}
		// The checkout of the repository that @ means: the one you are in
		// when it belongs to it, else the main one.
		from := fm.repo
		if fm.here != "" && fm.here != fm.repo {
			if st, err := os.Stat(fm.here); err == nil && st.IsDir() {
				if r, c, ok := repoOf(fm.here); ok && r == fm.repo {
					from = c
				}
			}
		}
		// A pull request from a fork: its head, fetched as a branch here.
		if it != nil && it.Kind == "pr" && it.Fork && !gitOK(fm.repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch) {
			if rm := preferredRemote(fm.repo); rm != "" {
				fmt.Fprintf(os.Stderr, "%sweaver: fetching %s's head%s\n", CGrey, it.Label(), COff)
				c := exec.Command("git", "-C", fm.repo, "fetch", "--quiet", rm, "pull/"+strconv.Itoa(it.Num)+"/head:"+branch)
				c.Stderr = os.Stderr
				if c.Run() != nil {
					return false
				}
			}
		}
		// A branch that is not here yet: fetch first (it may be on the
		// remote), and find where it would start.
		note := ""
		if !gitOK(fm.repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch) {
			base, note = e.threadBase(fm.repo, fm)
		}
		var ok bool
		if dir, created, base, ok = threadWorktree(fm.repo, branch, base, from, note); !ok {
			return false
		}
		name = branch
	}
	// A pull request's thread reviews against what it merges into.
	prBase := ""
	if it != nil && it.Kind == "pr" && it.Base != "" {
		prBase = it.Base
		if rm := preferredRemote(fm.repo); rm != "" {
			prBase = rm + "/" + it.Base
		}
		if branch != "" {
			exec.Command("git", "-C", fm.repo, "config", "branch."+branch+".weaverbase", prBase).Run()
		}
	}
	prompt := task
	review := fm.mode == "review"
	if it != nil {
		prompt = it.prompt(task, review, prBase)
	}
	tok := token()
	d := e.threadsNew() + "/" + e.SKey + "_" + tok
	if os.MkdirAll(d, 0o755) != nil {
		fmt.Fprintln(os.Stderr, "weaver: cannot create "+d)
		return false
	}
	os.WriteFile(d+"/task", []byte(prompt), 0o644)
	// Its stable number: the lowest free one (two at once must not share).
	unlock := e.numLock()
	n, _ := e.threadNum()
	// The command its agent runs, from here (the pane will not have this
	// environment).
	// A review is read-only: Claude Code in plan mode, Codex sandboxed.
	cmd := agentCmd(fm.agent)
	if review {
		if fm.agent == "codex" {
			cmd += " --sandbox read-only"
		} else {
			cmd += " --permission-mode plan"
		}
	}
	rec := Thread{Num: n, Cmd: cmd, Name: name, Task: task, Repo: fm.repo, Branch: branch, Worktree: dir,
		Here: fm.where == "here", WorktreeCreated: created, Agent: fm.agent, Created: time.Now().Unix(), Base: base}
	if it != nil {
		rec.Item = &ThreadItem{Kind: it.Kind, Ref: it.Ref(), URL: it.URL, Title: it.Title}
	}
	if review {
		rec.Mode = "review"
	}
	if writeRecord(d+"/thread.json", rec) != nil {
		unlock()
		fmt.Fprintln(os.Stderr, "weaver: cannot write the thread record")
		return false
	}
	unlock()
	// In the weaver session its window has its label from the start, not
	// once its agent first reports.
	wname := name
	if e.WeaverSession() {
		wname = threadLabel(n, task, dir)
	}
	// A review of a pull request opens its nvim on the review.
	var nvimEnv []string
	if review && it != nil && it.Kind == "pr" {
		nvimEnv = append(nvimEnv, "WEAVER_OPEN_REVIEW=branch")
	}
	tab, ok := e.threadTab(tok, fm.agent, dir, wname, nofocus, nvimEnv...)
	if !ok {
		os.RemoveAll(d)
		if created {
			fmt.Fprintf(os.Stderr, "weaver: tmux could not open the tab for %s (its new worktree stays: %s)\n", name, dir)
		} else {
			fmt.Fprintf(os.Stderr, "weaver: tmux could not open the tab for %s\n", name)
		}
		return false
	}
	e.threadPlace(tab, n)
	e.rememberRepo(fm.repo)
	fmt.Printf("weaver: thread %s in %s (tab %s)\n", name, tildify(dir), tab)
	return true
}

// AgentPane is `wvr _agent TOKEN AGENT`, the agent's pane of a new
// thread. First run: record the thread for this pane, tell the nvim beside
// it where the agent is, and start the agent with the task. A lost thread
// brought back: its records for this pane, then its conversation, resumed.
// Run again: resume the agent's conversation, never send the task twice.
func (e *Env) AgentPane(tok, kind string) int {
	if kind == "" {
		kind = "claude"
	}
	if kind != "claude" && kind != "codex" {
		fmt.Fprintln(os.Stderr, "weaver: unknown agent: "+kind)
		return 1
	}
	pane := e.MyPane
	d := e.threadsNew() + "/" + e.SKey + "_" + tok
	cmd := agentCmd(kind)
	rec := func(f string) oobj {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil
		}
		o, _ := parseOobj(b)
		return o
	}
	cmdOf := func(t Thread) {
		if t.Cmd != "" {
			cmd = t.Cmd
		}
	}
	put := func(f string, o oobj) {
		writeAtomic(f, []byte(o.String()+"\n"))
	}
	paneJSON := pane
	if !digits(pane) {
		paneJSON = jqStr(pane)
	}
	paneNum, _ := atoi(pane)
	pn := jsonInt(paneNum)
	run := func(args ...string) int {
		w := strings.Fields(cmd)
		p, err := exec.LookPath(w[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "weaver: "+w[0]+" is not installed")
			return 127
		}
		syscall.Exec(p, append(w, args...), os.Environ())
		return 1
	}
	if tok != "" && pane != "" && exists(d+"/task") {
		task, _ := os.ReadFile(d + "/task")
		th, therr := readThread(d + "/thread.json")
		cmdOf(th)
		// Claude Code starts on a session id weaver chose, named for the
		// thread: the thread knows its conversation before its first hook,
		// so it can be resumed (never --continue).
		var ident []string
		sid := ""
		if kind == "claude" {
			sid = uuid()
			if sid != "" {
				ident = append(ident, "--session-id", sid)
			}
			if th.Name != "" {
				ident = append(ident, "-n", th.Name)
			}
		}
		if therr == nil {
			th.Pane = &pn
			if sid != "" {
				th.SessionID = sid
			}
			writeRecord(e.threadFile(pane), th)
		}
		if os.Remove(d+"/task") != nil {
			fmt.Fprintln(os.Stderr, "weaver: cannot take the task from "+d)
			return 1
		}
		os.RemoveAll(d)
		t := string(task)
		// A task starting with "-" is not an option.
		if strings.HasPrefix(t, "-") {
			t = " " + t
		}
		return run(append(ident, t)...)
	}
	if tok != "" && pane != "" && exists(d+"/resume") {
		sid, _ := firstLine(d + "/resume")
		th, therr := readThread(d + "/thread.json")
		cmdOf(th)
		// A dead agent's files under this pane id (an earlier session's) go
		// first: kept as lost when they are, else forgotten.
		sf := e.StateFile(pane)
		if exists(sf) {
			pk := peek(sf)
			if pk.pid == "" || !agentAlive(pk.pid, pk.start, nil) {
				e.retire(sf, pk.pid)
			}
		}
		if therr == nil {
			th.Pane = &pn
			writeRecord(e.threadFile(pane), th)
		}
		if st := rec(d + "/state.json"); st != nil {
			put(sf, st.set("pane", paneJSON))
		}
		os.RemoveAll(d)
		if kind == "claude" {
			return run("--resume", sid)
		}
		return run("resume", sid)
	}
	// Its pane again (enter on an exited agent): the conversation it was
	// in, else the one weaver started it on.
	sid := ""
	str := func(o oobj, k string) string {
		if r, ok := o.get(k); ok {
			var s string
			json.Unmarshal(r, &s)
			return s
		}
		return ""
	}
	if pane != "" {
		sid = str(rec(e.StateFile(pane)), "session_id")
		th, _ := readThread(e.threadFile(pane))
		if sid == "" {
			sid = th.SessionID
		}
		cmdOf(th)
	}
	switch {
	case kind == "claude" && sid != "":
		return run("--resume", sid)
	case kind == "claude":
		return run("--continue")
	case sid != "":
		return run("resume", sid)
	}
	return run("resume", "--last")
}

// threadRestore brings lost thread lf back: a window like `wvr new`
// makes, in the directory its agent started in, whose agent resumes its
// conversation. It keeps its number when free; its state carries on.
func (e *Env) threadRestore(lf string, nofocus bool) (string, error) {
	tok := token()
	d := e.threadsNew() + "/" + e.SKey + "_" + tok
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", fmt.Errorf("cannot create %s", d)
	}
	// Claim it: of two restores at once, one takes the record.
	if os.Rename(lf, d+"/lost.json") != nil {
		os.RemoveAll(d)
		return "", fmt.Errorf("%s: already resumed", filepath.Base(lf))
	}
	back := func() {
		os.Rename(d+"/lost.json", lf)
		os.RemoveAll(d)
	}
	lo, err := readLost(d + "/lost.json")
	if err != nil {
		back()
		return "", fmt.Errorf("%s: unreadable: %v", filepath.Base(lf), err)
	}
	st, th := lo.AgentState(), lo.Thread
	sid := str(st.get("session_id", ""))
	kind := th.Agent
	if kind == "" {
		kind = str(st.get("agent", "claude"))
	}
	dir := clean(lo.Dir())
	nm := clean(th.Name)
	repo := clean(th.Repo)
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
	if kind != "claude" && kind != "codex" {
		kind = "claude"
	}
	if s, err := os.Stat(dir); sid == "" || err != nil || !s.IsDir() {
		back()
		t := tildify(dir)
		if t != "" {
			t += " "
		}
		return "", fmt.Errorf("%s: its directory %sis gone", nm, t)
	}
	unlock := e.numLock()
	n, used := e.threadNum()
	if th.Num > 0 && !used[th.Num] {
		n = th.Num
	}
	sto, _ := parseOobj(lo.State)
	t2 := th
	t2.Cmd, t2.Num, t2.Pane = agentCmd(kind), n, nil
	drop := map[string]bool{"pid": true, "proc_start": true, "nvim_pane": true, "end_reason": true, "waiting": true,
		"pending": true, "marker": true, "marked_root": true}
	var s2 oobj
	for _, m := range sto {
		if !drop[m.k] {
			s2 = append(s2, m)
		}
	}
	now := strconv.FormatInt(time.Now().Unix(), 10)
	s2 = s2.set("state", `"idle"`).set("ts", now).set("since", now).set("seen", "true").set("subagents", "[]").
		set("background", "0").set("background_tasks", `""`)
	if writeRecord(d+"/thread.json", t2) != nil ||
		os.WriteFile(d+"/state.json", []byte(s2.String()+"\n"), 0o644) != nil ||
		os.WriteFile(d+"/resume", []byte(sid+"\n"), 0o644) != nil {
		unlock()
		back()
		return "", fmt.Errorf("%s: cannot write its records", nm)
	}
	unlock()
	wname := nm
	if e.WeaverSession() {
		t := str(st.get("title", ""))
		if t == "" {
			t = clean(th.Task)
		}
		if t == "" {
			t = nm
		}
		wname = threadLabel(n, t, dir)
	}
	tab, ok := e.threadTab(tok, kind, dir, wname, nofocus)
	if !ok {
		back()
		return "", fmt.Errorf("%s: tmux could not open its window", nm)
	}
	os.Remove(d + "/lost.json")
	e.threadPlace(tab, n)
	return fmt.Sprintf("weaver: resumed %s in %s (tab %s)", nm, tildify(dir), tab), nil
}

// ResumeSession brings every thread this session lost back, in their
// numbers' order, leaving you where you are (a tmux session also takes
// those of the zellij session of its name).
func (e *Env) ResumeSession() {
	also := strings.TrimPrefix(e.SKey, "tmux-")
	type l struct {
		n int
		f string
	}
	var ls []l
	for _, r := range e.LostRows() {
		if r.Skey == e.SKey || r.Skey == also {
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
func (e *Env) Resume(args []string) int {
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
				fmt.Fprintln(os.Stderr, "weaver: "+usage)
				return 1
			}
			which = a
		}
	}
	rows := e.LostRows()
	if !all && which == "" {
		if len(rows) == 0 {
			fmt.Println("weaver: no lost threads")
			return 0
		}
		now := time.Now().Unix()
		for _, r := range rows {
			w := whereOf(r.Dir)
			wh := w.repo
			if w.branch != "" {
				wh += "/" + w.branch
			}
			lost, _ := strconv.ParseInt(r.Lost, 10, 64)
			fmt.Printf("%3s  %-32s %-28s %4s ago  %s\n", r.Num, r.Name, wh, age(now-lost), runeSlice(r.Sid, 8))
		}
		fmt.Println("wvr resume NAME (a number, or a session id) brings one back; --all, every one")
		return 0
	}
	var match []string
	if all {
		for _, r := range rows {
			match = append(match, r.File)
		}
	} else {
		for _, r := range rows {
			if which != r.Sid && which != r.Name && which != r.Num {
				if len(which) < 4 || !strings.HasPrefix(r.Sid, which) {
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
		fmt.Fprintf(os.Stderr, "weaver: no lost thread %s(wvr resume lists them)\n", n)
		return 1
	}
	if !all && len(match) != 1 {
		fmt.Fprintf(os.Stderr, "weaver: %s names %d lost threads: give its session id (wvr resume lists them)\n", which, len(match))
		return 1
	}
	if drop {
		if all {
			fmt.Fprintln(os.Stderr, "weaver: --forget takes one lost thread")
			return 1
		}
		f := match[0]
		nm := "the thread"
		if lo, err := readLost(f); err == nil && lo.Thread.Name != "" {
			nm = clean(lo.Thread.Name)
		}
		if !yes {
			t, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
			if err != nil {
				fmt.Fprintln(os.Stderr, "weaver: --forget needs a terminal to ask, or --yes")
				return 1
			}
			pad := cardPlace(t, 6)
			fmt.Fprint(t, cardRow(pad, "forget", CBold+nm+COff))
			fmt.Fprint(t, cardRow(pad, "", CGrey+"its conversation and worktree stay; weaver stops offering it"+COff))
			fmt.Fprint(t, "\n"+cardKeys(pad, "y", "forget", "n", "keep"))
			t.Close()
			if !readYes() {
				return 130
			}
		}
		os.Remove(f)
		return 0
	}
	if e.Session == "" {
		fmt.Fprintln(os.Stderr, "weaver: not inside a tmux session (start one with `wvr`)")
		return 1
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
	e.PushStatus(true)
	if failed > 0 {
		return 1
	}
	return 0
}

// cardPlace clears the terminal and gives the pad that sets a card about
// rows rows and 64 columns in its middle (a third of the way down), when
// fzf's place is to be taken (the dashboard ran this); else "  ".
func cardPlace(t *os.File, rows int) string {
	if os.Getenv("FZF_COLUMNS") == "" {
		return "  "
	}
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

// readYes: a key from the terminal, y or Y.
func readYes() bool {
	k := readKey()
	return k == 'y' || k == 'Y'
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// unsaved is how many listed buffers are modified in the nvim at sock (-1:
// no answer).
func unsaved(sock string) int {
	if st, err := os.Stat(sock); err != nil || st.Mode()&os.ModeSocket == 0 {
		return -1
	}
	n, ok := nvimEvalInt(sock, `len(filter(getbufinfo({"bufmodified": 1}), "v:val.listed"))`, 2*time.Second)
	if !ok {
		return -1
	}
	return n
}

// worktreeRemovable: may worktree dir be removed? Only a linked worktree,
// not locked, with nothing uncommitted or untracked, whose HEAD is merged
// into the default branch or pushed to its upstream. Why not.
func worktreeRemovable(dir string) string {
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "it is gone"
	}
	gd, ok1 := git(dir, "rev-parse", "--path-format=absolute", "--git-dir")
	cd, ok2 := git(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if !ok1 || !ok2 {
		return "it is not a git worktree"
	}
	if gd == cd {
		return "it is the main checkout"
	}
	if exists(gd + "/locked") {
		return "it is locked"
	}
	out, _ := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if n := strings.Count(string(out), "\n"); n > 0 {
		return strconv.Itoa(n) + " uncommitted file" + plural(n)
	}
	for _, ref := range []string{"origin/HEAD", "upstream/HEAD", "main", "master"} {
		if !gitOK(dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}") {
			continue
		}
		if gitOK(dir, "merge-base", "--is-ancestor", "HEAD", ref) {
			return ""
		}
	}
	if up, ok := git(dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); ok {
		ahead, _ := git(dir, "rev-list", "--count", "@{u}..HEAD")
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


// threadNvim is the nvim pane of a thread's agent before it is recorded
// as bound: the pane in the agent's window started as `wvr nvim --fresh
// AGENT` (or, before that, `wvr nvim --thread TOK`); and TOK, from the
// agent's own start command (`wvr _agent TOK`), whose record of the
// thread being opened goes with it. "" when there is none.
func threadNvim(panes []Pane, agent Pane) (string, string) {
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
		if p.Tab == agent.Tab && p.ID != agent.ID &&
			(strings.HasSuffix(p.Cmd, " nvim --fresh "+strconv.Itoa(agent.ID)) || strings.HasSuffix(p.Cmd, " nvim --thread "+tok)) {
			return strconv.Itoa(p.ID), tok
		}
	}
	return "", ""
}

// Close is `wvr close [--yes] [--remove-worktree] [PANE]`: close an
// agent (the focused one) with its nvim, and its thread's window; offers to
// remove a thread's worktree when it is clean and merged or pushed.
func (e *Env) Close(args []string) int {
	yes, rmwt, target := false, false, ""
	for _, a := range args {
		switch {
		case a == "--yes" || a == "-y":
			yes = true
		case a == "--remove-worktree":
			rmwt = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintln(os.Stderr, "weaver: usage: wvr close [--yes] [--remove-worktree] [pane-id]")
			return 1
		default:
			target = strings.TrimPrefix(strings.TrimPrefix(a, "terminal_"), "%")
		}
	}
	panes, ok := e.Panes()
	if !ok {
		fmt.Fprintln(os.Stderr, "weaver: tmux would not list the panes")
		return 1
	}
	byID := map[string]Pane{}
	for _, p := range panes {
		byID[strconv.Itoa(p.ID)] = p
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
				found, n := "", 0
				for _, p := range panes {
					id := strconv.Itoa(p.ID)
					if p.Tab == me.Tab && (exists(e.StateFile(id)) || exists(e.StateDir+"/"+e.SKey+"_"+id+".thread")) {
						found, n = id, n+1
					}
				}
				if n == 1 {
					target = found
				}
			}
		}
	}
	agent := e.agentOf(target)
	if agent == "" {
		t := target
		if t == "" {
			t = "?"
		}
		fmt.Fprintln(os.Stderr, "weaver: no agent in pane "+t)
		return 1
	}
	nv, pending := readNvimPane(e, agent), ""
	if nv == "" {
		nv, pending = threadNvim(panes, byID[agent])
	}
	th, _ := readThread(e.threadFile(agent))
	var st, pid, start, title, kind string
	if b, err := os.ReadFile(e.StateFile(agent)); err == nil {
		if o, ok := readObj(b); ok {
			st, pid, start = str(o.get("state", "")), str(o.get("pid", "")), str(o.get("proc_start", ""))
			title, kind = str(o.get("title", "")), str(o.get("agent", ""))
		}
	}
	tab := byID[agent].Tab
	// The window's other panes, which close must leave alone (and which
	// keep it open).
	others := false
	for _, p := range panes {
		id := strconv.Itoa(p.ID)
		if p.Tab == tab && id != agent && id != nv && !strings.HasSuffix(p.Cmd, " sidebar") {
			others = true
		}
	}
	name := clean(th.Name)
	if name == "" {
		name = title
	}
	if name == "" {
		k := kind
		if k == "" {
			k = "agent"
		}
		name = k + " in pane " + agent
	}
	what := CGrey + st + COff
	switch st {
	case "needs-you":
		what = CRed + "needs you" + COff
	case "done":
		what = CGreen + "done" + COff
	case "working":
		what = "working"
	case "":
		what = CGrey + "starting" + COff
	}
	n := 0
	if nv != "" {
		n = max(unsaved(e.SockDir+"/"+e.SKey+"_"+agent+".sock"), 0)
	}
	// Its worktree may go only if weaver made it and nothing would be lost.
	keep, removable, tilde := "", false, ""
	if th.WorktreeCreated && !th.Here && th.Worktree != "" {
		tilde = formPath(th.Worktree)
		var users []string
		skip := map[string]bool{agent: true, nv: true, e.MyPane: true}
		for _, p := range panes {
			id := strconv.Itoa(p.ID)
			if skip[id] || p.Exited || strings.HasSuffix(p.Cmd, " sidebar") {
				continue
			}
			if p.Cwd == th.Worktree || strings.HasPrefix(p.Cwd, th.Worktree+"/") {
				users = append(users, id)
			}
		}
		switch why := worktreeRemovable(th.Worktree); {
		case why != "":
			keep = tilde + " " + CDim + "·" + COff + CGrey + " " + why
		case len(users) > 0:
			keep = tilde + " " + CDim + "·" + COff + CGrey + " pane " + strings.Join(users, " ") + " is in it"
		default:
			removable = true
		}
	}
	var out io.Writer = os.Stdout
	var tty *os.File
	if !yes {
		t, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			fmt.Fprintln(os.Stderr, "weaver: close needs a terminal to ask, or --yes")
			return 1
		}
		tty, out = t, t
		defer t.Close()
	}
	goes := "the agent"
	if nv != "" {
		goes = "the agent and its nvim"
	}
	if !others && th.Name != "" {
		goes = "the agent"
		if nv != "" {
			goes += ", its nvim"
		}
		goes += " and the tab"
	}
	pad := "  "
	if tty != nil {
		pad = cardPlace(tty, 8)
	}
	k := kind
	if k == "" {
		k = "claude"
	}
	fmt.Fprint(out, cardRow(pad, "close", CBold+name+COff))
	fmt.Fprint(out, cardRow(pad, "", what+" "+CDim+"·"+COff+" "+CGrey+k+COff))
	fmt.Fprint(out, cardRow(pad, "goes", goes))
	if n > 0 {
		fmt.Fprint(out, cardRow(pad, "", CRed+"its nvim has "+strconv.Itoa(n)+" unsaved buffer"+plural(n)+COff))
	}
	if keep != "" {
		fmt.Fprint(out, cardRow(pad, "stays", CGrey+keep+COff))
	}
	if !yes {
		fmt.Fprint(out, "\n"+cardKeys(pad, "y", "close", "n", "keep"))
		if !readYes() {
			return 130
		}
	}
	remove := "0"
	if removable {
		if rmwt {
			remove = "1"
		} else if t, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
			br := th.Branch
			if br == "" {
				br = "?"
			}
			fmt.Fprint(t, "\n"+cardRow(pad, "remove", "the worktree "+tilde+" "+CGrey+"· branch "+br+" stays"+COff)+"\n"+cardKeys(pad, "y", "remove", "n", "keep"))
			t.Close()
			if readYes() {
				remove = "1"
			}
		}
	}
	tail := []string{agent, nv, pid, start, remove, th.Worktree, th.Repo, pending}
	// Closing the last panes of the window closes it, and any popup in it:
	// when this runs in one, finish from outside it.
	if me, ok := byID[e.MyPane]; !others && ok && me.Tab == tab {
		os.MkdirAll(e.StateDir, 0o755)
		// Out of this pane's session, and deaf to the hangup its closing
		// sends.
		signal.Ignore(syscall.SIGHUP, syscall.SIGTERM)
		c := exec.Command(e.Self, append([]string{"_close"}, tail...)...)
		if f, err := os.OpenFile(e.StateDir+"/close.log", os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644); err == nil {
			c.Stdout, c.Stderr = f, f
		}
		c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		c.Start()
		return 0
	}
	return e.CloseTail(tail)
}

// CloseTail is the closing itself (`wvr _close AGENT NVIM PID START
// REMOVE DIR REPO [TOK]`): close the agent's pane and its nvim's, wait for
// the agent to exit, forget it, and remove its worktree if asked, checking
// again first. TOK: the thread was still being opened (its nvim had not
// bound itself), and its record of that goes too.
func (e *Env) CloseTail(a []string) int {
	for len(a) < 8 {
		a = append(a, "")
	}
	agent, nv, pid, start, remove, dir, repo, tok := a[0], a[1], a[2], a[3], a[4], a[5], a[6], a[7]
	e.Tmux("kill-pane", "-t", "%"+agent)
	if nv != "" {
		e.Tmux("kill-pane", "-t", "%"+nv)
	}
	if digits(tok) {
		os.RemoveAll(e.threadsNew() + "/" + e.SKey + "_" + tok)
	}
	for i := 0; i < 50 && pid != "" && agentAlive(pid, start, nil); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if pid != "" && agentAlive(pid, start, nil) {
		fmt.Fprintf(os.Stderr, "weaver: the agent (pid %s) is still running; its worktree stays\n", pid)
		remove = "0"
	}
	sf := e.StateFile(agent)
	if exists(sf) {
		e.forget(sf, pid)
	} else {
		b := strings.TrimSuffix(sf, ".json")
		os.Remove(b + ".nvim")
		os.Remove(b + ".thread")
	}
	rc := 0
	if remove == "1" {
		t := tildify(dir)
		if why := worktreeRemovable(dir); why != "" {
			fmt.Fprintf(os.Stderr, "weaver: kept %s: %s\n", t, why)
			rc = 1
		} else {
			r := repo
			if r == "" {
				r = dir
			}
			var c *exec.Cmd
			if _, err := exec.LookPath("wt"); err == nil && os.Getenv("WEAVER_WORKTRUNK") != "0" {
				c = exec.Command("wt", "-C", r, "remove", "--no-delete-branch", "--foreground", dir)
			} else {
				c = exec.Command("git", "-C", r, "worktree", "remove", dir)
			}
			c.Stdout, c.Stderr = os.Stdout, os.Stderr
			if c.Run() != nil {
				rc = 1
			} else {
				fmt.Println("weaver: removed " + t)
			}
		}
	}
	e.PushStatus(false)
	return rc
}

// Send is `wvr send [--force] PANE < TEXT`: the text to agent PANE's
// prompt, as if you typed it (a bracketed paste, then enter), only while it
// waits for you to say more (idle, done, interrupted) unless forced.
func (e *Env) Send(args []string) int {
	want, force := "", false
	usage := "weaver: usage: wvr send [--force] PANE < TEXT"
	for _, a := range args {
		switch {
		case a == "--force":
			force = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintln(os.Stderr, usage)
			return 1
		default:
			want = strings.TrimPrefix(strings.TrimPrefix(a, "terminal_"), "%")
		}
	}
	if !digits(want) || isTTY(0) {
		fmt.Fprintln(os.Stderr, usage)
		return 1
	}
	text, _ := io.ReadAll(os.Stdin)
	if strings.TrimSpace(string(text)) == "" {
		fmt.Fprintln(os.Stderr, "weaver: nothing to send")
		return 1
	}
	rows, _ := e.AgentRows()
	st := ""
	for _, r := range rows {
		if r.Pane == want {
			st = r.State
			break
		}
	}
	if st == "" {
		fmt.Fprintln(os.Stderr, "weaver: no agent in pane "+want)
		return 1
	}
	if !force {
		switch st {
		case "idle", "done", "interrupted":
		case "needs-you":
			fmt.Fprintln(os.Stderr, "weaver: the agent waits on a dialog: answer it first (or --force)")
			return 1
		default:
			fmt.Fprintf(os.Stderr, "weaver: the agent is %s: send when it waits for you (or --force)\n", st)
			return 1
		}
	}
	args2 := []string{"load-buffer", "-b", "weaver-send", "-"}
	if !e.InTmux {
		args2 = append(append([]string{}, e.TSock...), args2...)
	}
	c := exec.Command("tmux", args2...)
	c.Stdin = strings.NewReader(string(text))
	if c.Run() != nil {
		fmt.Fprintln(os.Stderr, "weaver: tmux would not take the text")
		return 1
	}
	if _, err := e.Tmux("paste-buffer", "-p", "-d", "-b", "weaver-send", "-t", "%"+want); err != nil {
		fmt.Fprintln(os.Stderr, "weaver: tmux would not paste into pane "+want)
		return 1
	}
	time.Sleep(200 * time.Millisecond)
	e.Tmux("send-keys", "-t", "%"+want, "Enter")
	return 0
}

// Quit is `wvr quit [--yes]`: quit the session (outside one, the weaver
// session) with every agent and nvim in it, after asking. Its threads are
// kept: they come back as the weaver session starts again.
func (e *Env) Quit(args []string) int {
	yes := false
	for _, a := range args {
		switch a {
		case "--yes", "-y":
			yes = true
		default:
			fmt.Fprintln(os.Stderr, "weaver: usage: wvr quit [--yes]")
			return 1
		}
	}
	name := e.Session
	if name == "" {
		name = os.Getenv("WEAVER_SESSION")
	}
	if name == "" {
		name = "weaver"
	}
	if _, err := e.Tmux("has-session", "-t", "="+name); err != nil {
		fmt.Fprintln(os.Stderr, "weaver: no tmux session "+name+" is running")
		return 1
	}
	skey := "tmux-" + strings.ReplaceAll(name, "/", "_")
	if !yes {
		t, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			fmt.Fprintln(os.Stderr, "weaver: quit needs a terminal to ask, or --yes")
			return 1
		}
		threads, work, bufs := 0, 0, 0
		ts, _ := filepath.Glob(e.StateDir + "/" + skey + "_*.thread")
		threads = len(ts)
		// What stops: turns under way, and edits not yet saved in the
		// agents' nvims.
		js, _ := filepath.Glob(e.StateDir + "/" + skey + "_*.json")
		for _, f := range js {
			p := strings.TrimSuffix(f[strings.LastIndexByte(f, '_')+1:], ".json")
			if !digits(p) {
				continue
			}
			if l, _ := firstLine(f); strings.Contains(l, `"state":"working"`) {
				work++
			}
			if n := unsaved(e.SockDir + "/" + skey + "_" + p + ".sock"); n > 0 {
				bufs += n
			}
		}
		back := "wvr resume brings them back"
		if l, _ := firstLine(e.StateDir + "/" + skey + ".layout"); l == "weaver" {
			back = "back when you start weaver"
		}
		pad := cardPlace(t, 7)
		fmt.Fprint(t, cardRow(pad, "quit", CBold+name+COff+" "+CDim+"·"+COff+" "+CGrey+"every agent and nvim in it"+COff))
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
		if bufs > 0 {
			fmt.Fprint(t, cardRow(pad, "", CRed+"nvim has "+strconv.Itoa(bufs)+" unsaved buffer"+plural(bufs)+COff))
		}
		fmt.Fprint(t, "\n"+cardKeys(pad, "y", "quit", "n", "keep"))
		t.Close()
		if !readYes() {
			return 130
		}
	}
	if _, err := e.Tmux("kill-session", "-t", "="+name); err != nil {
		fmt.Fprintln(os.Stderr, "weaver: tmux could not quit "+name)
		return 1
	}
	return 0
}
