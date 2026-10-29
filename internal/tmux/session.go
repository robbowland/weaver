package tmux

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/mux"
)

// Session is the tmux session Name as weaver acts on it (mux.Mux): on the
// server of the tmux client weaver runs in (Inside), else on the server
// Sock names (-L name, or -S path).
type Session struct {
	Name   string
	Sock   []string
	Inside bool
}

var _ mux.Mux = Session{}

// Run runs tmux on the session's server with args as they are: its output.
func (s Session) Run(args ...string) ([]byte, error) {
	return s.cmd(context.Background(), args...).Output()
}

func (s Session) cmd(ctx context.Context, args ...string) *exec.Cmd {
	if !s.Inside {
		args = append(append([]string{}, s.Sock...), args...)
	}
	return exec.CommandContext(ctx, "tmux", args...)
}

// at is the session's window at index i, or (i < 0) its next free index.
func (s Session) at(i int) string {
	if i < 0 {
		return "=" + s.Name + ":"
	}
	return "=" + s.Name + ":" + strconv.Itoa(i)
}

// Life is the session's server pid and id, as $TMUX says them.
func (s Session) Life() agent.Where {
	out, err := s.Run("display-message", "-p", "-t", s.at(-1), "#{pid} #{session_id}")
	srv, ses, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
	n, _ := strconv.Atoi(srv)
	if err != nil || n <= 0 || !strings.HasPrefix(ses, "$") {
		return agent.Where{}
	}
	return agent.Where{Server: n, Session: ses}
}

func (s Session) Made() (int64, bool) {
	out, err := s.Run("display-message", "-p", "-t", s.at(-1), "#{session_created}")
	n, perr := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	return n, err == nil && perr == nil
}

func (s Session) Running() bool {
	_, err := s.Run("has-session", "-t", "="+s.Name)
	return err == nil
}

func (s Session) Quit() error {
	_, err := s.Run("kill-session", "-t", "="+s.Name)
	return err
}

func (s Session) Panes() ([]mux.Pane, error) {
	out, err := s.Run("list-panes", "-s", "-t", "="+s.Name, "-F", PanesFormat)
	return ParsePanes(out), err
}

func (s Session) Windows() ([]mux.Window, error) {
	out, err := s.Run("list-windows", "-t", "="+s.Name, "-F",
		strings.Join([]string{"#{window_id}", "#{window_index}", "#{window_active}", "#{@weaver-view}", "#{window_width}", "#{window_name}"}, us))
	if err != nil {
		return nil, err
	}
	var ws []mux.Window
	for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		f := strings.SplitN(l, us, 6)
		if len(f) != 6 {
			continue
		}
		id, ok := ParseWindow(f[0])
		i, err := strconv.Atoi(f[1])
		if !ok || err != nil {
			continue
		}
		w, _ := strconv.Atoi(f[4])
		ws = append(ws, mux.Window{ID: id, Index: i, Active: f[2] == "1", View: f[3], Width: w, Name: f[5]})
	}
	return ws, nil
}

// Looking are the panes tmux's clients show, every client of the server.
func (s Session) Looking() ([]mux.PaneID, error) {
	out, err := s.Run("list-clients", "-F", "#{pane_id}")
	if err != nil {
		return nil, err
	}
	var ps []mux.PaneID
	for _, f := range strings.Fields(string(out)) {
		if p, ok := ParsePane(f); ok {
			ps = append(ps, p)
		}
	}
	return ps, nil
}

func (s Session) Active() (mux.PaneID, bool) {
	out, err := s.Run("display-message", "-p", "-t", s.at(-1), "#{pane_id}")
	if err != nil {
		return "", false
	}
	return ParsePane(string(out))
}

func (s Session) PID(p mux.PaneID) (int, bool) {
	out, err := s.Run("display-message", "-p", "-t", p.Target(), "#{pane_pid}")
	n, perr := strconv.Atoi(strings.TrimSpace(string(out)))
	return n, err == nil && perr == nil
}

func (s Session) Screen(p mux.PaneID) []byte {
	out, _ := s.Run("capture-pane", "-p", "-e", "-t", p.Target())
	return out
}

// spawn is sp as new-window, split-window and respawn-pane take it: its
// directory, the session's name and sp's environment, then its argv.
func (s Session) spawn(sp mux.Spawn) []string {
	var args []string
	if sp.Dir != "" {
		args = append(args, "-c", Text(sp.Dir))
	}
	args = append(args, "-e", "WEAVER_TMUX_SESSION="+s.Name)
	for _, v := range sp.Env {
		args = append(args, "-e", v)
	}
	return append(args, sp.Argv...)
}

