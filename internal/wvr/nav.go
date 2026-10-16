package wvr

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Moving between agents: the board's jumps, Alt y (go home), Alt 1 (next),
// and tmux's hooks as you look (seen) or resize (fit).

// focus moves your client to pane p (its window first).
func (e *Env) focus(p string) {
	e.Tmux("select-window", "-t", "%"+p, ";", "select-pane", "-t", "%"+p)
}

// Goto is `wvr _goto PANE TAB`: jump to an agent from the home board
// (which stays) and count it as seen; with no PANE, to the tab.
func (e *Env) Goto(pane, tab string) {
	if pane == "" {
		e.Tmux("select-window", "-t", "@"+tab)
		return
	}
	e.focus(pane)
	e.MarkSeen(pane)
	e.PushStatus(false)
}

type tab struct {
	id, name, view string
	active         bool
}

func (e *Env) tabs() []tab {
	out, err := e.Tmux("list-windows", "-t", "="+e.Session, "-F", "#{window_id}"+us+"#{window_name}"+us+"#{window_active}"+us+"#{@weaver-view}")
	if err != nil {
		return nil
	}
	var ts []tab
	for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		f := strings.Split(l, us)
		if len(f) == 4 {
			ts = append(ts, tab{strings.TrimPrefix(f[0], "@"), f[1], f[3], f[2] == "1"})
		}
	}
	return ts
}

// GoHome is `wvr go home` (Alt y): from a thread (or anywhere else) to
// home, made again first if it was closed; from home on through the views
// (THREADS, GITHUB, LINEAR), and from the last back to where you came from.
func (e *Env) GoHome() {
	ts := e.tabs()
	var cur tab
	home := ""
	for _, t := range ts {
		if t.active && cur.id == "" {
			cur = t
		}
		if t.name == "home" && t.view == "" && home == "" {
			home = t.id
		}
	}
	lf := e.StateDir + "/" + e.SKey + ".last"
	// Back where you came from; when that is gone (or was nowhere), home.
	back := func() {
		if last, _ := firstLine(lf); digits(last) {
			for _, t := range ts {
				if t.id == last && t.view == "" && t.id != home {
					e.Tmux("select-window", "-t", "@"+last)
					return
				}
			}
		}
		if home != "" && cur.id != home {
			e.Tmux("select-window", "-t", "@"+home)
		}
	}
	views := e.viewNames()
	at := -1
	if cur.view != "" {
		for i, v := range views {
			if v == cur.view {
				at = i
			}
		}
	}
	switch {
	case home != "" && cur.id == home, at >= 0:
		if at+1 < len(views) {
			if _, err := e.OpenView(views[at+1], false); err == nil {
				return
			}
		}
		back()
		return
	}
	if cur.id != "" && cur.view == "" {
		os.WriteFile(lf, []byte(cur.id+"\n"), 0o644)
	}
	if home == "" {
		if _, err := e.Tmux("new-window", "-t", "="+e.Session+":0", "-n", "home", "-e", "WEAVER_TMUX_SESSION="+e.Session, e.Self, "home"); err != nil {
			e.Tmux("new-window", "-t", "="+e.Session+":", "-n", "home", "-e", "WEAVER_TMUX_SESSION="+e.Session, e.Self, "home")
		}
		return
	}
	e.Tmux("select-window", "-t", "@"+home)
}

// uiLock is one `wvr edit` or `wvr next` per session at a time; with
// nowait a second press while the first is at work is dropped (false). A
// lock older than 10s was left by a killed process. Its release.
func (e *Env) uiLock(nowait bool) (func(), bool) {
	d := e.StateDir + "/" + e.SKey + ".ui"
	os.MkdirAll(e.StateDir, 0o755)
	for i := 0; ; i++ {
		if os.Mkdir(d, 0o755) == nil {
			break
		}
		now := time.Now().Unix()
		t := now
		if l, err := firstLine(d + "/at"); err == nil {
			if n, ok := atoi(l); ok {
				t = int64(n)
			}
		}
		if now-t > 10 {
			os.RemoveAll(d)
			continue
		}
		if nowait {
			return func() {}, false
		}
		if i >= 100 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	os.WriteFile(d+"/at", []byte(strconv.FormatInt(time.Now().Unix(), 10)+"\n"), 0o644)
	return func() { os.RemoveAll(d) }, true
}

// Next is `wvr next` (Alt 1): to the agent that has needed you longest;
// failing that, the one that finished longest ago and you haven't looked at
// yet. Pressed again on one of those, on to the next, round and round.
func (e *Env) Next() {
	// Pressed again while the last press is still at work: that one moves
	// you, so this one does nothing.
	release, ok := e.uiLock(true)
	if !ok {
		return
	}
	defer release()
	rows, _ := e.AgentRows()
	type c struct {
		class     int
		since     int64
		pane, tab string
		label     string
	}
	var cs []c
	for _, r := range rows {
		if r.Pane == "" {
			continue
		}
		class := 0
		switch r.State {
		case "needs-you":
		case "done":
			if r.Seen == "true" {
				continue
			}
			class = 1
		default:
			continue
		}
		// What tmux's message line says as you land: whose, and why.
		l := r.Title
		if l == "" {
			l = r.Ttask
		}
		if l == "" {
			l = r.Prompt
		}
		if i := strings.IndexByte(l, '\n'); i >= 0 {
			l = l[:i]
		}
		if rlen(l) > 40 {
			l = runeSlice(l, 39) + "…"
		}
		if r.Tnum != "" {
			n, _ := strconv.Atoi(r.Tnum)
			l = fmt.Sprintf("%02d %s", n, l)
		}
		if r.State == "needs-you" {
			l += " · needs you"
			if r.Waiting != "" {
				l += " · " + runeSlice(r.Waiting, 50)
			}
		} else {
			l += " · done"
		}
		sn, _ := strconv.ParseInt(r.Since, 10, 64)
		cs = append(cs, c{class, sn, r.Pane, r.Tab, l})
	}
	if len(cs) == 0 {
		if isTTY(1) {
			fmt.Println("weaver: no agent needs you")
			time.Sleep(time.Second)
		} else {
			e.Tmux("display-message", "-d", "2000", "weaver: no agent needs you")
		}
		return
	}
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].class != cs[j].class {
			return cs[i].class < cs[j].class
		}
		return cs[i].since < cs[j].since
	})
	// Where you are now, to go on from there: the session's active pane (a
	// popup or run-shell is not a pane).
	focused := ""
	if out, err := e.Tmux("display-message", "-p", "-t", "="+e.Session+":", "#{pane_id}"); err == nil {
		focused = strings.TrimPrefix(strings.TrimSpace(string(out)), "%")
		if focused == e.MyPane {
			focused = ""
		}
	}
	at := -1
	for i, x := range cs {
		if x.pane == focused {
			at = i
		}
	}
	pick := (at + 1) % len(cs)
	e.focus(cs[pick].pane)
	e.MarkSeen(cs[pick].pane)
	e.Tmux("display-message", "-d", "2500", fmt.Sprintf("weaver: [%d/%d] %s", pick+1, len(cs), cs[pick].label))
	e.PushStatus(false)
}

