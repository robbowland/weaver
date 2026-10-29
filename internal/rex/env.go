package rex

import (
	"errors"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/mux"
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
	_, _, ok := ids{dir}.session(os.Getenv("REX_SESSION"))
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
	if name, _, ok := r.session(sid); ok {
		h.Name = name
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

// Lost: has the session life w gone, and does Rex say? Gone when its
// server's process has ended (no call), when the server running is
// another, or when the session is no longer on it. Not known when Rex does
// not answer what that takes, whichever call fails: never gone for that.
// A life with no session: not lost.
func Lost(w agent.Where) (lost, known bool) {
	switch {
	case w.Session == "":
		return false, true
	case w.Server > 1 && syscall.Kill(w.Server, 0) != nil:
		return true, true
	}
	pid := serverPID()
	switch {
	case pid == 0:
		return false, false
	case w.Server != 0 && pid != w.Server:
		return true, true
	}
	live, err := sessions()
	if err != nil {
		return false, false
	}
	return !live[w.Session], true
}

// Alive is the session whose files are keyed skey (Key of its name): one
// weaver made of a name with that key, else the one whose id the key holds
// (a session weaver did not make is named by its id); whether it runs, and
// whether Rex answered at all. By key, not by a name taken back out of
// it: Key turns a name's / into _.
func Alive(dir, skey string) (s Session, running, answered bool) {
	live, err := sessions()
	if err != nil {
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

// Named is weaver's Rex session name, when it runs.
func Named(dir, name string) (Session, bool) {
	id, ok := ids{dir}.named(name)
	if !ok {
		return Session{}, false
	}
	s := Session{ID: id, Name: name, Dir: dir}
	return s, s.Running()
}

// Start makes weaver's Rex session name, home (argv, in dir here) its
// first window; it is weaver's from then on (FromEnv, Named).
func Start(dir, name, here string, home []string) (Session, error) {
	var out struct {
		ID      string `json:"session_id"`
		Windows []struct {
			Window string `json:"window_id"`
		} `json:"initial_windows"`
	}
	if err := call("session.create", map[string]any{"label": cut(name, 128), "initial_windows": []any{
		map[string]any{"window_label": "home", "layout": layout(mux.Spawn{Argv: home, Dir: here}, "")},
	}}, &out); err != nil {
		return Session{}, err
	}
	if out.ID == "" || len(out.Windows) == 0 {
		return Session{}, errors.New("rex: no session made")
	}
	s := Session{ID: out.ID, Name: name, Dir: dir}
	if err := s.ids().made(out.ID, name, time.Now().Unix()); err != nil {
		return s, err
	}
	if n, err := s.ids().num(out.Windows[0].Window); err == nil {
		_ = s.ids().note("want", n, "0")
	}
	return s, nil
}

// ShowInApp asks the Rex app to show the session: it can only when Remote
// Control is on in its settings.
func (s Session) ShowInApp() error {
	_, err := run("do", "session.select", "session_id="+s.ID)
	return err
}
