package wvr

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/oneline"
	"github.com/robbowland/weaver/internal/process"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
)

// Restarting: a thread's agent started again where it is, resuming its
// conversation, so an updated harness (a new Claude Code) takes
// over without closing the session. Its window, review panel and number stay.

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
	var panes []mux.PaneID
	switch {
	case all:
		for _, f := range e.SessionFiles("thread") {
			panes = append(panes, session.PaneOfFile(f))
		}
	case which != "":
		p, ok := e.paneArg(which)
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
	e.Mux().Say(4000, msg)
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
// Respawn ends the agent there and runs the pane's `wvr _agent` again, which resumes the conversation. The thread's label ("01"), and
// errMidTurn when it is mid-turn and not forced.
func (e *Env) restartAgent(p mux.PaneID, force bool) (string, error) {
	th, err := store.ReadThread(e.ThreadFile(p))
	if err != nil {
		return "pane " + string(p), errors.New("not a thread (adopt it first)")
	}
	label := fmt.Sprintf("%02d", th.Num)
	// Mid-turn as the board says it; an agent that has exited never is.
	if r, now, ok := e.RowOf(p); ok && !force && r.MidTurn(now) {
		return label, errMidTurn
	}
	// Its conversation, as its state says, else as its record does; none
	// known (it never reported): its agent starts afresh.
	st, _ := store.ReadState(e.StateFile(p))
	st.SessionID = cmp.Or(st.SessionID, th.SessionID)
	tok := token()
	d := e.opening(tok)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return label, fmt.Errorf("cannot create %s", d)
	}
	th.Pane = nil
	if err := e.writeResume(d, th, st, st.SessionID); err != nil {
		os.RemoveAll(d)
		return label, err
	}
	dir := store.Lost{State: st, Thread: th}.Dir()
	e.Restarting(e.StateFile(p))
	if err := e.Mux().Respawn(p, e.agentSpawn(tok, dir)); err != nil {
		os.RemoveAll(d)
		return label, errors.New("could not restart it in Rex")
	}
	// On Rex the agent starts again in a pane put in its place.
	e.settleWidths(p)
	return label, nil
}

// An update of Claude Code under running agents: each agent whose process
// started before the installed claude last changed runs the old one. Its
// row says so (update · R), and once it has rested restIdle, the home
// board's tender (Env.tend) restarts it onto the new one, as R does.

