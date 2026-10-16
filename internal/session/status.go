package session

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/git"
	"github.com/robbowland/weaver/internal/oneline"
	"github.com/robbowland/weaver/internal/store"
	"github.com/robbowland/weaver/internal/tmux"
)

// CountsFmt is the status line's counts in tmux's colours: needs you, done,
// working, each only when there are some.
func CountsFmt(n, d, w int) string {
	out := ""
	if n > 0 {
		out += "#[fg=#616161][#[fg=#ff3b2f]!#[fg=#616161]]#[fg=#999999] " + strconv.Itoa(n) + "  "
	}
	if d > 0 {
		out += "#[fg=#616161][#[fg=#39d97a]x#[fg=#616161]]#[fg=#999999] " + strconv.Itoa(d) + "  "
	}
	if w > 0 {
		out += "#[fg=#616161][#[fg=#999999]/#[fg=#616161]]#[fg=#999999] " + strconv.Itoa(w) + "  "
	}
	return out
}

// StatusLine is `wvr status --tmux SESSION`: the counts PushStatus last
// wrote for the session, in the status line's colours.
func StatusLine(args []string) string {
	k := ""
	if len(args) > 1 && args[1] != "" {
		k = "tmux-" + args[1]
	}
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home := os.Getenv("HOME")
		if home == "" {
			home = "/nonexistent"
		}
		state = home + "/.local/state"
	}
	c := [3]int{}
	if k != "" {
		if l, err := files.FirstLine(state + "/weaver/" + strings.ReplaceAll(k, "/", "_") + ".status"); err == nil {
			f := strings.Fields(l)
			ok := len(f) >= 3
			for i := 0; ok && i < 3; i++ {
				ok = digits(f[i])
			}
			for i := 0; ok && i < 3; i++ {
				c[i], _ = strconv.Atoi(f[i])
			}
		}
	}
	return CountsFmt(c[0], c[1], c[2])
}

// StatusText is plain `wvr status`: the session's counts as cells,
// `[!] 1  [x] 2  [/] 1`, each only when there are some (done counts the
// ones not yet seen).
func (e *Env) StatusText() string {
	rows, _ := e.AgentRows()
	var n, d, w int
	for _, r := range rows {
		if r.Pane == "" {
			continue
		}
		switch r.State {
		case agent.StateNeedsYou:
			n++
		case agent.StateWorking, agent.StateStarting:
			w++
		case agent.StateDone:
			if !r.Seen {
				d++
			}
		case agent.StateIdle, agent.StateInterrupted, agent.StateEnded:
			// Not counted.
		}
	}
	var out []string
	for _, c := range []struct {
		g string
		k int
	}{{agent.CellNeedsYou, n}, {agent.CellDone, d}, {agent.CellWorking, w}} {
		if c.k > 0 {
			out = append(out, c.g+" "+strconv.Itoa(c.k))
		}
	}
	return strings.Join(out, "  ")
}

// PushStatus brings what shows the session's agents up to date: agents
// done that a client looks at count as seen, the counts go to the status
// file and tmux's status line, and every dashboard reloads.
func (e *Env) PushStatus() { e.pushStatus(false) }

// PushStatusAndLabels is PushStatus, and in the weaver session the threads'
// windows labelled again (a title, task or root changed).
func (e *Env) PushStatusAndLabels() { e.pushStatus(true) }

