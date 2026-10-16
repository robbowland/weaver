package wvr

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/neovim/go-client/nvim"
	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/git"
	"github.com/robbowland/weaver/internal/store"
	"github.com/robbowland/weaver/internal/tmux"
)

// hookBg is `wvr hook --bg`: everything a hook does besides writing the
// state, after it, so the agent never waits on nvim,
// tmux, git or Worktrunk.
func (e *Env) hookBg() {
	h := &hk{}
	if json.Unmarshal([]byte(os.Getenv("WEAVER_BG")), h) != nil {
		return
	}
	os.Unsetenv("WEAVER_BG")
	h.sf = e.StateFile(h.Pane)
	// nvim first: following is what you see.
	if h.NV != "" {
		if h.EvFile != "" {
			e.nvimEditEvent(h, "edit", h.Root, h.EvFile, h.Tool)
		} else if h.Root != h.Old.Root {
			e.nvimEvent(h, "root", `,"root":`+jsonStr(h.Root), h.Root, "")
		}
		// The bar shows the state, turn, prompt and title: any change to them.
		if h.State != h.Old.State || h.Turn != h.sess().Turn || h.Prompt != h.Old.Prompt || h.Title != h.sess().Title || h.Doing != h.sess().Doing {
			e.nvimStateEvent(h)
		}
	}
	// tmux: the window of an agent that has just finished is flagged, for
	// config/tmux.conf's pane-focus-in hook to mark it seen when you look.
	if h.State != h.Old.State {
		if h.State == agent.StateDone {
			e.Tmux("set-option", "-w", "-t", h.Pane.Target(), "@weaver-unseen", "1")
		} else if h.Old.State == agent.StateDone {
			e.Tmux("set-option", "-w", "-u", "-t", h.Pane.Target(), "@weaver-unseen")
		}
	}
	if h.State != h.Old.State || h.Event == "UserPromptSubmit" || h.Event == "SessionStart" || h.Title != h.sess().Title || h.Root != h.Old.Root ||
		h.Doing != h.sess().Doing {
		// A thread's label is its title (else its task or prompt) and branch.
		if h.Event == "UserPromptSubmit" || h.Event == "SessionStart" || h.Title != h.sess().Title || h.Root != h.Old.Root {
			e.PushStatusAndLabels()
		} else {
			e.PushStatus()
		}
	}
	if h.NV != "" && h.Root != h.Old.Root && e.sameRoot(h) {
		e.renamePane(h.NV, "nvim: "+filepath.Base(h.Root))
	}
	if h.MarkDir != h.Old.MarkedRoot || h.MarkDir != "" && h.Marker != h.Old.Marker {
		if h.Old.MarkedRoot != "" && h.Old.MarkedRoot != h.MarkDir {
			wtMarker(h.Old.MarkedRoot, "")
		}
		if h.MarkDir != "" {
			wtMarker(h.MarkDir, h.Marker)
		}
	}
	if h.Event == "SessionStart" {
		e.GC()
	}
	e.hookSnapshots(h)
}

// sameRoot: is h's root still the agent's? A later hook may have moved it
// on while this one waited.
func (e *Env) sameRoot(h *hk) bool {
	l, _ := firstLine(h.sf)
	m := regexp.MustCompile(`"root":"([^"]*)"`).FindStringSubmatch(l)
	return m == nil || m[1] == h.Root
}

// renamePane titles a pane (the nvim beside an agent), bounded.
func (e *Env) renamePane(p tmux.PaneID, title string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	args := []string{"select-pane", "-t", p.Target(), "-T", title}
	if !e.InTmux {
		args = append(append([]string{}, e.TSock...), args...)
	}
	exec.CommandContext(ctx, "tmux", args...).Run()
}

// wtMarker sets or clears a Worktrunk branch marker in dir.
func wtMarker(dir, m string) {
	if _, err := lookPath("wt"); err != nil {
		return
	}
	if m != "" {
		exec.Command("wt", "-C", dir, "config", "state", "marker", "set", m).Run()
	} else {
		exec.Command("wt", "-C", dir, "config", "state", "marker", "clear").Run()
	}
}

