package rex

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/mux"
)

// Fleet is weaver's sessions on Rex as weaver acts on them (mux.Mux): its
// home session (the board and the views) and the sessions of its threads,
// one each (a thread's agent, its review panel, its helpers' windows).
// Rex finds a block or window only in its own session, so each call goes
// to the session weaver's numbers say the pane or window is in (in/<num>;
// one numbered before weaver kept that is home's). Here is the session wvr
// runs in, where what you see goes (a popup, a message); home's when it is
// none of them.
type Fleet struct {
	Home Session
	Here string
	live map[string]bool // the sessions Rex runs, when asked already (Answer.Keyed)
}

var _ mux.Mux = Fleet{}

// in is the session of weaver's session id: home, or a thread's.
func (f Fleet) in(id string) Session {
	if id == "" {
		return f.Home
	}
	return Session{ID: id, Name: f.Home.Name, Dir: f.Home.Dir}
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

// Threads are the sessions of the threads that run, as Rex lists them.
// Weaver's record of one that has gone goes too, but not in its first
// minute: it may have been made since Rex listed them.
func (f Fleet) Threads() ([]Session, error) {
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
		} else if m, _ := r.session(id); m.at < float64(time.Now().Unix()-60) {
			os.Remove(r.path("sessions", fileName(id)))
		}
	}
	return out, nil
}

// all is home and the sessions of the threads that run.
func (f Fleet) all() ([]Session, error) {
	ts, err := f.Threads()
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

func (f Fleet) Life() agent.Where   { return f.Home.Life() }
func (f Fleet) Made() (int64, bool) { return f.Home.Made() }
func (f Fleet) Running() bool       { return f.Home.Running() }

// Quit ends the threads' sessions, then home's.
func (f Fleet) Quit() error {
	ts, _ := f.Threads()
	for _, t := range ts {
		_ = t.Quit()
	}
	return f.Home.Quit()
}

// Panes are the panes of home and of every thread's session; an error
// when one of them did not answer (a pane not listed is one gone).
func (f Fleet) Panes() ([]mux.Pane, error) {
	ss, err := f.all()
	if err != nil {
		return nil, err
	}
	return each(ss, Session.Panes)
}

// Windows are the windows of home and of every thread's session; the
// active one is Here's.
func (f Fleet) Windows() ([]mux.Window, error) {
	ss, err := f.all()
	if err != nil {
		return nil, err
	}
	here := f.here().ID
	return each(ss, func(s Session) ([]mux.Window, error) {
		ws, err := s.Windows()
		for i := range ws {
			ws[i].Active = ws[i].Active && s.ID == here
		}
		return ws, err
	})
}

func (f Fleet) Active() (mux.PaneID, bool)   { return f.here().Active() }
func (f Fleet) PID(p mux.PaneID) (int, bool) { return f.of(string(p)).PID(p) }
func (f Fleet) Screen(p mux.PaneID) []byte   { return f.of(string(p)).Screen(p) }

// OpenWindow opens a window in home (home's board, a view).
func (f Fleet) OpenWindow(w mux.NewWindow) (mux.WindowID, mux.PaneID, int, error) {
	return f.Home.OpenWindow(w)
}

// OpenSession opens a thread's session, labelled label, w its first
// window (OpenSession).
func (f Fleet) OpenSession(label string, w mux.NewWindow) (mux.WindowID, mux.PaneID, int, error) {
	var out struct {
		ID      string `json:"session_id"`
		Windows []struct {
			Window string   `json:"window_id"`
			Blocks []string `json:"block_ids"`
		} `json:"initial_windows"`
	}
	if err := call("session.create", map[string]any{"label": cut(label, 128), "initial_windows": []any{
		map[string]any{"window_label": cut(w.Label, 128), "layout": layout(w.Spawn, "")},
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

func (f Fleet) SplitLeft(of mux.PaneID, s mux.NewSplit) (mux.PaneID, int, error) {
	return f.of(string(of)).SplitLeft(of, s)
}

func (f Fleet) BreakOut(p mux.PaneID, label string, at int, background bool) (mux.WindowID, int, error) {
	return f.of(string(p)).BreakOut(p, label, at, background)
}

func (f Fleet) Respawn(p mux.PaneID, s mux.Spawn) error { return f.of(string(p)).Respawn(p, s) }
func (f Fleet) Close(p mux.PaneID)                      { f.of(string(p)).Close(p) }
func (f Fleet) CloseWindow(w mux.WindowID)              { f.of(string(w)).CloseWindow(w) }
func (f Fleet) Place(w mux.WindowID, at int)            { f.of(string(w)).Place(w, at) }
func (f Fleet) Label(w mux.WindowID, label string)      { f.of(string(w)).Label(w, label) }
func (f Fleet) Title(p mux.PaneID, title string)        { f.of(string(p)).Title(p, title) }
func (f Fleet) SetWidth(p mux.PaneID, cols int)         { f.of(string(p)).SetWidth(p, cols) }
func (f Fleet) Moved(p mux.PaneID) bool                 { return f.of(string(p)).Moved(p) }
func (f Fleet) Zoom(p mux.PaneID)                       { f.of(string(p)).Zoom(p) }
func (f Fleet) Show(w mux.WindowID)                     { f.of(string(w)).Show(w) }
func (f Fleet) Focus(p mux.PaneID)                      { f.of(string(p)).Focus(p) }
func (f Fleet) Say(ms int, text string)                 { f.here().Say(ms, text) }

// SayToSession says text in home, to everyone looking at it.
func (f Fleet) SayToSession(ms int, text string) { f.Home.Say(ms, text) }

func (f Fleet) Popup(size func(cols, lines int) (int, int), border bool, dir string, argv []string) (*exec.Cmd, bool) {
	return f.here().Popup(size, border, dir, argv)
}
