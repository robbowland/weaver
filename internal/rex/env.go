package rex

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/process"
)

// Here is where weaver runs, as Rex tells it ($REX_SESSION, $REX_BLOCK):
// the session, weaver's name for it, the pane and the session's life;
// outside Rex, nothing.
type Here struct {
	Inside bool
	ID     string
	Name   string
	Pane   mux.PaneID
	Where  agent.Where
}

// Chosen: does weaver act on Rex here? When $WEAVER_MUX says rex, or in a
// session weaver made on Rex; never in a tmux pane ($TMUX), nor when
// $WEAVER_MUX says tmux.
func Chosen(dir string) bool {
	if os.Getenv("TMUX") != "" {
		return false
	}
	switch os.Getenv("WEAVER_MUX") {
	case "rex":
		return true
	case "tmux":
		return false
	}
	_, ok := ids{dir}.session(os.Getenv("REX_SESSION"))
	return ok && os.Getenv("REX_SESSION") != ""
}

// FromEnv is where weaver runs on Rex, its numbers kept in dir. A session
// weaver did not make is called by its id.
func FromEnv(dir string) Here {
	sid := os.Getenv("REX_SESSION")
	if sid == "" {
		return Here{}
	}
	r := ids{dir}
	h := Here{Inside: true, ID: sid, Where: agent.Where{Server: serverPID(), Session: sid}}
	if m, ok := r.session(sid); ok {
		h.Name = m.name
	} else {
		h.Name = strings.TrimPrefix(sid, "session:")
	}
	if b := os.Getenv("REX_BLOCK"); b != "" {
		if n, err := r.num(b); err == nil {
			h.Pane = mux.PaneID(n)
		}
	}
	return h
}

// Answer is what the Rex server $REX_SERVER names says of its sessions,
// each thing asked once, when first needed: its pid, then the sessions it
// runs. Every file one pass looks at (a gc run, the board's rows) is
// judged by one Answer. Its zero value has asked nothing yet.
type Answer struct {
	pidAsked, listAsked bool
	pid                 int
	live                map[string]bool // nil: not answered
}

func (a *Answer) serverPID() int {
	if !a.pidAsked {
		a.pid, a.pidAsked = serverPID(), true
	}
	return a.pid
}

func (a *Answer) sessions() map[string]bool {
	if !a.listAsked {
		a.live, _ = sessions()
		a.listAsked = true
	}
	return a.live
}

// Lost: has the session life w gone, and does Rex say? Gone when its
// server's process has ended (no call), when the server running is
// another, or when the session is no longer on it. Not known when Rex does
// not answer what that takes, whichever call fails: never gone for that.
// A life with no session: not lost.
func (a *Answer) Lost(w agent.Where) (lost, known bool) {
	switch {
	case w.Session == "":
		return false, true
	case w.Server > 1 && !process.Alive(w.Server):
		return true, true
	}
	pid := a.serverPID()
	switch {
	case pid == 0:
		return false, false
	case w.Server != 0 && pid != w.Server:
		return true, true
	}
	live := a.sessions()
	if live == nil {
		return false, false
	}
	return !live[w.Session], true
}

// Keyed is the session whose files are keyed skey (Key of its name): one
// weaver made of a name with that key, else the one whose id the key holds
// (a session weaver did not make is named by its id); whether it runs, and
// whether Rex answered at all. By key, not by a name taken back out of
// it: Key turns a name's / into _.
func (a *Answer) Keyed(dir, skey string) (s Session, running, answered bool) {
	live := a.sessions()
	if live == nil {
		return Session{}, false, false
	}
	id, name, ok := ids{dir}.keyed(skey)
	if !ok {
		name = strings.TrimPrefix(skey, "rex-")
		id = "session:" + name
	}
	if live[id] {
		return Session{ID: id, Name: name, Dir: dir}, true, true
	}
	return Session{}, false, true
}

// sessions are the ids of the sessions Rex runs.
func sessions() (map[string]bool, error) {
	var l struct {
		Sessions []struct {
			ID string `json:"session_id"`
		} `json:"sessions"`
	}
	if err := call("session.list", map[string]any{}, &l); err != nil {
		return nil, err
	}
	live := map[string]bool{}
	for _, x := range l.Sessions {
		live[x.ID] = true
	}
	return live, nil
}

// Named is the Rex session weaver made last of name, and whether it runs:
// false when weaver made none. An error when Rex does not answer: not
// known to run, nor known not to.
func Named(dir, name string) (Session, bool, error) {
	id, ok := ids{dir}.named(name)
	if !ok {
		return Session{}, false, nil
	}
	live, err := sessions()
	if err != nil {
		return Session{}, false, err
	}
	return Session{ID: id, Name: name, Dir: dir}, live[id], nil
}

// Ensure is weaver's Rex session name, running: the one weaver made, else
// one made now (prepare, then Start). One Rex restored after its server
// restarted (Restored) is made again, and the old one given back, a husk
// for the caller to end once the new one shows (the caller may be running
// in it). One weaver at a time asks and makes, so two started at once
// make one session; none is made while Rex does not answer whether one
// runs.
func Ensure(dir, name, here string, home []string, prepare func() error) (s Session, husk *Session, err error) {
	unlock, err := ids{dir}.lockOn("start")
	if err != nil {
		return Session{}, nil, err
	}
	defer unlock()
	s, running, err := Named(dir, name)
	switch {
	case err != nil:
		return Session{}, nil, err
	case running && !s.Restored():
		return s, nil, nil
	case running:
		old := s
		husk = &old
	}
	if err := prepare(); err != nil {
		return Session{}, nil, err
	}
	if s, err = Start(dir, name, here, home); err != nil {
		return s, nil, err
	}
	return s, husk, nil
}

// Start makes weaver's Rex session name, home (argv, in dir here) its
// first window; it is weaver's from then on (FromEnv, Named). Ensure is
// how wvr starts it: Start makes one whether or not one runs.
func Start(dir, name, here string, home []string) (Session, error) {
	var out struct {
		ID      string `json:"session_id"`
		Windows []struct {
			Window string `json:"window_id"`
		} `json:"initial_windows"`
	}
	if err := call("session.create", map[string]any{"label": cut(name, 128), "initial_windows": []any{
		map[string]any{"window_label": mux.HomeName, "layout": layout(mux.Spawn{Argv: home, Dir: here}, "")},
	}}, &out); err != nil {
		return Session{}, err
	}
	if out.ID == "" || len(out.Windows) == 0 {
		return Session{}, errors.New("rex: no session made")
	}
	s := Session{ID: out.ID, Name: name, Dir: dir}
	if err := s.ids().made(out.ID, name, time.Now(), serverPID()); err != nil {
		return s, err
	}
	if n, err := s.ids().num(out.Windows[0].Window); err == nil {
		_ = s.ids().note("want", n, "0")
	}
	return s, nil
}

// ShowInApp asks the Rex app to show the session: it can only when Remote
// Control is on in its settings.
func (s Session) ShowInApp() error { return s.showInApp("") }