// nvimEvent sends event ev (docs/design.md) to the agent's nvim: WeaverEvent,
// or AgentFollow / AgentChecktime on a weaver.lua that predates it, over its
// socket (msgpack-RPC; no nvim client started). Returns 0 on success, 128+
// when nvim did not answer in time, 1 when nothing listens. In a hook, a
// timeout is remembered: calls are skipped for 15s.
func (e *Env) nvimEvent(h *hk, ev, extra, r, f string) int {
	sock := e.nvimSock(h.Pane)
	if st, err := os.Stat(sock); err != nil || st.Mode()&os.ModeSocket == 0 {
		return 1
	}
	stalled := sock + ".stalled"
	if t, err := firstLine(stalled); err == nil {
		if n, ok := atoi(t); ok && h.Now-int64(n) < 15 {
			return 128
		}
	}
	j := `{"ev":"` + ev + `","seq":` + strconv.FormatInt(h.NowMs, 10) + `,"pane":` + jsonStr(string(h.Pane)) + `,"agent":"` + h.AgentKind +
		`","sid":` + jsonStr(h.Sid) + `,"turn":` + strconv.Itoa(h.Turn) + `,"state":"` + string(h.State) + `"` + extra + "}"
	q := func(s string) string { return strings.ReplaceAll(s, "'", "''") }
	compat := "0"
	switch ev {
	case "edit", "root", "focus":
		compat = "v:lua.AgentFollow('" + q(r) + "','" + q(f) + "')"
	case "checktime":
		compat = "v:lua.AgentChecktime()"
	}
	expr := "luaeval('_G.WeaverEvent ~= nil') ? v:lua.WeaverEvent('" + q(j) + "') : " + compat
	rc := nvimEval(sock, expr, 2*time.Second)
	if rc >= 128 {
		bestEffort(os.WriteFile(stalled, []byte(strconv.FormatInt(h.Now, 10)+"\n"), 0o644))
	} else if exists(stalled) {
		os.Remove(stalled)
	}
	return rc
}

// nvimEval evaluates expr in the nvim at sock: 0, 1 when it cannot be
// reached, 142 when it does not answer within d.
func nvimEval(sock, expr string, d time.Duration) int {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	v, err := nvim.Dial(sock, nvim.DialContext(ctx), nvim.DialLogf(func(string, ...any) {}))
	if err != nil {
		if ctx.Err() != nil {
			return 142
		}
		return 1
	}
	done := make(chan error, 1)
	go func() {
		var r any
		done <- v.Eval(expr, &r)
	}()
	select {
	case err := <-done:
		v.Close()
		if err != nil {
			return 1
		}
		return 0
	case <-ctx.Done():
		v.Close()
		return 142
	}
}

// nvimEvalInt is expr's value in the nvim at sock, as a number.
func nvimEvalInt(sock, expr string, d time.Duration) (int, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	v, err := nvim.Dial(sock, nvim.DialContext(ctx), nvim.DialLogf(func(string, ...any) {}))
	if err != nil {
		return 0, false
	}
	defer v.Close()
	done := make(chan int, 1)
	go func() {
		var r int
		if v.Eval(expr, &r) != nil {
			r = -1
		}
		done <- r
	}()
	select {
	case r := <-done:
		return r, r >= 0
	case <-ctx.Done():
		return 0, false
	}
}

// nvimEditEvent: an edit (or a focus) at file f in root r.
func (e *Env) nvimEditEvent(h *hk, ev, r, f, tool string) {
	x := `,"root":` + jsonStr(r) + `,"file":` + jsonStr(f) + `,"tool":` + jsonStr(tool)
	if h.Aid != "" {
		x += `,"agent_id":` + jsonStr(h.Aid)
	}
	e.nvimEvent(h, ev, x, r, f)
}

func (e *Env) nvimStateEvent(h *hk) {
	e.nvimEvent(h, "state", `,"prompt":`+jsonStr(h.Prompt)+`,"waiting":`+jsonStr(h.Waiting)+`,"title":`+jsonStr(h.Title)+
		`,"doing":`+jsonStr(h.Doing), "", "")
}

// PushStatus brings what shows the session's agents up to date: agents
// done that a client looks at count as seen, the counts go to the status
// file and tmux's status line, and every dashboard reloads.
func (e *Env) PushStatus() { e.pushStatus(false) }

// PushStatusAndLabels is PushStatus, and in the weaver session the threads'
// windows labelled again (a title, task or root changed).
func (e *Env) PushStatusAndLabels() { e.pushStatus(true) }

