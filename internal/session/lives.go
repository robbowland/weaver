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
// by the same answers. A file is keyed by its session (rex-<name>).
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

// life is the Rex session skey names.
func (l *lives) life(skey string) *rexLife {
	if l.rexes == nil {
		l.rexes = map[string]*rexLife{}
	}
	a := l.rexes[skey]
	if a == nil {
		a = &rexLife{}
		a.s, a.running, a.answered = l.rex.Keyed(l.e.RexDir(), skey)
		l.rexes[skey] = a
	}
	return a
}

// held: is a Rex session's file of ts held off, Rex not answering of it?
// All but one from before the machine started.
func (l *lives) held(a *rexLife, ts int64) bool { return !a.answered && !l.beforeBoot(ts) }

// running: does skey's session run, for a file of ts? While Rex does not
// answer, it does (but for a file from before the machine started).
func (l *lives) running(skey string, ts int64) bool {
	a := l.life(skey)
	return a.running || l.held(a, ts)
}

// madeAfter: is the session per-pane file f's pane is in one made after
// ts, another life of it, the one a file of ts knew having gone? (A name
// on two servers: the earlier.) Its pane's own session, home's or its
// thread's: a home made again after the files of the threads it took
// over is no other life of theirs.
func (l *lives) madeAfter(f string, ts int64) bool {
	a := l.life(KeyOfFile(f))
	made, known := a.s.MadeOf(string(PaneOfFile(f)))
	return a.running && known && ts > 0 && made > ts
}

// kept: is the pane of st, the state of an agent that has died, kept (its
// agent exited, its thread to be started again or closed: the board's to
// show, not to retire), or can that not be told?
func (l *lives) kept(skey string, st agent.Record) bool {
	a := l.life(skey)
	switch {
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

// lost: did an agent's session go since ts (epoch s), it having run in
// life w of it (when its state says)? When the machine has started since
// ts (no server outlives that, and one started since may have the old
// one's pid); else as Rex says (rex.Answer.Lost), known false while Rex
// does not answer (a thread taken for lost while it runs would be resumed
// twice, and one taken for not lost forgotten).
func (l *lives) lost(ts int64, w agent.Where) (lost, known bool) {
	if l.beforeBoot(ts) {
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
	if endedOnPurpose(s.EndReason) {
		// You ended its agent (its pane kept, shown exited): a thread
		// lasts until you close it, so it is kept when the machine or the
		// server it ran on went, which is no close of yours; its session
		// gone on the server it ran on may be its window closed by hand,
		// which closes it. (Quitting keeps it: KeepExited.)
		return l.serverWent(KeyOfFile(f), s.TS, s.Where)
	}
	return l.lost(s.TS, s.Where)
}

// endedOnPurpose: did the agent end as you asked (/exit, a logout, /clear)?
func endedOnPurpose(reason string) bool {
	switch reason {
	case "prompt_input_exit", "logout", "clear":
		return true
	}
	return false
}

// serverWent: has the machine started, or the server life w ran on gone,
// since ts? As lost, without its session's own going.
func (l *lives) serverWent(skey string, ts int64, w agent.Where) (gone, known bool) {
	switch {
	case !strings.HasPrefix(skey, "rex-"):
		return false, true
	case l.beforeBoot(ts):
		return true, true
	case w.Session == "":
		return false, true
	}
	return l.rex.ServerGone(w)
}

// KeepExited has each thread of rows (the session's, as a look just
// took them) whose agent you ended, its pane kept and shown exited, kept
// as lost with the rest when its session goes, as quitting does to every
// thread: its state forgets why it ended. Not one whose window you have
// closed since (a thread closed), which no row shows. What it returns
// puts the reasons back (the quit failed).
func (e *Env) KeepExited(rows []Row) (undo func()) {
	was := map[string]string{}
	for _, r := range rows {
		if r.Tnum == 0 || !r.Exited || !strings.HasSuffix(r.Sfile, ".json") {
			continue
		}
		store.UpdateState(r.Sfile, func(st *agent.Record) {
			if endedOnPurpose(st.EndReason) {
				was[r.Sfile], st.EndReason = st.EndReason, ""
			}
		})
	}
	return func() {
		for f, reason := range was {
			store.UpdateState(f, func(st *agent.Record) {
				if st.EndReason == "" {
					st.EndReason = reason
				}
			})
		}
	}
}

// retire: the agent of state file f (pid) is gone: its thread lost with
// its session is kept, anything else forgotten. A thread's agent gone
// while its session runs on (its window closed by hand, its helpers'
// running) is the thread closed: its session ends with it, as close ends
// it. When whether its session went cannot be told (Rex not answering),
// nothing yet: a later look decides.
func (l *lives) retire(f, pid string) {
	lost, known := l.lostCheck(f)
	switch {
	case !known:
		return
	case lost:
		// Kept as lost; when that fails (its record not written), its
		// files stay for a later look, never forgotten for want of one.
		l.e.Lose(f, pid)
		return
	}
	if a := l.life(KeyOfFile(f)); a.running && files.Exists(strings.TrimSuffix(f, ".json")+".thread") {
		// Only while its state still names that agent: one started in its
		// pane since (a restart, a resume) is the thread's.
		l.e.forget(f, pid, func() { a.s.CloseSession(PaneOfFile(f)) })
		return
	}
	l.e.Forget(f, pid)
}