func (s Session) OpenWindow(w mux.NewWindow) (mux.WindowID, mux.PaneID, int, error) {
	args := []string{"new-window"}
	if w.Background {
		args = append(args, "-d")
	}
	args = append(args, "-P", "-F", "#{window_id} #{pane_id} #{window_width}", "-t", s.at(w.At), "-n", Text(w.Label))
	out, err := s.Run(append(args, s.spawn(w.Spawn)...)...)
	f := strings.Fields(string(out))
	if err != nil || len(f) < 3 {
		return "", "", 0, errors.New("tmux could not open a window")
	}
	id, _ := ParseWindow(f[0])
	p, _ := ParsePane(f[1])
	width, _ := strconv.Atoi(f[2])
	if w.View != "" {
		s.Run("set-option", "-w", "-t", id.Target(), "@weaver-view", w.View)
	}
	return id, p, width, nil
}

func (s Session) SplitLeft(of mux.PaneID, n mux.NewSplit) (mux.PaneID, int, error) {
	args := []string{"split-window"}
	if n.Background {
		args = append(args, "-d")
	}
	cols := "80%"
	if n.Cols > 0 {
		cols = strconv.Itoa(n.Cols)
	}
	args = append(args, "-h", "-b", "-P", "-F", "#{pane_id} #{pane_pid}", "-t", of.Target(), "-l", cols)
	out, err := s.Run(append(args, s.spawn(n.Spawn)...)...)
	f := strings.Fields(string(out))
	if err != nil || len(f) < 2 {
		return "", 0, errors.New("tmux split-window failed")
	}
	p, _ := ParsePane(f[0])
	pid, _ := strconv.Atoi(f[1])
	if n.Title != "" {
		s.Title(p, n.Title)
	}
	return p, pid, nil
}

func (s Session) BreakOut(p mux.PaneID, label string, at int, focus bool) (mux.WindowID, error) {
	args := []string{"break-pane", "-P", "-F", "#{window_id}", "-s", p.Target(), "-n", Text(label)}
	if !focus {
		args = append(args, "-d")
	}
	if at >= 0 {
		args = append(args, "-t", s.at(at))
	}
	out, err := s.Run(args...)
	if err != nil {
		return "", err
	}
	w, _ := ParseWindow(string(out))
	return w, nil
}

func (s Session) Respawn(p mux.PaneID, sp mux.Spawn) error {
	_, err := s.Run(append([]string{"respawn-pane", "-k", "-t", p.Target()}, s.spawn(sp)...)...)
	return err
}

func (s Session) KeepExited(p mux.PaneID, what, hint string) {
	t := p.Target()
	s.Run("set-option", "-p", "-t", t, "remain-on-exit", "on", ";",
		"set-option", "-p", "-t", t, "remain-on-exit-format", Text(what)+
			"#{?pane_dead_signal, on signal #{pane_dead_signal},#{?#{!=:#{pane_dead_status},0}, with status #{pane_dead_status},}}"+
			" · "+Text(hint))
}

func (s Session) Close(p mux.PaneID)         { s.Run("kill-pane", "-t", p.Target()) }
func (s Session) CloseWindow(w mux.WindowID) { s.Run("kill-window", "-t", w.Target()) }

func (s Session) Place(w mux.WindowID, at int) {
	s.Run("move-window", "-d", "-s", w.Target(), "-t", s.at(at))
}

func (s Session) Label(w mux.WindowID, label string) {
	s.Run("rename-window", "-t", w.Target(), Text(label))
}

// Title titles pane p, waiting at most 2 s: a title, nothing more.
func (s Session) Title(p mux.PaneID, title string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s.cmd(ctx, "select-pane", "-t", p.Target(), "-T", Text(title)).Run()
}

func (s Session) SetWidth(p mux.PaneID, cols int) {
	s.Run("resize-pane", "-t", p.Target(), "-x", strconv.Itoa(cols))
}

func (s Session) Zoom(p mux.PaneID)   { s.Run("resize-pane", "-Z", "-t", p.Target()) }
func (s Session) Show(w mux.WindowID) { s.Run("select-window", "-t", w.Target()) }
func (s Session) Focus(p mux.PaneID) {
	s.Run("select-window", "-t", p.Target(), ";", "select-pane", "-t", p.Target())
}
func (s Session) Say(ms int, text string) {
	s.Run("display-message", "-d", strconv.Itoa(ms), Text(text))
}

func (s Session) SayToSession(ms int, text string) {
	s.Run("display-message", "-d", strconv.Itoa(ms), "-t", s.at(-1), Text(text))
}

// Type pastes text bracketed (paste-buffer -p), so its newlines do not
// submit, and presses Enter once the paste has landed.
func (s Session) Type(p mux.PaneID, text string) error {
	c := s.cmd(context.Background(), "load-buffer", "-b", "weaver-send", "-")
	c.Stdin = strings.NewReader(text)
	if c.Run() != nil {
		return errors.New("tmux would not take the text")
	}
	if _, err := s.Run("paste-buffer", "-p", "-d", "-b", "weaver-send", "-t", p.Target()); err != nil {
		return errors.New("tmux would not paste into pane " + string(p))
	}
	time.Sleep(200 * time.Millisecond)
	s.Run("send-keys", "-t", p.Target(), "Enter")
	return nil
}

