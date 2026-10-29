package wvr

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/cli"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/git"
	"github.com/robbowland/weaver/internal/items"
	"github.com/robbowland/weaver/internal/jsonx"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/oneline"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
	"github.com/robbowland/weaver/internal/tmux"
)

// Adopting: a conversation weaver did not start becomes a thread, kept as
// threads are (lost with its session, brought back as weaver starts). An
// agent you started by hand becomes one where it runs; a conversation that
// runs nowhere is resumed in a thread's window.

const adoptUsage = "usage: wvr adopt [pane-id | session-id]"

// Adopt is `wvr adopt [PANE | SESSION-ID]`: the agent in PANE, else the
// one in this pane, made a thread where it runs; conversation SESSION-ID
// (or its first 4+ characters) resumed as a thread; with neither, a picker
// of Claude Code's recent conversations. Outside tmux, into the weaver
// session.
func (e *Env) Adopt(args []string) error {
	if len(args) > 1 || len(args) == 1 && strings.HasPrefix(args[0], "-") {
		return errors.New(adoptUsage)
	}
	// From a terminal outside tmux, into the weaver session.
	if err := e.toWeaverSession(); err != nil {
		return err
	}
	which := firstArg(args)
	var msg string
	var err error
	picked := false
	switch p, ok := paneArg(which); {
	case ok:
		msg, err = e.adoptPane(p)
	case which != "":
		msg, err = e.adoptConversation(which)
	case e.MyPane != "" && files.Exists(e.StateFile(e.MyPane)):
		msg, err = e.adoptPane(e.MyPane)
	default:
		sid := e.pickConversation()
		if sid == "" {
			return cli.ErrCancelled
		}
		picked = true
		msg, err = e.adoptConversation(sid)
	}
	if err != nil {
		if picked {
			// The picker's popup closes as this ends: said on the status line.
			e.Mux().Say(4000, "weaver: "+err.Error())
		}
		return err
	}
	fmt.Println(msg)
	return nil
}

// paneArg is which as a pane: %N, or a number of at most three digits (a
// longer one is the start of a session id).
func paneArg(which string) (mux.PaneID, bool) {
	if strings.HasPrefix(which, "%") {
		return tmux.ParsePane(which)
	}
	if len(which) > 3 {
		return "", false
	}
	return tmux.ParsePane(which)
}

// adoptPane makes the agent in pane p, which weaver follows but did not
// start, a thread where it runs: its record (in the checkout it is in,
// which close never offers to remove), and in the weaver session a window
// of its own at its number, its nvim on its left. Nothing restarts: a turn
// under way goes on.
func (e *Env) adoptPane(p mux.PaneID) (string, error) {
	st, err := store.ReadState(e.StateFile(p))
	if err != nil {
		return "", fmt.Errorf("weaver follows no agent in pane %%%s", p)
	}
	sid := oneline.Line(st.SessionID)
	if st.State == agent.StateEnded || st.Pid != "" && !session.AgentAlive(string(st.Pid), st.ProcStart, nil) {
		return "", fmt.Errorf("its agent has ended: `wvr adopt %s` resumes its conversation as a thread", cmp.Or(sid, "SESSION-ID"))
	}
	kind, ok := agent.ParseKind(st.Agent)
	if !ok {
		return "", fmt.Errorf("weaver cannot resume a %s agent, so cannot keep it as a thread", st.Agent)
	}
	if sid == "" {
		return "", errors.New("its agent has not said which conversation it is in yet")
	}
	root := cmp.Or(oneline.Line(st.Root), oneline.Line(st.Cwd))
	repo, _, _ := git.RepoOf(root)
	branch, _ := git.Line(root, "symbolic-ref", "--short", "-q", "HEAD")
	about := cmp.Or(oneline.Line(st.Title), oneline.Line(st.Prompt))
	name := cmp.Or(items.Slugify(about), branch, "thread-"+time.Now().Format("150405"))
	pn, _ := atoi(string(p))
	pane := jsonx.Int(pn)
	// Of two adopting it at once, one numbers it.
	unlock := e.numLock()
	if th, err := store.ReadThread(e.ThreadFile(p)); err == nil {
		unlock()
		return "", fmt.Errorf("%s is thread %d already", cmp.Or(oneline.Line(th.Name), "it"), th.Num)
	}
	n, _ := e.threadNum()
	th := store.Thread{Num: n, Cmd: agentCmd(kind), Name: name, Repo: repo, Branch: branch, Worktree: root, Here: true,
		Agent: kind, Created: time.Now().Unix(), Pane: &pane, SessionID: sid}
	err = store.Write(e.ThreadFile(p), th)
	unlock()
	if err != nil {
		return "", errors.New("cannot write its thread record")
	}
	where := ""
	if e.WeaverSession() {
		if tab := e.adoptWindow(p, n, session.ThreadLabel(n, cmp.Or(about, name), root), root); tab != "" {
			where = " (window " + tab.Target() + ")"
		}
	}
	e.PushStatusAndLabels()
	return fmt.Sprintf("weaver: %s is thread %d%s", name, n, where), nil
}

