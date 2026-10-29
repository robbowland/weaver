package session

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/git"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/oneline"
	"github.com/robbowland/weaver/internal/store"
	"github.com/robbowland/weaver/internal/tmux"
)

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
	return tmux.Counts(c[0], c[1], c[2])
}

// StatusText is plain `wvr status`: the session's counts as cells,
// `[!] 1  [x] 2  [/] 1`, each only when there are some (done counts the
// ones not yet seen).
func (e *Env) StatusText() string {
	rows, now := e.AgentRows()
	var n, d, w int
	for _, r := range rows {
		if r.Pane == "" {
			continue
		}
		switch r.Need(now) {
		case NeedYou:
			n++
		case NeedLook:
			d++
		case NeedWait:
			w++
		case NeedNothing:
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
	looking, _ := e.Mux().Looking()
	rows, at := e.AgentRows()
	var need, done, work int
	var seenNow []mux.PaneID
	for _, r := range rows {
		if r.Pane == "" {
			continue
		}
		switch r.Need(at) {
		case NeedYou:
			need++
		case NeedLook:
			// Done while you look at it (its pane or its nvim): seen at once.
			if slices.Contains(looking, r.Pane) ||
				slices.Contains(looking, e.NvimPane(r.Pane)) && e.NvimPane(r.Pane) != "" {
				seenNow = append(seenNow, r.Pane)
			} else {
				done++
			}
		case NeedWait:
			work++
		case NeedNothing:
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
	// Shown, they redraw every client (tmux's status line): set them when
	// they change.
	if labels || now != old {
		e.Mux().Publish(need, done, work)
	}
	q := Quote(e.Self)
	socks, _ := filepath.Glob(e.SockDir + "/" + e.SKey + ".fzf.*.sock")
	for _, s := range socks {
		if st, err := os.Stat(s); err != nil || st.Mode()&os.ModeSocket == 0 {
			continue
		}
		// A refresh, not a reload: one reloads only rows that changed, and
		// leaves the card where you scrolled it otherwise.
		if !FzfPost(s, "transform(COLOR=1 "+q+" _refresh)") {
			for _, x := range BoardFiles(strings.TrimSuffix(s, ".sock")) {
				os.Remove(x)
			}
		}
	}
	// The views (GITHUB, LINEAR) show the threads of their items too:
	// <skey>.view.NAME.PID.sock.
	views, _ := filepath.Glob(e.SockDir + "/" + e.SKey + ".view.*.*.sock")
	for _, s := range views {
		name, _, _ := strings.Cut(strings.TrimPrefix(filepath.Base(s), e.SKey+".view."), ".")
		if !FzfPost(s, "transform("+q+" _items "+name+" refresh)") {
			os.Remove(s)
			os.Remove(s + ".rows")
		}
	}
	if labels && e.WeaverSession() {
		e.threadTabLabels(rows)
	}
}

// BoardFiles are a board's socket and the files beside it, by their stem
// base: they go with it.
func BoardFiles(base string) []string {
	return []string{base + ".sock", base + ".keys", base + ".view", base + ".title", base + ".label", base + ".room",
		base + ".shown", base + ".tight", base + ".vp", base + ".rows"}
}

// FzfPost sends actions to the fzf listening on unix socket s.
func FzfPost(s, actions string) bool {
	r, err := fzfClient(s).Post("http://localhost", "text/plain", strings.NewReader(actions))
	if err != nil {
		return false
	}
	_ = r.Body.Close() // fzf has the actions
	return true
}

// FzfQuery is the query of the fzf listening on socket s, as typed now;
// false when it does not answer.
func FzfQuery(s string) (string, bool) {
	r, err := fzfClient(s).Get("http://localhost")
	if err != nil {
		return "", false
	}
	defer func() { _ = r.Body.Close() }()
	var st struct {
		Query string `json:"query"`
	}
	if json.NewDecoder(r.Body).Decode(&st) != nil {
		return "", false
	}
	return st.Query, true
}

// fzfClient talks to the fzf listening on socket s, within a second.
func fzfClient(s string) *http.Client {
	return &http.Client{Timeout: time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", s)
		}}}
}

// MarkSeen marks agent p seen (you looked at it): a StateDone no longer stands
// out.
func (e *Env) MarkSeen(p mux.PaneID) {
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
		e.Mux().Label(r.Tab, label)
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
