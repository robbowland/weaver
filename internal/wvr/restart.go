package wvr

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
	"github.com/robbowland/weaver/internal/tmux"
)

// Restarting: a thread's agent started again where it is, resuming its
// conversation, so an updated harness (a new Claude Code or Codex) takes
// over without closing the session. Its window, nvim and number stay.

const restartUsage = "usage: wvr restart [--all] [--force] [pane-id]"

// errMidTurn: the agent is working or waiting on you, and restarting it
// would stop its turn.
var errMidTurn = errors.New("mid-turn")

// Restart is `wvr restart [--all] [--force] [PANE]`: the agent of the thread
// in PANE (else this pane's), or with --all every thread's, started again,
// resuming its conversation. One mid-turn is left unless --force. What it
// did is said on the status line too, as the board's R runs it out of
// sight.
func (e *Env) Restart(args []string) error {
	all, force, which := false, false, ""
	for _, a := range args {
		switch {
		case a == "--all":
			all = true
		case a == "--force":
			force = true
		case strings.HasPrefix(a, "-") || which != "":
			return errors.New(restartUsage)
		default:
			which = a
		}
	}
	if err := e.toWeaverSession(); err != nil {
		return err
	}
	var panes []tmux.PaneID
	switch {
	case all:
		for _, f := range e.SessionFiles("thread") {
			panes = append(panes, session.PaneOfFile(f))
		}
	case which != "":
		p, ok := paneArg(which)
		if !ok {
			return errors.New(restartUsage)
		}
		panes = append(panes, p)
	case e.MyPane != "":
		panes = append(panes, e.agentOf(e.MyPane))
	default:
		return errors.New(restartUsage)
	}
	var done, busy, failed []string
	for _, p := range panes {
		label, err := e.restartAgent(p, force)
		switch {
		case errors.Is(err, errMidTurn):
			busy = append(busy, label)
		case err != nil:
			failed = append(failed, label+": "+err.Error())
		default:
			done = append(done, label)
		}
	}
	msg := restartSummary(done, busy, failed)
	fmt.Println(msg)
	e.Tmux("display-message", "-d", "4000", msg)
	e.PushStatus()
	if len(failed) > 0 {
		return errors.New(strings.Join(failed, "; "))
	}
	return nil
}

// restartSummary says what a restart did: the threads restarted, those left
// mid-turn, and those it could not.
func restartSummary(done, busy, failed []string) string {
	sort.Strings(done)
	sort.Strings(busy)
	var parts []string
	if len(done) > 0 {
		parts = append(parts, "restarted "+strings.Join(done, " "))
	}
	if len(busy) > 0 {
		parts = append(parts, "left "+strings.Join(busy, " ")+" mid-turn (--force restarts it all the same)")
	}
	if len(failed) > 0 {
		parts = append(parts, "could not restart "+strings.Join(failed, "; "))
	}
	if len(parts) == 0 {
		return "weaver: no thread's agent to restart"
	}
	return "weaver: " + strings.Join(parts, "; ")
}

// restartAgent starts the agent of thread p again in its pane: its records
// go through the meeting place a restored thread's do (writeResume), then
// tmux's respawn-pane ends the agent there and runs the pane's `wvr _agent`
// again, which resumes the conversation. The thread's label ("01"), and
// errMidTurn when it is mid-turn and not forced.
func (e *Env) restartAgent(p tmux.PaneID, force bool) (string, error) {
	th, err := store.ReadThread(e.ThreadFile(p))
	if err != nil {
		return "%" + string(p), errors.New("not a thread (adopt it first)")
	}
	label := fmt.Sprintf("%02d", th.Num)
	st, err := store.ReadState(e.StateFile(p))
	if err != nil || st.SessionID == "" {
		return label, errors.New("its agent has not said which conversation it is in yet")
	}
	if !force && (st.State == agent.StateWorking || st.State == agent.StateNeedsYou) {
		return label, errMidTurn
	}
	kind, ok := agent.ParseKind(string(th.Agent))
	if !ok {
		kind = agent.Claude
	}
	tok := token()
	d := e.threadsNew() + "/" + e.SKey + "_" + tok
	if err := os.MkdirAll(d, 0o755); err != nil {
		return label, fmt.Errorf("cannot create %s", d)
	}
	th.Pane = nil
	if err := e.writeResume(d, th, st, st.SessionID); err != nil {
		os.RemoveAll(d)
		return label, err
	}
	dir := store.Lost{State: st, Thread: th}.Dir()
	if _, err := e.Tmux("respawn-pane", "-k", "-t", p.Target(), "-c", dir, "-e", "WEAVER_TMUX_SESSION="+e.Session,
		e.Self, "_agent", tok, string(kind)); err != nil {
		os.RemoveAll(d)
		return label, errors.New("tmux could not restart it")
	}
	return label, nil
}