// agentOf is the agent of pane p: the pane itself when it is an agent (it
// has state, a thread record or an nvim), or the agent whose nvim it is.
func (e *Env) agentOf(p string) string {
	if !digits(p) {
		return ""
	}
	for _, x := range []string{"json", "thread", "nvim"} {
		if exists(e.StateDir + "/" + e.SKey + "_" + p + "." + x) {
			return p
		}
	}
	for _, f := range e.SessionFiles("nvim") {
		if q, _ := firstLine(f); q == p {
			b := strings.TrimSuffix(f, ".nvim")
			return b[strings.LastIndexByte(b, '_')+1:]
		}
	}
	return ""
}

func doneUnseen(l string) bool {
	return strings.Contains(l, `"state":"done"`) && !strings.Contains(l, `"seen":true`)
}

// Seen is `wvr _seen WINDOW` (tmux's pane-focus-in hook, 1.5 s after you
// focus a window flagged as holding an agent done and unseen): if you are
// still on it, the agent you are looking at is seen.
func (e *Env) Seen(win string) {
	win = strings.TrimPrefix(win, "@")
	out, err := e.Tmux("display-message", "-p", "-t", "@"+win, "#{window_active}:#{session_attached}:#{pane_id}")
	if err != nil {
		return
	}
	info := strings.TrimSpace(string(out))
	f := strings.SplitN(info, ":", 3)
	if len(f) != 3 || f[0] != "1" || f[1] == "" || f[1] == "0" || !strings.HasPrefix(f[2], "%") {
		return
	}
	a := e.agentOf(strings.TrimPrefix(f[2], "%"))
	if a == "" {
		return
	}
	if l, _ := firstLine(e.StateFile(a)); doneUnseen(l) {
		e.MarkSeen(a)
	}
	left := false
	if ps, err := e.Tmux("list-panes", "-t", "@"+win, "-F", "#{pane_id}"); err == nil {
		for _, p := range strings.Fields(string(ps)) {
			if l, _ := firstLine(e.StateFile(strings.TrimPrefix(p, "%"))); doneUnseen(l) {
				left = true
			}
		}
	}
	if !left {
		e.Tmux("set-option", "-w", "-u", "-t", "@"+win, "@weaver-unseen")
	}
	e.PushStatus(false)
}

// agentCols is the agent's columns in a thread window total columns wide:
// $WEAVER_AGENT_WIDTH of it (20%), at least $WEAVER_AGENT_MIN_COLS (44) as
// long as nvim keeps 60; columns given outright as they are.
func agentCols(total int) int {
	w := os.Getenv("WEAVER_AGENT_WIDTH")
	pct := 20
	switch {
	case strings.HasSuffix(w, "%") && len(w) >= 2 && len(w) <= 3 && digits(w[:len(w)-1]) && w[0] != '0':
		pct, _ = strconv.Atoi(w[:len(w)-1])
	case len(w) >= 1 && len(w) <= 3 && digits(w) && w[0] != '0':
		n, _ := strconv.Atoi(w)
		return n
	}
	min := 44
	if n, ok := atoi(os.Getenv("WEAVER_AGENT_MIN_COLS")); ok {
		min = n
	}
	c := total * pct / 100
	if min > total-60 {
		min = total - 60
	}
	if c < min {
		c = min
	}
	return c
}

// Fit is `wvr _fit WINDOW` (tmux's window-resized hook): its thread's
// agent back to its width, since tmux takes a narrower window's columns from
// the rightmost pane, the agent's.
func (e *Env) Fit(win string) {
	win = strings.TrimPrefix(win, "@")
	out, err := e.Tmux("list-panes", "-t", "@"+win, "-F", "#{pane_id}")
	if err != nil {
		return
	}
	ps := strings.Fields(string(out))
	for _, p := range ps {
		p = strings.TrimPrefix(p, "%")
		if !exists(e.StateDir + "/" + e.SKey + "_" + p + ".thread") {
			continue
		}
		if readNvimPane(e, p) == "" || len(ps) != 2 {
			continue
		}
		t, err := e.Tmux("display-message", "-p", "-t", "%"+p, "#{window_width}")
		total, ok := atoi(strings.TrimSpace(string(t)))
		if err != nil || !ok {
			continue
		}
		e.Tmux("resize-pane", "-t", "%"+p, "-x", strconv.Itoa(agentCols(total)))
	}
}
