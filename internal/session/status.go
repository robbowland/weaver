package session

import (
	"context"
	"encoding/json"
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
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/oneline"
	"github.com/robbowland/weaver/internal/store"
)

// StatusText is plain `wvr status`: the session's counts as cells,
// `[!] 1  [x] 2  [/] 1`, each only when there are some (done counts the
// ones not yet seen).
func (e *Env) StatusText() string {
	rows, now := e.AgentRows()
	var n, d, w int
	for _, r := range rows {
		if r.Pane == "" || r.Helper() {
			continue
		}
		switch r.Shown().Need(now) {
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

// PushStatus brings what shows the session's agents up to date: every
// dashboard reloads.
func (e *Env) PushStatus() { e.pushStatus(false) }

// PushStatusAndLabels is PushStatus, and in the weaver session the threads'
// windows labelled again (a title, task or root changed).
func (e *Env) PushStatusAndLabels() { e.pushStatus(true) }

func (e *Env) pushStatus(labels bool) {
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
	if labels && e.Session != "" {
		rows, _ := e.AgentRows()
		e.threadTabLabels(rows)
	}
}

// BoardFiles are a board's socket and the files beside it, by their stem
// base: they go with it.
func BoardFiles(base string) []string {
	return []string{base + ".sock", base + ".keys", base + ".view", base + ".title", base + ".label", base + ".room",
		base + ".shown", base + ".tight", base + ".vp", base + ".rows", base + ".holding", base + ".heldopen"}
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

// threadTabLabels: a thread's first window is labelled "<num> <name> ·
// <repo>/<branch>"; renamed only when that is not its label already, as
// its row read it (Row.WindowName: rows read the panes, and their windows'
// labels, once).
func (e *Env) threadTabLabels(rows []Row) {
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
		if r.WindowName == label {
			continue
		}
		e.Mux().Label(r.Window, label)
	}
}

// ThreadLabel is the label of a thread's first window: its number,
// what it is about (the first line of text, fitted), and where it works.
func ThreadLabel(n int, text, root string) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	return padNum(n) + " " + strings.TrimRight(oneline.Fit(text, 30), " ") + " · " + git.WhereOf(root).Name()
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