// adoptWindow gives an adopted agent, pane p, its thread's window: the one
// it is in when it has it to itself (but for its nvim), else one of its own
// broken out of the window it shares (home, a view, another thread's);
// at its number, named label, with an nvim on its left in dir when it has
// none. You go with it when it is the pane you are in. Its window.
func (e *Env) adoptWindow(p mux.PaneID, n int, label, dir string) mux.WindowID {
	ap, ok := e.findPane(p)
	if !ok {
		return ""
	}
	tab := ap.Tab
	nv := e.NvimPane(p)
	own := ap.TabIndex != 0 && e.viewOfWindow(tab) == ""
	if own {
		for _, q := range e.windowPanes(tab) {
			if q != p && q != nv {
				own = false
			}
		}
	}
	if own {
		e.Mux().Label(tab, label)
		e.threadPlace(tab, n)
	} else {
		focus := p == e.MyPane
		w, err := e.Mux().BreakOut(p, label, n, focus)
		if err != nil {
			// Its number is another window's: where the multiplexer puts it.
			w, err = e.Mux().BreakOut(p, label, -1, focus)
		}
		if err != nil {
			return ""
		}
		tab = w
	}
	if nv == "" {
		total, ok := e.windowWidth(tab)
		if !ok {
			total = 200
		}
		e.splitNvim(p, dir, total)
	}
	return tab
}

// adoptConversation makes conversation which (its session id, or its
// first 4+ characters) a thread: one a session lost is resumed as lost
// threads are; one an agent here runs is adopted where it runs; one that
// runs nowhere is resumed in a thread's window, in the directory it
// started in.
func (e *Env) adoptConversation(which string) (string, error) {
	match := func(sid string) bool {
		return sid != "" && (sid == which || len(which) >= 4 && strings.HasPrefix(sid, which))
	}
	for _, r := range e.LostRows() {
		if match(r.Sid) {
			return e.threadRestore(r.File, false)
		}
	}
	for _, f := range e.SessionFiles("json") {
		if st, err := store.ReadState(f); err == nil && match(st.SessionID) && st.State != agent.StateEnded {
			return e.adoptPane(session.PaneOfFile(f))
		}
	}
	c, err := e.findConversation(which)
	if err != nil {
		return "", err
	}
	if pid := e.ClaudeRunning()[c.ID]; pid != "" {
		return "", fmt.Errorf("%s runs in Claude Code (pid %s) outside this session: /exit it there, then adopt it", cmp.Or(c.Title, c.ID), pid)
	}
	repo, _, _ := git.RepoOf(c.Start)
	name := cmp.Or(items.Slugify(c.Title), c.Branch, "thread-"+time.Now().Format("150405"))
	tok := token()
	d := e.opening(tok)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", fmt.Errorf("cannot create %s", d)
	}
	msg, err := e.threadReopen(tok, c.lost(e.SKey, name, repo, time.Now().Unix()), false)
	if err != nil {
		os.RemoveAll(d)
		return "", err
	}
	e.PushStatusAndLabels()
	return msg, nil
}

// adoptRows is how many of the newest conversations the picker offers.
const adoptRows = 80

// pickConversation picks one of Claude Code's recent conversations that is
// not a thread here already: its session id, "" for none.
func (e *Env) pickConversation() string {
	threads := map[string]bool{}
	for _, f := range e.SessionFiles("thread") {
		if t, err := store.ReadThread(f); err == nil {
			threads[t.SessionID] = true
		}
		if s, err := store.ReadState(strings.TrimSuffix(f, ".thread") + ".json"); err == nil {
			threads[s.SessionID] = true
		}
	}
	running := e.ClaudeRunning()
	now := time.Now().Unix()
	var b strings.Builder
	n := 0
	for _, f := range e.transcripts("") {
		if n == adoptRows {
			break
		}
		c, err := readConversation(f)
		if err != nil || c.Title == "" || threads[c.ID] {
			continue
		}
		n++
		w := git.WhereOf(cmp.Or(c.Cwd, c.Start))
		where := w.Repo
		if c.Branch != "" && c.Branch != "HEAD" {
			where += "/" + c.Branch
		}
		mark := ""
		if running[c.ID] != "" {
			mark = "  " + CDim + "running" + COff
		}
		// Its session id last, dim: searched for, as typed or pasted.
		fmt.Fprintf(&b, "%s\t%s  %s%s %4s%s%s  %s%s%s\n", c.ID, oneline.Fit(c.Title, 46), CGrey, oneline.Fit(where, 26), age(now-c.Mod), COff, mark,
			CDim, c.ID, COff)
	}
	if n == 0 {
		e.Mux().Say(3000, "weaver: no conversation to adopt")
		return ""
	}
	out := e.pick(b.String(), formLabel("WEAVER", "ADOPT"), formPrompt("adopt"), "a conversation, or its session id", "")
	query, picked, _ := strings.Cut(out, "\n")
	if v, _, _ := strings.Cut(picked, "\t"); v != "" {
		return v
	}
	// None listed matches: a session id (or its first 4+ characters) is
	// looked for among them all.
	if q := strings.TrimSpace(query); sessionIDish(q) {
		return q
	}
	return ""
}

// sessionIDish: could s be a session id, or the first 4+ characters of one?
func sessionIDish(s string) bool {
	return len(s) >= 4 && strings.Trim(s, "0123456789abcdefABCDEF-") == ""
}