func (e *Env) pushStatus(labels bool) {
	clients := " "
	if out, err := e.Tmux("list-clients", "-F", "#{pane_id}"); err == nil {
		clients = " " + strings.ReplaceAll(strings.ReplaceAll(strings.TrimRight(string(out), "\n"), "%", ""), "\n", " ") + " "
	}
	rows, _ := e.AgentRows()
	var need, done, work int
	var seenNow []tmux.PaneID
	for _, r := range rows {
		if r.Pane == "" {
			continue
		}
		switch r.State {
		case agent.StateNeedsYou:
			need++
		case agent.StateWorking, agent.StateStarting:
			work++
		case agent.StateDone:
			if r.Seen {
				continue
			}
			if strings.Contains(clients, " "+string(r.Pane)+" ") {
				seenNow = append(seenNow, r.Pane)
			} else {
				done++
			}
		case agent.StateIdle, agent.StateInterrupted, agent.StateEnded:
			// Not counted.
		}
	}
	for _, p := range seenNow {
		e.MarkSeen(p)
	}
	os.MkdirAll(e.StateDir, 0o755)
	sf := e.StateDir + "/" + e.SKey + ".status"
	old := ""
	if l, err := firstLine(sf); err == nil {
		if f := strings.Fields(l); len(f) >= 3 {
			old = strings.Join(f[:3], " ")
		}
	}
	now := strconv.Itoa(need) + " " + strconv.Itoa(done) + " " + strconv.Itoa(work)
	tmp := sf + "." + strconv.Itoa(os.Getpid())
	if os.WriteFile(tmp, []byte(now+"\n"), 0o644) == nil {
		os.Rename(tmp, sf)
	}
	// tmux's status line shows the session's @weaver-counts, and an option
	// set redraws every client: set them when they change.
	if labels || now != old {
		e.Tmux("set-option", "-t", "="+e.Session+":", "@weaver-counts", CountsFmt(need, done, work))
	}
	q := Quote(e.Self)
	socks, _ := filepath.Glob(e.SockDir + "/" + e.SKey + ".fzf.*.sock")
	for _, s := range socks {
		if st, err := os.Stat(s); err != nil || st.Mode()&os.ModeSocket == 0 {
			continue
		}
		if !fzfPost(s, "reload(COLOR=1 "+q+" _rows)+refresh-preview") {
			b := strings.TrimSuffix(s, ".sock")
			for _, x := range []string{s, b + ".keys", b + ".view", b + ".title", b + ".label", b + ".room", b + ".shown", b + ".tight", b + ".vp"} {
				os.Remove(x)
			}
		}
	}
	if labels && e.WeaverSession() {
		e.threadTabLabels(rows)
	}
}

// fzfPost sends actions to the fzf listening on unix socket s.
func fzfPost(s, actions string) bool {
	c := &http.Client{Timeout: time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", s)
		}}}
	r, err := c.Post("http://localhost", "text/plain", strings.NewReader(actions))
	if err != nil {
		return false
	}
	r.Body.Close()
	return true
}

// MarkSeen marks agent p seen (you looked at it): a StateDone no longer stands
// out.
func (e *Env) MarkSeen(p tmux.PaneID) {
	sf := e.StateFile(p)
	if !exists(sf) {
		return
	}
	store.UpdateState(sf, func(st *agent.Record) { st.Seen = true })
}

// threadTabLabels: in the weaver session a thread's window is its line on
// the status bar, "<num> <name> · <repo>/<branch>"; renamed only when that
// is not its name already.
func (e *Env) threadTabLabels(rows []Row) {
	panes, _ := e.Panes()
	names := map[string]bool{}
	for _, p := range panes {
		n := strings.NewReplacer("\t", " ", "\n", " ").Replace(p.TabName)
		names[string(p.Tab)+"\t"+n] = true
	}
	for _, r := range rows {
		if r.Pane == "" || r.Tnum == 0 {
			continue
		}
		l := r.Title
		if l == "" {
			l = r.Ttask
		}
		if l == "" {
			l = r.Prompt
		}
		label := threadLabel(r.Tnum, l, r.Root)
		if names[string(r.Tab)+"\t"+label] {
			continue
		}
		e.Tmux("rename-window", "-t", r.Tab.Target(), label)
	}
}

// threadLabel is a thread window's name in the weaver session: its number,
// what it is about (the first line of text, fitted), and where it works.
func threadLabel(n int, text, root string) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	w := whereOf(root)
	label := padNum(n) + " " + strings.TrimRight(fit(text, 30), " ") + " · " + w.repo
	if w.branch != "" {
		label += "/" + w.branch
	}
	return label
}