// MarkUnseen flags the window (@weaver-unseen) for config/tmux.conf's
// pane-focus-in hook, which marks its agent seen when you look.
func (s Session) MarkUnseen(p mux.PaneID, on bool) {
	if on {
		s.Run("set-option", "-w", "-t", p.Target(), "@weaver-unseen", "1")
	} else {
		s.Run("set-option", "-w", "-u", "-t", p.Target(), "@weaver-unseen")
	}
}

// Publish sets the session's @weaver-counts, which its status line shows
// (an option set redraws every client), and @weaver-busy, which Ctrl q
// reads to ask first while a turn is under way.
func (s Session) Publish(need, done, work int) {
	s.Run("set-option", "-t", s.at(-1), "@weaver-counts", Counts(need, done, work))
	s.Run("set-option", "-t", s.at(-1), "@weaver-busy", strconv.Itoa(need+work))
}

// Counts is the status line's counts in tmux's colours: needs you, done,
// working, each only when there are some.
func Counts(n, d, w int) string {
	// Each a button (tmux's user ranges, which `wvr _click status` reads):
	// to the agent that needs you, or home.
	out := ""
	if n > 0 {
		out += "#[range=user|next]#[fg=#616161][#[fg=#ff3b2f]!#[fg=#616161]]#[fg=#999999] " + strconv.Itoa(n) + "#[norange]  "
	}
	if d > 0 {
		out += "#[range=user|next]#[fg=#616161][#[fg=#39d97a]x#[fg=#616161]]#[fg=#999999] " + strconv.Itoa(d) + "#[norange]  "
	}
	if w > 0 {
		out += "#[range=user|home]#[fg=#616161][#[fg=#999999]/#[fg=#616161]]#[fg=#999999] " + strconv.Itoa(w) + "#[norange]  "
	}
	return out
}

// Strip sets the session's @weaver-strip, the status line's left on home
// and in a view: each item, the one you are on in ink, one not set up yet
// with a faint dot; each a button (a tmux user range, which `wvr _click
// status` reads).
func (s Session) Strip(items []mux.StripItem) {
	var parts []string
	for _, it := range items {
		var p string
		if it.View == "" {
			p = "#[range=user|home]#{?#{==:#{window_index},0},#[fg=#ffffff],#[fg=#616161]}" + Text(it.Label) + "#[norange]"
		} else {
			p = "#[range=user|" + it.View + "]#{?#{==:#{@weaver-view}," + it.View + "},#[fg=#ffffff],#[fg=#616161]}" + Text(it.Label) + "#[norange]"
		}
		if !it.Ready {
			p += " #[fg=#616161]·"
		}
		parts = append(parts, p)
	}
	s.Run("set-option", "-t", s.at(-1), "@weaver-strip", strings.Join(parts, "   "))
}

// Popup is display-popup on the client that looked at the session last,
// at its size; tmux's command returns when the popup closes.
func (s Session) Popup(size func(cols, lines int) (int, int), border bool, dir string, argv []string) (*exec.Cmd, bool) {
	args := []string{"display-popup"}
	cw, ch := 0, 0
	client := false
	if out, err := s.Run("list-clients", "-t", "="+s.Name, "-F", "#{client_activity} #{client_name} #{client_width} #{client_height}"); err == nil {
		at := ""
		best := []string(nil)
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if f := strings.Fields(l); len(f) == 4 && f[0] >= at {
				best, at = f, f[0]
			}
		}
		if best != nil {
			args = append(args, "-c", Text(best[1]))
			cw, _ = strconv.Atoi(best[2])
			ch, _ = strconv.Atoi(best[3])
			client = true
		}
	}
	w, h := size(cw, ch)
	args = append(args, "-E", "-w", strconv.Itoa(w), "-h", strconv.Itoa(h))
	if !border {
		args = append(args, "-B")
	}
	if dir != "" {
		args = append(args, "-d", Text(dir))
	}
	args = append(append(args, "--"), argv...)
	return s.cmd(context.Background(), args...), client
}

// Here is where weaver runs, as tmux tells it: in a tmux client's pane
// ($TMUX), the session, the pane and the session's life; outside tmux,
// nothing.
type Here struct {
	Inside  bool
	Session string
	Pane    mux.PaneID
	Where   agent.Where
}

// FromEnv is where weaver runs, from tmux's environment: the session from
// $WEAVER_TMUX_SESSION, which weaver's sessions set for their panes, else
// asked of tmux.
func FromEnv() Here {
	h := Here{Inside: os.Getenv("TMUX") != "", Where: agent.ParseWhere(os.Getenv("TMUX"))}
	h.Pane, _ = ParsePane(os.Getenv("TMUX_PANE"))
	if !h.Inside {
		return h
	}
	h.Session = os.Getenv("WEAVER_TMUX_SESSION")
	if h.Session == "" {
		args := []string{"display-message", "-p"}
		if p := os.Getenv("TMUX_PANE"); p != "" {
			args = append(args, "-t", p)
		}
		out, _ := exec.Command("tmux", append(args, "#S")...).Output()
		h.Session = strings.TrimRight(string(out), "\n")
	}
	return h
}
