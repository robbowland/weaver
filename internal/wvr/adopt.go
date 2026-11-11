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
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/oneline"
	"github.com/robbowland/weaver/internal/process"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
)

// Adopting: a conversation weaver did not start becomes a thread, kept as
// threads are (lost with its session, brought back as weaver starts). An
// agent you started by hand, and a conversation that runs nowhere, resume
// in a thread's session of its own.

const adoptUsage = "usage: wvr adopt [pane-id | session-id]"

// Adopt is `wvr adopt [PANE | SESSION-ID]`: the agent in PANE, else the
// one in this pane, made a thread from its pane; conversation SESSION-ID
// (or its first 4+ characters) resumed as a thread; with neither, a picker
// of Claude Code's recent conversations. Outside it, into the weaver
// session.
func (e *Env) Adopt(args []string) error {
	if len(args) > 1 || len(args) == 1 && strings.HasPrefix(args[0], "-") {
		return errors.New(adoptUsage)
	}
	// From a terminal outside it, into the weaver session.
	if err := e.toWeaverSession(); err != nil {
		return err
	}
	which := arg(args, 0)
	var msg string
	var err error
	picked := false
	switch p, ok := e.paneArg(which); {
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

// paneArg is which as a pane: a number. Pane numbers have no length
// (they grow with each Rex block weaver meets), but a number of sidPrefix digits or more could be
// the start of a session id (adopt's) or a name (rename's): it is a pane
// only when weaver follows an agent there.
func (e *Env) paneArg(which string) (mux.PaneID, bool) {
	p, ok := mux.ParsePane(which)
	if !ok || len(p) < sidPrefix {
		return p, ok
	}
	return p, e.agentOf(p) == p
}

// sidPrefix is the fewest characters of a session id that name it.
const sidPrefix = 4

// adoptPane makes the agent in pane p, which weaver follows but did not
// start, a thread: its agent ends with its pane, between turns and with
// nothing in the background (one process to a conversation), and its conversation resumes in a thread's
// session of its own (resumeThread). Typed into the agent (`! wvr adopt`),
// this runs in the pane that goes: the rest is done from outside it.
func (e *Env) adoptPane(p mux.PaneID) (string, error) {
	st, err := store.ReadState(e.StateFile(p))
	if err != nil {
		return "", fmt.Errorf("weaver follows no agent in pane %s", p)
	}
	sid := oneline.Line(st.SessionID)
	pid, start := oneline.Line(string(st.Pid)), oneline.Line(st.ProcStart)
	switch {
	case st.State == agent.StateEnded || pid != "" && !process.StillRuns(pid, start, nil):
		return "", fmt.Errorf("its agent has ended: `wvr adopt %s` resumes its conversation as a thread", cmp.Or(sid, "SESSION-ID"))
	case sid == "":
		return "", errors.New("its agent has not said which conversation it is in yet")
	case st.State == agent.StateWorking || st.State == agent.StateNeedsYou:
		return "", errors.New("its turn is under way: adopt it once it is done")
	case st.Background > 0 || len(st.Subagents) > 0:
		// Its pane goes, and what runs in it with it.
		return "", errors.New("its background work is under way: adopt it once that is done")
	}
	if th, err := store.ReadThread(e.ThreadFile(p)); err == nil {
		return "", fmt.Errorf("%s is thread %d already", cmp.Or(oneline.Line(th.Name), "it"), th.Num)
	}
	if p == e.MyPane {
		e.outside("adopt", sid)
		return "weaver: its agent ends here, and its conversation resumes in a thread's session", nil
	}
	e.Mux().Close(p)
	for i := 0; i < 50 && pid != "" && process.StillRuns(pid, start, nil); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if pid != "" && process.StillRuns(pid, start, nil) {
		return "", fmt.Errorf("its agent (pid %s) is still running: adopt it once it has exited", pid)
	}
	e.Forget(e.StateFile(p), pid)
	return e.resumeThread(sid)
}

// adoptConversation makes conversation which (its session id, or its
// first 4+ characters) a thread: one a session lost is resumed as lost
// threads are; one an agent here runs is adopted from its pane
// (adoptPane); one that runs nowhere is resumed (resumeThread).
func (e *Env) adoptConversation(which string) (string, error) {
	match := func(sid string) bool {
		return sid != "" && (sid == which || len(which) >= sidPrefix && strings.HasPrefix(sid, which))
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
	return e.resumeThread(which)
}

// resumeThread resumes conversation which (its session id, or its first
// 4+ characters), which runs nowhere, as a thread in a session of its own,
// in the directory it started in.
func (e *Env) resumeThread(which string) (string, error) {
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
	out := e.pick(b.String(), formLabel("ADOPT"), formPrompt("adopt"), "a conversation, or its session id", "")
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
	return len(s) >= sidPrefix && strings.Trim(s, "0123456789abcdefABCDEF-") == ""
}
