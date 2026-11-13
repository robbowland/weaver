package rex

import (
	"errors"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/robbowland/weaver/internal/mux"
)

// Fleet is weaver's sessions on Rex as weaver acts on them: its home
// session (the board and the views) and the sessions of its threads, one
// each (a thread's agent, its review panel, its helpers' windows).
// Rex finds a block or window only in its own session, so each call goes
// to the session weaver's numbers say the pane or window is in (in/<num>;
// one numbered before weaver kept that is home's). Here is the session wvr
// runs in, where what you see goes (a popup, a message); home's when it is
// none of them. Methods that steer what you see (focus, names, widths) are
// best-effort and say nothing of failing; a caller that needs to know
// reads the state after.
type Fleet struct {
	Home Session
	Here string
	live map[string]bool // the sessions Rex runs, when asked already (Answer.Keyed)
}

// in is the session of weaver's session id: home, or a thread's.
func (f Fleet) in(id string) Session {
	if id == "" {
		return f.Home
	}
	return f.Home.in(id)
}

// of is the session pane or window n is in.
func (f Fleet) of(n string) Session { return f.in(f.Home.ids().noted("in", n)) }

func (f Fleet) here() Session {
	if f.Here == "" {
		return f.Home
	}
	return f.in(f.Here)
}

// Has: is session id home's, or one of its threads'?
func (f Fleet) Has(id string) bool {
	if id == f.Home.ID {
		return true
	}
	m, ok := f.Home.ids().session(id)
	return ok && m.home == f.Home.ID
}

// threads are the sessions of the threads that run, as Rex lists them.
func (f Fleet) threads() ([]Session, error) {
	live, err := f.live, error(nil)
	if live == nil {
		if live, err = sessions(); err != nil {
			return nil, err
		}
	}
	r := f.Home.ids()
	var out []Session
	for _, id := range r.threads(f.Home.ID) {
		if live[id] {
			out = append(out, f.in(id))
		}
	}
	return out, nil
}

// all is home and the sessions of the threads that run.
func (f Fleet) all() ([]Session, error) {
	ts, err := f.threads()
	return append([]Session{f.Home}, ts...), err
}

// each is what of does for every session of all, asked at once (a snapshot
// is a call each): their answers in order, or the first error.
func each[T any](ss []Session, of func(Session) ([]T, error)) ([]T, error) {
	got := make([][]T, len(ss))
	errs := make([]error, len(ss))
	var wg sync.WaitGroup
	for i, s := range ss {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i], errs[i] = of(s)
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	var out []T
	for _, g := range got {
		out = append(out, g...)
	}
	return out, nil
}

// Made is when home was made, epoch seconds.
func (f Fleet) Made() (int64, bool) { return f.Home.Made() }

// MadeOf is when the session pane or window n is in was made: home's, or
// its thread's (a thread's session taken over by a home made again keeps
// when it was made).
func (f Fleet) MadeOf(n string) (int64, bool) { return f.of(n).Made() }

// Running: does home run?
func (f Fleet) Running() bool { return f.Home.Running() }

// Quit ends the threads' sessions, then home's, and last the one wvr runs
// in (Here: ctrl+q's card runs in a thread's), whose end is wvr's. When
// Rex will not list the threads, nothing: home's end would leave them
// running with no home.
func (f Fleet) Quit() error {
	ts, err := f.threads()
	if err != nil {
		return err
	}
	all := append(ts, f.Home)
	here := func(s Session) int {
		if s.ID == f.Here {
			return 1
		}
		return 0
	}
	slices.SortStableFunc(all, func(a, b Session) int { return here(a) - here(b) })
	for _, s := range all {
		if e := s.Quit(); s.ID == f.Home.ID {
			err = e
		}
	}
	return err
}

// look is a snapshot of home and of every thread's session that runs, in
// one rex do (fleetLua) rather than a call to list the sessions and one
// each to look at them: the sessions in order, home first, and their
// snapshots. A Rex without what that script uses (its error is the
// script's own, "-e:<line>: ...") is looked at session by session.
func (f Fleet) look(full bool) ([]Session, map[string]snapshot, error) {
	ts := f.Home.ids().threads(f.Home.ID)
	var out struct {
		Live  list[string]        `json:"live"`
		Snaps map[string]snapshot `json:"snaps"`
	}
	err := do(f.Home.ID, fleetScript, map[string]any{"full": full, "threads": orNone(ts)}, &out)
	if err != nil && strings.Contains(err.Error(), "-e:") {
		return f.lookEach(full)
	}
	if err != nil {
		return nil, nil, err
	}
	ss := []Session{f.Home}
	for _, id := range ts {
		if _, ok := out.Snaps[id]; ok && id != f.Home.ID {
			ss = append(ss, f.in(id))
		}
	}
	return ss, out.Snaps, nil
}

// lookEach is look, a call for the sessions and one for each.
func (f Fleet) lookEach(full bool) ([]Session, map[string]snapshot, error) {
	ss, err := f.all()
	if err != nil {
		return nil, nil, err
	}
	sns, err := each(ss, func(s Session) ([]snapshot, error) {
		sn, err := s.snapshot(full)
		return []snapshot{sn}, err
	})
	if err != nil {
		return nil, nil, err
	}
	m := map[string]snapshot{}
	for i, s := range ss {
		m[s.ID] = sns[i]
	}
	return ss, m, nil
}

// fleetScript is the script look runs: fleetLua (a test's stand-in).
var fleetScript = fleetLua