func (e *Env) pushStatus(labels bool) {
	clients := " "
	if out, err := e.Tmux("list-clients", "-F", "#{pane_id}"); err == nil {
		clients = " " + strings.ReplaceAll(strings.ReplaceAll(strings.TrimRight(string(out), "\n"), "%", ""), "\n", " ") + " "
	}
	rows, _ := e.AgentRows()
	var need, done, work int
	var seenNow []tmux.PaneID
	for _, r := range rows {
		if r.Pane == "" {
			continue
		}
		switch r.State {
		case agent.StateNeedsYou:
			need++
		case agent.StateWorking, agent.StateStarting:
			work++
		case agent.StateDone:
			if r.Seen {
				continue
			}
			if strings.Contains(clients, " "+string(r.Pane)+" ") {
				seenNow = append(seenNow, r.Pane)
			} else {
				done++
			}
		case agent.StateIdle, agent.StateInterrupted, agent.StateEnded:
			// Not counted.
		}
	}
	for _, p := range seenNow {
		e.MarkSeen(p)
	}
	sf := e.StateDir + "/" + e.SKey + ".status"
	old := ""
	if l, err := files.FirstLine(sf); err == nil {
		if f := strings.Fields(l); len(f) >= 3 {
			old = strings.Join(f[:3], " ")
		}
	}
	now := strconv.Itoa(need) + " " + strconv.Itoa(done) + " " + strconv.Itoa(work)
	bestEffort(store.WriteAtomic(sf, []byte(now+"\n"))) // the counts again next time
	// tmux's status line shows the session's @weaver-counts, and an option
	// set redraws every client: set them when they change.
	if labels || now != old {
		e.Tmux("set-option", "-t", "="+e.Session+":", "@weaver-counts", CountsFmt(need, done, work))
		// Ctrl q asks first while a turn is under way (0: it quits at once).
		e.Tmux("set-option", "-t", "="+e.Session+":", "@weaver-busy", strconv.Itoa(need+work))
	}
	q := Quote(e.Self)
	socks, _ := filepath.Glob(e.SockDir + "/" + e.SKey + ".fzf.*.sock")
	for _, s := range socks {
		if st, err := os.Stat(s); err != nil || st.Mode()&os.ModeSocket == 0 {
			continue
		}
		if !FzfPost(s, "reload(COLOR=1 "+q+" _rows)+refresh-preview") {
			b := strings.TrimSuffix(s, ".sock")
			for _, x := range []string{s, b + ".keys", b + ".view", b + ".title", b + ".label", b + ".room", b + ".shown", b + ".tight", b + ".vp"} {
				os.Remove(x)
			}
		}
	}
	// The views (GITHUB, LINEAR) show the threads of their items too:
	// <skey>.view.NAME.PID.sock.
	views, _ := filepath.Glob(e.SockDir + "/" + e.SKey + ".view.*.*.sock")
	for _, s := range views {
		name, _, _ := strings.Cut(strings.TrimPrefix(filepath.Base(s), e.SKey+".view."), ".")
		if !FzfPost(s, "reload("+q+" _items "+name+" rows)+refresh-preview") {
			os.Remove(s)
		}
	}
	if labels && e.WeaverSession() {
		e.threadTabLabels(rows)
	}
}

// FzfPost sends actions to the fzf listening on unix socket s.
func FzfPost(s, actions string) bool {
	c := &http.Client{Timeout: time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", s)
		}}}
	r, err := c.Post("http://localhost", "text/plain", strings.NewReader(actions))
	if err != nil {
		return false
	}
	_ = r.Body.Close() // fzf has the actions
	return true
}

// MarkSeen marks agent p seen (you looked at it): a StateDone no longer stands
// out.
func (e *Env) MarkSeen(p tmux.PaneID) {
	sf := e.StateFile(p)
	if !files.Exists(sf) {
		return
	}
	store.UpdateState(sf, func(st *agent.Record) { st.Seen = true })
}

// threadTabLabels: in the weaver session a thread's window is its line on
// the status bar, "<num> <name> · <repo>/<branch>"; renamed only when that
// is not its name already.
func (e *Env) threadTabLabels(rows []Row) {
	panes, _ := e.Panes()
	names := map[string]bool{}
	for _, p := range panes {
		n := strings.NewReplacer("\t", " ", "\n", " ").Replace(p.TabName)
		names[string(p.Tab)+"\t"+n] = true
	}
	for _, r := range rows {
		if r.Pane == "" || r.Tnum == 0 {
			continue
		}
		l := r.Title
		if l == "" {
			l = r.Ttask
		}
		if l == "" {
			l = r.Prompt
		}
		label := ThreadLabel(r.Tnum, l, r.Root)
		if names[string(r.Tab)+"\t"+label] {
			continue
		}
		e.Tmux("rename-window", "-t", r.Tab.Target(), label)
	}
}

// ThreadLabel is a thread window's name in the weaver session: its number,
// what it is about (the first line of text, fitted), and where it works.
func ThreadLabel(n int, text, root string) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	w := git.WhereOf(root)
	label := padNum(n) + " " + strings.TrimRight(oneline.Fit(text, 30), " ") + " · " + w.Repo
	if w.Branch != "" {
		label += "/" + w.Branch
	}
	return label
}

func padNum(n int) string {
	s := strconv.Itoa(n)
	if len(s) < 2 {
		s = "0" + s
	}
	return s
}

// Quote is a word for sh, as bash's printf %q would make it usable.
func Quote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789/._-+=:,@%") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
