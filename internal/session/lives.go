package session

import (
	"slices"
	"strings"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/process"
	"github.com/robbowland/weaver/internal/rex"
	"github.com/robbowland/weaver/internal/store"
)

// lives is what weaver's multiplexers say of the sessions weaver's files
// name, and what weaver makes of it: each multiplexer is asked a thing at
// most once while it is kept, so every file one pass looks at (a gc run,
// the board's rows) is judged by the same answers. A file is keyed by its
// session (tmux-<name>, rex-<name>); a key of neither (zellij's, from
// before weaver ran on tmux) is judged as tmux's, but never lost.
type lives struct {
	e    *Env
	boot int64 // when the machine started (epoch s); 0 when not known

	tmuxAsked    bool
	tmux         []tmuxSession
	tmuxAnswered bool
	shown        map[paneLife]bool // tmux's panes; nil until asked

	rex   rex.Answer
	rexes map[string]*rexLife
}

// rexLife is a Rex session, as one key names it: whether it runs and
// whether Rex said, and its panes once asked (a snapshot of the session).
type rexLife struct {
	s                 rex.Session
	running, answered bool
	listed            bool
	panes             []mux.Pane
	listErr           error
}

func (e *Env) newLives() *lives { return &lives{e: e, boot: process.BootTime()} }

// beforeBoot: is ts from before the machine started? No session outlives
// that.
func (l *lives) beforeBoot(ts int64) bool { return l.boot > 0 && ts < l.boot }

// tmuxSessions are tmux's running sessions, on weaver's server and the
// one weaver runs in; false when neither answered.
func (l *lives) tmuxSessions() ([]tmuxSession, bool) {
	if !l.tmuxAsked {
		l.tmux, l.tmuxAnswered = l.e.tmuxSessions()
		l.tmuxAsked = true
	}
	return l.tmux, l.tmuxAnswered
}

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

// running: does skey's session run, for a file of ts? While its
// multiplexer does not answer, it does (but for a Rex file from before the
// machine started).
func (l *lives) running(skey string, ts int64) bool {
	if a, ok := l.onRex(skey); ok {
		return a.running || l.held(a, ts)
	}
	ss, answered := l.tmuxSessions()
	return !answered || slices.ContainsFunc(ss, func(s tmuxSession) bool { return s.key == skey })
}

// madeAfter: is skey's session one made after ts, another life of it, the
// one a file of ts knew having gone? (A name on two servers: the earlier.)
func (l *lives) madeAfter(skey string, ts int64) bool {
	if a, ok := l.onRex(skey); ok {
		made, known := a.s.Made()
		return a.running && known && ts > 0 && made > ts
	}
	ss, _ := l.tmuxSessions()
	made := int64(0)
	for _, s := range ss {
		if s.key == skey && (made == 0 || s.created < made) {
			made = s.created
		}
	}
	return made > 0 && ts > 0 && made > ts
}

// kept: is the pane of st, the state of an agent that has died, kept (its
// agent exited, its thread to be started again or closed: the board's to
// show, not to retire), or can that not be told?
func (l *lives) kept(skey string, st agent.Record) bool {
	p := paneOfNum(st.Pane)
	if a, ok := l.onRex(skey); ok {
		if l.held(a, st.TS) {
			return true
		}
		if !a.running || a.s.ID != st.Session {
			return false
		}
		if !a.listed {
			a.panes, a.listErr = a.s.Panes()
			a.listed = true
		}
		_, shown := PaneIn(a.panes, p)
		return a.listErr != nil || shown
	}
	if l.shown == nil {
		l.shown = l.e.panesShown()
	}
	return l.shown[paneLife{skey, p, st.Where}]
}

// lost: did skey's session go since ts (epoch s), its agent having run in
// life w of it (when its state says)? When the machine has started since
// ts (no server outlives that, and one started since may have the old
// one's pid). Else a tmux session, when no server has that life of it any
// more: no session of its name, or one on another server, or one made
// again on the same server (another id; older states, without one: a
// session made after ts). A Rex session, as Rex says (rex.Answer.Lost),
// known false while Rex does not answer, when it is neither (a thread
// taken for lost while it runs would be resumed twice, and one taken for
// not lost forgotten). Any other key never is.
func (l *lives) lost(skey string, ts int64, w agent.Where) (lost, known bool) {
	rexKey := strings.HasPrefix(skey, "rex-")
	if !strings.HasPrefix(skey, "tmux-") && !rexKey {
		return false, true
	}
	if l.beforeBoot(ts) {
		return true, true
	}
	if rexKey {
		return l.rex.Lost(w)
	}
	ss, _ := l.tmuxSessions()
	for _, s := range ss {
		if s.key != skey {
			continue
		}
		switch {
		case w.Server == 0:
			if s.created <= ts {
				return false, true
			}
		case s.where.Server != w.Server:
		case w.Session != "":
			if s.where.Session == w.Session {
				return false, true
			}
		case s.created <= ts:
			return false, true
		}
	}
	return true, true
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