// claudeInstalled is the Claude Code a thread's agent starts now: the file
// its command (agentCmd) resolves to on $PATH, links followed, and when
// that file last changed; zero when there is none.
func claudeInstalled() (string, time.Time) {
	w := agent.Command(agentCmd(), agent.ModeWork)
	if len(w) == 0 {
		return "", time.Time{}
	}
	p, err := exec.LookPath(w[0])
	if err != nil {
		return "", time.Time{}
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	st, err := os.Stat(p)
	if err != nil {
		return "", time.Time{}
	}
	return p, st.ModTime()
}

// outdated: does row r's agent run an older Claude Code than the one
// installed, which last changed at installed? Its process started before
// that (to the second its start is recorded to). An agent whose start is
// not recorded yet (restarted, not reported since) is not.
func outdated(r session.Row, installed time.Time) bool {
	if installed.IsZero() || r.Exited || r.Pstart == "" {
		return false
	}
	t, ok := process.ParseStart(r.Pstart)
	return ok && t.Before(installed.Truncate(time.Second))
}

// reClaudeVersion is a version as a native install names its file: 2.1.330.
var reClaudeVersion = regexp.MustCompile(`^\d+\.\d+(\.\d+)*$`)

// claudeName is the Claude Code at path as a message names it: "Claude
// Code 2.1.330", its version read beside it (the npm package's
// package.json above it, or a native install's file, named for its
// version); else "the new Claude Code".
func claudeName(path string) string {
	if path == "" {
		return "the new Claude Code"
	}
	if b := filepath.Base(path); reClaudeVersion.MatchString(b) {
		return "Claude Code " + b
	}
	d := filepath.Dir(path)
	for range 3 {
		var pkg struct{ Name, Version string }
		if store.Read(d+"/package.json", &pkg) == nil && strings.HasSuffix(pkg.Name, "claude-code") && pkg.Version != "" {
			return "Claude Code " + oneline.Line(pkg.Version)
		}
		d = filepath.Dir(d)
	}
	return "the new Claude Code"
}

// restIdle is how long (s) an agent behind an update rests before it is
// restarted onto it: long enough that you are not about to prompt it.
const restIdle = 600

// atRest: is row r's agent waiting for your next prompt, asking nothing of
// you (not even a look) and with nothing under way: no turn, background
// task or subagent?
func atRest(r session.Row, now int64) bool {
	return !r.Exited && r.Need(now) == session.NeedNothing && !r.NotReporting(now) &&
		r.State != agent.StateStarting && r.Bg == 0 && r.Nsub == 0
}

// restartable are the threads' agents among rows, at now, that an update
// installed at installed restarts by themselves: behind it (outdated), at
// rest for restIdle or more, their helpers at rest too (none mid-turn or
// with a task in the background), and not in the Rex session shown, the
// one you look at, where a prompt you are typing would be lost.
func restartable(rows []session.Row, now int64, installed time.Time, shown string) []mux.PaneID {
	busy := map[string]bool{} // threads' sessions with a helper at work
	for _, r := range rows {
		if r.Helper() && (r.MidTurn(now) || r.Bg > 0 || r.Nsub > 0) {
			busy[r.Session] = true
		}
	}
	var out []mux.PaneID
	for _, r := range rows {
		if r.Tnum == 0 || r.Session == "" || r.Session == shown || busy[r.Session] ||
			!atRest(r, now) || now-r.Since < restIdle || !outdated(r, installed) {
			continue
		}
		out = append(out, r.Pane)
	}
	return out
}

// shownSession is the Rex session the Rex app shows (appSession); a
// test's stand-in.
var shownSession = appSession

// reLastFocused is the app's preference for the session it shows, in its
// preferences as `defaults export` writes them: base64 of
// {"sessionID":"session:…"}.
var reLastFocused = regexp.MustCompile(`<key>Workspace\.lastFocusedSession</key>\s*<data>([^<]*)</data>`)

// appSession is the Rex session the Rex app shows, as the app keeps it in
// its preferences (Workspace.lastFocusedSession): Rex itself does not say
// which session an app shows. False when that cannot be read.
func appSession() (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "defaults", "export", "com.superlogical.rex", "-").Output()
	if err != nil {
		return "", false
	}
	return lastFocused(out)
}

// lastFocused is the session the app's preferences, prefs (as `defaults
// export` writes them), say it shows.
func lastFocused(prefs []byte) (string, bool) {
	m := reLastFocused.FindSubmatch(prefs)
	if m == nil {
		return "", false
	}
	b, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(string(m[1])), ""))
	if err != nil {
		return "", false
	}
	var s struct {
		ID string `json:"sessionID"`
	}
	if json.Unmarshal(b, &s) != nil || s.ID == "" {
		return "", false
	}
	return s.ID, true
}

// restartUpdated restarts, each onto the Claude Code installed now, the
// threads' agents an update leaves behind once they rest (restartable),
// resuming their conversations as R does, and says so where you are:
// "weaver: restarted 03 onto Claude Code 2.1.330". Not knowing which
// session you look at, it restarts none.
func (e *Env) restartUpdated() {
	path, installed := claudeInstalled()
	if installed.IsZero() {
		return
	}
	rows, now := e.AgentRows()
	if !slices.ContainsFunc(rows, func(r session.Row) bool { return r.Tnum > 0 && outdated(r, installed) }) {
		return
	}
	shown, ok := shownSession()
	if !ok {
		return
	}
	var done []string
	for _, p := range restartable(rows, now, installed, shown) {
		if label, err := e.restartAgent(p, false); err == nil {
			done = append(done, label)
		}
	}
	if len(done) == 0 {
		return
	}
	sort.Strings(done)
	m := e.Mux()
	if m.Has(shown) {
		m.Here = shown
	}
	m.Say(4000, "weaver: restarted "+strings.Join(done, " ")+" onto "+claudeName(path))
	e.PushStatus()
}