// orNone is l, or an empty list for none: [] in JSON, which Lua reads as
// an empty table (null it reads as nil).
func orNone(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

// Panes are the panes of home and of every thread's session; an error
// when one of them did not answer (a pane not listed is one gone).
func (f Fleet) Panes() ([]mux.Pane, error) {
	ss, sns, err := f.look(true)
	if err != nil {
		return nil, err
	}
	var out []mux.Pane
	for _, s := range ss {
		out = append(out, s.panesOf(sns[s.ID])...)
	}
	return out, nil
}

// Windows are the windows of home and of every thread's session; the
// active one is Here's.
func (f Fleet) Windows() ([]mux.Window, error) {
	ss, sns, err := f.look(false)
	if err != nil {
		return nil, err
	}
	here := f.here().ID
	var out []mux.Window
	for _, s := range ss {
		ws := s.windowsOf(sns[s.ID])
		for i := range ws {
			ws[i].Active = ws[i].Active && s.ID == here
		}
		out = append(out, ws...)
	}
	return out, nil
}

// Active is the active pane where wvr runs.
func (f Fleet) Active() (mux.PaneID, bool) { return f.here().Active() }

// Screen is what pane p shows, with its colours.
func (f Fleet) Screen(p mux.PaneID) []byte { return f.of(string(p)).Screen(p) }

// OpenWindow opens a window in home (home's board, a view): its id, its
// pane and its width.
func (f Fleet) OpenWindow(w mux.NewWindow) (mux.WindowID, mux.PaneID, int, error) {
	return f.Home.OpenWindow(w)
}

// OpenSession opens a thread's session, labelled label, w its first
// window: the window, its pane and its width.
func (f Fleet) OpenSession(label string, w mux.NewWindow) (mux.WindowID, mux.PaneID, int, error) {
	var out struct {
		ID      string `json:"session_id"`
		Windows []struct {
			Window string   `json:"window_id"`
			Blocks []string `json:"block_ids"`
		} `json:"initial_windows"`
	}
	if err := call("session.create", map[string]any{"label": cut(label), "initial_windows": []any{
		map[string]any{"window_label": cut(w.Label), "layout": layout(w.Spawn, "")},
	}}, &out); err != nil {
		return "", "", 0, err
	}
	if out.ID == "" || len(out.Windows) == 0 || len(out.Windows[0].Blocks) == 0 {
		return "", "", 0, errors.New("rex: no session made")
	}
	s := f.in(out.ID)
	if err := s.ids().made(out.ID, f.Home.Name, time.Now(), serverPID(), f.Home.ID); err != nil {
		_ = s.Quit()
		return "", "", 0, err
	}
	wn, err := s.num(out.Windows[0].Window)
	if err != nil {
		return "", "", 0, err
	}
	pn, err := s.num(out.Windows[0].Blocks[0])
	if err != nil {
		return "", "", 0, err
	}
	_ = s.ids().note("cmd", pn, strings.Join(w.Argv, " "))
	if !w.Background {
		s.show(out.Windows[0].Window)
	}
	return mux.WindowID(wn), mux.PaneID(pn), s.drawnCols(mux.PaneID(pn)), nil
}

// CloseSession ends the session pane p is in, every pane in it with it;
// never home's.
func (f Fleet) CloseSession(p mux.PaneID) {
	if s := f.of(string(p)); s.ID != f.Home.ID {
		_ = s.Quit()
	}
}

// SplitLeft opens a pane on the left of pane of: the new pane (its
// program says its own pid: Rex starts it through the user's shell).
func (f Fleet) SplitLeft(of mux.PaneID, s mux.NewSplit) (mux.PaneID, error) {
	return f.of(string(of)).SplitLeft(of, s)
}

// Respawn starts s again in pane p, ending what runs there.
func (f Fleet) Respawn(p mux.PaneID, s mux.Spawn) error { return f.of(string(p)).Respawn(p, s) }

// Close ends pane p.
func (f Fleet) Close(p mux.PaneID) { f.of(string(p)).Close(p) }

// Label names window w, and Title pane p.
func (f Fleet) Label(w mux.WindowID, label string) { f.of(string(w)).Label(w, label) }
func (f Fleet) Title(p mux.PaneID, title string)   { f.of(string(p)).Title(p, title) }

// SetWidth makes pane p cols wide; Moved says whether someone else moved
// the split beside it; Zoom has it fill its window, or back.
func (f Fleet) SetWidth(p mux.PaneID, cols int) { f.of(string(p)).SetWidth(p, cols) }
func (f Fleet) Moved(p mux.PaneID) bool         { return f.of(string(p)).Moved(p) }
func (f Fleet) Zoom(p mux.PaneID)               { f.of(string(p)).Zoom(p) }

// Show shows window w; Focus pane p, its window first.
func (f Fleet) Show(w mux.WindowID) { f.of(string(w)).Show(w) }
func (f Fleet) Focus(p mux.PaneID)  { f.of(string(p)).Focus(p) }

// Say shows text for ms milliseconds where wvr runs; SayToSession in home,
// to everyone looking at it.
func (f Fleet) Say(ms int, text string)          { f.here().Say(ms, text) }
func (f Fleet) SayToSession(ms int, text string) { f.Home.Say(ms, text) }

// Popup is the command that shows argv over where wvr runs, sized by size
// from the terminal's columns and lines, in dir (empty: Rex's choice); it
// returns when the popup closes. False when no app shows the session.
func (f Fleet) Popup(size func(cols, lines int) (int, int), dir string, argv []string) (*exec.Cmd, bool) {
	return f.here().Popup(size, dir, argv)
}