func padNum(n int) string {
	s := strconv.Itoa(n)
	if len(s) < 2 {
		s = "0" + s
	}
	return s
}

// followBashEdits: a Bash command or MCP tool that changed files (sed -i, a
// heredoc, a formatter, an MCP write) counts as an edit: in the repos the
// command names, the one it ran in and the agent's root, the files git sees
// as changed since the previous such call; the newest is followed. False
// when nothing changed.
func (e *Env) followBashEdits(h *hk) bool {
	stamp := e.paneFile(h.Pane, "bash")
	nw := stamp + "." + strconv.Itoa(os.Getpid())
	if os.WriteFile(nw, nil, 0o644) != nil {
		return false
	}
	sst, err := os.Stat(stamp)
	if err != nil {
		os.Rename(nw, stamp)
		return false
	}
	var roots []string
	seen := map[string]bool{}
	cands := append([]string{h.Cwd, h.Root}, h.Paths...)
	for _, p := range cands {
		if !strings.HasPrefix(p, "/") {
			continue
		}
		d := p
		for d != "" {
			if st, err := os.Stat(d); err == nil && st.IsDir() {
				break
			}
			d = d[:max(strings.LastIndexByte(d, '/'), 0)]
		}
		if d == "" {
			continue
		}
		r, ok := git.WorkTree(d)
		if !ok || seen[r] {
			continue
		}
		seen[r] = true
		roots = append(roots, r)
		if len(roots) >= 4 {
			break
		}
	}
	type cand struct {
		p string
		t time.Time
	}
	var cs []cand
	for _, r := range roots {
		c := exec.Command("git", "-C", r, "status", "--porcelain", "-z", "-uall")
		c.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
		out, _ := c.Output()
		es := strings.Split(string(out), "\x00")
		for i := 0; i < len(es); i++ {
			en := es[i]
			if len(en) < 4 {
				continue
			}
			xy := en[:2]
			// A rename is followed by its original path.
			if strings.ContainsAny(xy, "RC") {
				i++
			}
			if strings.Contains(xy, "D") {
				continue
			}
			p := r + "/" + en[3:]
			st, err := os.Lstat(p)
			if err != nil || !st.Mode().IsRegular() || !st.ModTime().After(sst.ModTime()) {
				continue
			}
			cs = append(cs, cand{p, st.ModTime()})
		}
	}
	os.Rename(nw, stamp)
	if len(cs) == 0 {
		return false
	}
	sort.SliceStable(cs, func(i, j int) bool {
		if !cs[i].t.Equal(cs[j].t) {
			return cs[i].t.After(cs[j].t)
		}
		return cs[i].p < cs[j].p
	})
	newest := cs[0].p
	r, ok := git.FileWorkTree(newest)
	if !ok {
		return false
	}
	if exists(h.sf) {
		store.UpdateState(h.sf, func(st *agent.Record) {
			st.Root, st.File = r, newest
			var fs []string
			for _, f := range st.Files {
				if f != newest {
					fs = append(fs, f)
				}
			}
			fs = append(fs, newest)
			if len(fs) > 200 {
				fs = fs[len(fs)-200:]
			}
			st.Files = fs
			if h.Turn > 0 && !slices.Contains(st.TurnRoots, r) {
				st.TurnRoots = append(st.TurnRoots, r)
			}
		})
	}
	// A root first touched this turn starts its chain from HEAD.
	if !strings.Contains(rs+h.TurnRoots+rs, rs+r+rs) && h.Turn > 0 && h.Sid != "" {
		e.snapRoots(h, "base", h.Turn, r, "", h.PromptID)
	}
	x := `,"file":` + jsonStr(newest)
	if h.Tool == "Bash" {
		h.logLine(e, "bash", `,"cmd":`+jsonStr(h.Cmd)+x)
	} else {
		h.logLine(e, "edit", x+`,"tool":`+jsonStr(h.Tool)+`,"root":`+jsonStr(r))
	}
	if h.NV == "" {
		return true
	}
	e.nvimEditEvent(h, "edit", r, newest, h.Tool)
	if r != h.Old.Root {
		e.renamePane(h.NV, "nvim: "+filepath.Base(r))
	}
	return true
}
