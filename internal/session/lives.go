package session

import (
	"strings"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/process"
	"github.com/robbowland/weaver/internal/rex"
	"github.com/robbowland/weaver/internal/store"
)

// lives is what Rex says of the sessions weaver's files name, and what
// weaver makes of it: Rex is asked a thing at most once while it is kept,
// so every file one pass looks at (a gc run, the board's rows) is judged
// by the same answers. A file is keyed by its session (rex-<name>); a key
// of another (tmux's or zellij's, from before weaver ran on Rex only)
// names a session that no longer runs, and is never lost.
type lives struct {
	e    *Env
	boot int64 // when the machine started (epoch s); 0 when not known

	rex   rex.Answer
	rexes map[string]*rexLife
}

// rexLife is a Rex session, as one key names it, with its threads'
// sessions: whether it runs and whether Rex said, and their panes once
// asked (a snapshot of each session).
type rexLife struct {
	s                 rex.Fleet
	running, answered bool
	listed            bool
	panes             []mux.Pane
	listErr           error
}

func (e *Env) newLives() *lives { return &lives{e: e, boot: process.BootTime()} }

// beforeBoot: is ts from before the machine started? No session outlives
// that.
func (l *lives) beforeBoot(ts int64) bool { return l.boot > 0 && ts < l.boot }

// onRex is the Rex session skey names, when it is a Rex key.
func (l *lives) onRex(skey string) (*rexLife, bool) {
	if !strings.HasPrefix(skey, "rex-") {
		return nil, false
	}
	if l.rexes == nil {
		l.rexes = map[string]*rexLife{}
	}
	a := l.rexes[skey]
	if a == nil {
		a = &rexLife{}
		a.s, a.running, a.answered = l.rex.Keyed(l.e.RexDir(), skey)
		l.rexes[skey] = a
	}
	return a, true
}

// held: is a Rex session's file of ts held off, Rex not answering of it?
// All but one from before the machine started.
func (l *lives) held(a *rexLife, ts int64) bool { return !a.answered && !l.beforeBoot(ts) }

// running: does skey's session run, for a file of ts? While Rex does not
// answer, it does (but for a file from before the machine started).
func (l *lives) running(skey string, ts int64) bool {
	a, ok := l.onRex(skey)
	return ok && (a.running || l.held(a, ts))
}

// madeAfter: is skey's session one made after ts, another life of it, the
// one a file of ts knew having gone? (A name on two servers: the earlier.)
func (l *lives) madeAfter(skey string, ts int64) bool {
	a, ok := l.onRex(skey)
	if !ok {
		return false
	}
	made, known := a.s.Made()
	return a.running && known && ts > 0 && made > ts
}

// kept: is the pane of st, the state of an agent that has died, kept (its
// agent exited, its thread to be started again or closed: the board's to
// show, not to retire), or can that not be told?
func (l *lives) kept(skey string, st agent.Record) bool {
	a, ok := l.onRex(skey)
	switch {
	case !ok:
		return false
	case l.held(a, st.TS):
		return true
	case !a.running || !a.s.Has(st.Session):
		return false
	}
	if !a.listed {
		a.panes, a.listErr = a.s.Panes()
		a.listed = true
	}
	_, shown := PaneIn(a.panes, paneOfNum(st.Pane))
	return a.listErr != nil || shown
}

// lost: did skey's session go since ts (epoch s), its agent having run in
// life w of it (when its state says)? When the machine has started since
// ts (no server outlives that, and one started since may have the old
// one's pid); else as Rex says (rex.Answer.Lost), known false while Rex
// does not answer (a thread taken for lost while it runs would be resumed
// twice, and one taken for not lost forgotten). A key of another
// multiplexer's never is.
func (l *lives) lost(skey string, ts int64, w agent.Where) (lost, known bool) {
	switch {
	case !strings.HasPrefix(skey, "rex-"):
		return false, true
	case l.beforeBoot(ts):
		return true, true
	}
	return l.rex.Lost(w)
}

// lostCheck: is the dead agent of state file f a thread lost with its
// session, and could that be told? A thread not ended on purpose whose
// session has gone since its last event (whether or not its conversation
// is known: a thread lasts until you close it).
func (l *lives) lostCheck(f string) (lost, known bool) {
	b := strings.TrimSuffix(f, ".json")
	if !files.Exists(b+".thread") || !files.Exists(f) {
		return false, true
	}
	s, err := store.ReadState(f)
	if err != nil {
		return false, true
	}
	switch s.EndReason {
	case "prompt_input_exit", "logout", "clear":
		return false, true
	}
	return l.lost(KeyOfFile(f), s.TS, s.Where)
}

// retire: the agent of state file f (pid) is gone: its thread lost with
// its session is kept, anything else forgotten. When whether its session
// went cannot be told (Rex not answering), nothing yet: a later look
// decides.
func (l *lives) retire(f, pid string) {
	lost, known := l.lostCheck(f)
	switch {
	case !known:
		return
	case lost && l.e.Lose(f, pid):
		return
	}
	l.e.Forget(f, pid)
}
