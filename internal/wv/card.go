package wv

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The dashboard's preview: an agent's resume card, its screen, the keys, or
// a lost thread's card.

const nbsp = " "

// cardAge is the card's age: 75 -> 1m, 2h ...
func cardAge(s int64) string { return age(s) }

// cardOne: runs of tabs, newlines and returns as one space.
var reTNR = regexp.MustCompile(`[\t\n\r]+`)

func cardOne(v any) string { return reTNR.ReplaceAllString(tostring(v), " ") }

// wrap: words into lines of at most n characters (a longer word is cut).
func wrap(t string, n int) []string {
	var ls []string
	for _, x := range strings.Split(t, " ") {
		if x == "" {
			continue
		}
		switch {
		case len(ls) == 0:
			ls = append(ls, runeSlice(x, n))
		case rlen(ls[len(ls)-1])+1+rlen(x) <= n:
			ls[len(ls)-1] += " " + x
		default:
			ls = append(ls, runeSlice(x, n))
		}
	}
	return ls
}

// field is a card's field: its label, then the value wrapped under itself,
// at most lines lines, in w columns.
func field(w int, label string, text any, color string, lines int) []string {
	t := cardOne(text)
	if t == "" {
		return nil
	}
	ls := wrap(t, max(w-11, 20))
	var out []string
	for i, l := range ls {
		if i >= lines {
			break
		}
		lead := "           "
		if i == 0 {
			lead = CDim + runeSlice(label+"           ", 11) + COff
		}
		s := lead + color + l + COff
		if i == lines-1 && len(ls) > lines {
			s += CDim + " …" + COff
		}
		out = append(out, s)
	}
	return out
}

func previewCols() int {
	if n, ok := envInt("FZF_PREVIEW_COLUMNS"); ok {
		return n
	}
	return 80
}

// titleLine is the card's title: its first part (to " · ") bold, the rest dim.
func titleLine(t string) string {
	head, _, _ := strings.Cut(t, " · ")
	return CBold + head + COff + CDim + t[len(head):] + COff + "\n"
}

// Help is the keys and glyphs, for the dashboard's ? view.
func Help() string {
	d, g, o := CDim, CGrey, COff
	p := func(cell, c string) string { return paint(cell, c, CDim, COff) }
	return g + "KEYS" + o + "\n" +
		"  " + g + "⏎  1-9" + o + "      jump to the thread " + d + "(1-9: that row)" + o + "\n" +
		"  " + g + "j k  g G" + o + "    move; first, last\n" +
		"  " + g + "/" + o + "           search " + d + "(esc: back)" + o + "\n" +
		"  " + g + "n" + o + "           new thread " + d + "(weaver new)" + o + "\n" +
		"  " + g + "x" + o + "           close the thread, its nvim and its window\n" +
		"              " + d + "(and offers to remove a merged worktree)" + o + "\n" +
		"  " + g + "e  ^e" + o + "       jump and open its nvim\n" +
		"  " + g + "s  tab" + o + "      resume card ↔ screen\n" +
		"  " + g + "^r" + o + "          reload\n" +
		"  " + g + "?" + o + "           this help " + d + "(? again: back)" + o + "\n" +
		"  " + g + "q  esc" + o + "      close the dashboard " + d + "(home: back to your thread)" + o + "\n" +
		"  " + g + "^q" + o + "          quit the session, every agent and nvim " + d + "(the threads" + o + "\n" +
		"              " + d + "come back as weaver starts; weaver quit asks first)" + o + "\n" +
		"\n" +
		g + "THREADS" + o + "\n" +
		"  " + p(GNeed, CRed) + "  needs you    " + p(GDone, CGreen) + "  done, not yet seen    " + p(GWork, g) + "  working\n" +
		"  " + p(GSeen, d) + "  done, seen   " + p(GIdle, d) + "  idle    " + p(GInt, g) + "  interrupted\n" +
		"\n" +
		"  " + d + "×2" + o + "  " + d + "two agents share the worktree: their turn diffs mix" + o + "\n" +
		"\n" +
		g + "LOST" + o + " " + d + "(its session went: a crash, or quit)" + o + "\n" +
		"  " + g + "⏎" + o + "           resume it in a new window " + d + "(weaver resume)" + o + "\n" +
		"  " + g + "x" + o + "           forget it " + d + "(its worktree stays)" + o + "\n"
}

// Intro is the card of an empty dashboard.
func Intro() string {
	d, g, o := CDim, CGrey, COff
	return "No agents in this session yet.\n\n" +
		g + "n" + o + "       start a thread: a task, its worktree, and a tab with\n" +
		"        nvim and the agent " + d + "(weaver new)" + o + "\n" +
		g + "Alt N" + o + "   the same from anywhere in the session\n\n" +
		d + "Or start claude or codex in any pane: weaver follows it\n" +
		"through its hooks." + o + "\n"
}

var reRoot = regexp.MustCompile(`"root":"([^"]*)"`)
var reWorktree = regexp.MustCompile(`"worktree":"([^"]*)"`)

// Preview is `weaver _preview PANE TITLE ID`.
func (e *Env) Preview(args []string) string {
	for len(args) < 3 {
		args = append(args, "")
	}
	pane, title, id := strings.TrimPrefix(args[0], "terminal_"), args[1], args[2]
	view := "card"
	if base := sockBase(); base != "" {
		if v, err := firstLine(base + ".view"); err == nil {
			view = v
		}
	}
	// A lost thread (its item's id l…, or without one, its session id).
	if strings.HasPrefix(id, "l") || id == "" && pane != "" && !digits(pane) {
		if view == "help" {
			return Help()
		}
		return e.lostCard(pane, title)
	}
	switch view {
	case "help":
		return Help()
	case "screen":
		if pane == "" {
			return ""
		}
		head, _, _ := strings.Cut(title, " · ")
		out := fmt.Sprintf("%sscreen · %s%s%s%s%s\n", CDim, COff+CBold, head, COff, CDim+title[len(head):], COff)
		lines := 40
		if n, ok := envInt("FZF_PREVIEW_LINES"); ok {
			lines = n
		}
		scr, _ := e.Tmux("capture-pane", "-p", "-e", "-t", "%"+pane)
		ls := strings.Split(strings.TrimSuffix(string(scr), "\n"), "\n")
		last := 0
		for i, l := range ls {
			if strings.TrimSpace(l) != "" {
				last = i + 1
			}
		}
		s := max(last-(lines-1)+1, 1)
		for i := s; i <= last; i++ {
			out += ls[i-1] + "\n"
		}
		return out
	}
	if pane == "" {
		return Intro()
	}
	sf := e.StateFile(pane)
	re := reRoot
	if !exists(sf) {
		// A thread whose agent has not reported yet: its record.
		sf, re = e.StateDir+"/"+e.SKey+"_"+pane+".thread", reWorktree
		if !exists(sf) {
			return ""
		}
	}
	out := ""
	if title != "" {
		out = titleLine(title)
	}
	l, _ := firstLine(sf)
	root := ""
	if m := re.FindStringSubmatch(l); m != nil {
		root = m[1]
	}
	// Other agents in the same worktree: their turn diffs mix.
	shared := 0
	for _, f := range e.SessionFiles("json") {
		if f == sf {
			continue
		}
		l, _ := firstLine(f)
		if m := reRoot.FindStringSubmatch(l); m != nil && m[1] == root {
			shared++
		}
	}
	// The worktree in one line: branch, ahead/behind, changes.
	head, ab, changed, nw := "", "", 0, 0
	if st, err := os.Stat(root); root != "" && err == nil && st.IsDir() {
		c := exec.Command("git", "-C", root, "status", "--porcelain=v2", "--branch")
		c.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
		b, _ := c.Output()
		for _, l := range strings.Split(string(b), "\n") {
			switch {
			case strings.HasPrefix(l, "# branch.head "):
				head = strings.TrimPrefix(l, "# branch.head ")
			case strings.HasPrefix(l, "# branch.ab "):
				if x := strings.TrimPrefix(l, "# branch.ab "); x != "+0 -0" {
					ab = x
				}
			case strings.HasPrefix(l, "? "):
				nw++
			case len(l) > 1 && strings.ContainsRune("12u", rune(l[0])) && l[1] == ' ':
				changed++
			}
		}
	}
	b, err := os.ReadFile(sf)
	if err != nil {
		return out
	}
	s, ok := readObj(b)
	if !ok {
		return out
	}
	return out + card(s, previewCols(), head, ab, changed, nw, shared)
}

func card(s obj, w int, head, ab string, changed, nw, shared int) string {
	now := time.Now().Unix()
	if _, ok := s["state"]; !ok {
		s = obj{"state": "starting", "since": s["created"], "prompt": s["task"], "agent": s["agent"]}
	}
	st := tostring(s.get("state", "idle"))
	seen := s.get("seen", false) == true
	since := now
	if v, ok := num(firstOf(s.get("since", nil), s.get("ts", nil))); ok {
		since = int64(v)
	}
	word := st
	switch st {
	case "needs-you":
		word = CRed + "needs you" + COff
	case "done":
		if !seen {
			word = CGreen + "done" + COff
		}
	}
	first := word + CDim + " · " + COff + cardAge(now-since)
	if ag := tostring(s.get("agent", "claude")); ag != "claude" {
		first += CDim + " · " + COff + ag
	}
	ls := []string{first, ""}
	add := func(f []string) { ls = append(ls, f...) }
	add(field(w, "asked", s.get("prompt", ""), "", 4))
	if st == "needs-you" {
		add(field(w, "waits", s.get("waiting", "you"), "", 3))
	}
	if d := s.get("doing", ""); d != "" && st == "working" {
		add(field(w, "runs", d, "", 3))
	}
	if n, ok := num(s.get("background", json.Number("0"))); ok && n > 0 {
		add(field(w, "background", s.get("background_tasks", ""), "", 2))
	}
	if l := s.get("last_message", ""); l != "" && st != "working" {
		add(field(w, "said", l, CGrey, 6))
	}
	if ts, ok := s.get("tasks", []any{}).([]any); ok && len(ts) > 0 {
		if len(ts) > 12 {
			ts = ts[len(ts)-12:]
		}
		var ps []string
		for _, t := range ts {
			to, _ := t.(map[string]any)
			g := GIdle + " "
			if to["done"] == true {
				g = GSeen + " "
			}
			ps = append(ps, strings.ReplaceAll(cardOne(g+tostring(to["subject"])), " ", nbsp))
		}
		add(field(w, "tasks", strings.Join(ps, nbsp+" "), "", 3))
	}
	if ss, ok := s.get("subagents", []any{}).([]any); ok && len(ss) > 0 {
		var ps []string
		for _, x := range ss {
			so, _ := x.(map[string]any)
			sn := now
			if v, ok := num(obj(so).get("since", nil)); ok {
				sn = int64(v)
			}
			ps = append(ps, tostring(so["type"])+" "+cardAge(now-sn))
		}
		add(field(w, "subagents", strings.Join(ps, " · "), "", 2))
	}
	if fs, ok := s.get("files", []any{}).([]any); ok && len(fs) > 0 {
		last := fs
		if len(last) > 6 {
			last = last[len(last)-6:]
		}
		var ps []string
		for i := len(last) - 1; i >= 0; i-- {
			ps = append(ps, strings.ReplaceAll(filepath.Base(tostring(last[i])), " ", nbsp))
		}
		t := strings.Join(ps, nbsp+" ")
		if len(fs) > 6 {
			t += nbsp + " +" + strconv.Itoa(len(fs)-6)
		}
		add(field(w, "files", t, CGrey, 2))
	}
	if head != "" {
		t := head
		if ab != "" {
			t += " " + ab
		}
		if changed+nw == 0 {
			t += " · clean"
		} else {
			if changed > 0 {
				t += " · " + strconv.Itoa(changed) + " changed"
			}
			if nw > 0 {
				t += " · " + strconv.Itoa(nw) + " new"
			}
		}
		add(field(w, "git", t, CDim, 1))
	}
	if shared > 0 {
		add(field(w, "shared", strconv.Itoa(shared+1)+" agents work in this worktree: their turn diffs mix", CDim, 2))
	}
	return strings.ReplaceAll(strings.Join(ls, "\n"), nbsp, " ") + "\n"
}

// lostCard is a lost thread's card: what was asked and last said, where,
// and the keys that act on it.
func (e *Env) lostCard(sid, title string) string {
	m, _ := filepath.Glob(e.LostDir + "/*_" + sid + ".json")
	f := ""
	for _, g := range m {
		if st, err := os.Stat(g); err == nil && st.Mode().IsRegular() {
			f = g
			break
		}
	}
	if f == "" {
		return ""
	}
	out := ""
	if title != "" {
		out = titleLine(title)
	}
	b, err := os.ReadFile(f)
	if err != nil {
		return out
	}
	o, ok := readObj(b)
	if !ok {
		return out
	}
	stm, _ := o.get("state", nil).(map[string]any)
	thm, _ := o.get("thread", nil).(map[string]any)
	st, th := obj(stm), obj(thm)
	now := time.Now().Unix()
	lost := now
	if v, ok := num(o.get("lost", nil)); ok {
		lost = int64(v)
	}
	w := previewCols()
	home := os.Getenv("HOME")
	dir := tostring(lostDir(st, th))
	switch {
	case dir == home:
		dir = "~"
	case strings.HasPrefix(dir, home+"/"):
		dir = "~" + dir[len(home):]
	}
	if n := max(w-11, 20); rlen(dir) > n {
		r := []rune(dir)
		dir = "…" + string(r[len(r)-n+1:])
	}
	ls := []string{"lost" + CDim + " · " + COff + cardAge(now-lost) + CDim + " · its session went" + COff, ""}
	ls = append(ls, field(w, "asked", firstOf(th.get("task", nil), st.get("prompt", nil), ""), "", 4)...)
	if l := st.get("last_message", ""); l != "" {
		ls = append(ls, field(w, "said", l, CGrey, 6)...)
	}
	ls = append(ls, field(w, "in", dir, CGrey, 1)...)
	ls = append(ls, field(w, "session", strings.TrimPrefix(tostring(lostSkey(o)), "tmux-")+" · "+tostring(st.get("session_id", "")), CDim, 2)...)
	ls = append(ls, "", CDim+"⏎ resume it in a new window   x forget it"+COff)
	return out + strings.Join(ls, "\n") + "\n"
}

// View is the preview's next view, as fzf actions (`weaver _view screen`,
// s or tab: card, screen, card; `weaver _view help`, ?: the keys, and back).
func View(what string) string {
	base := sockBase()
	if base == "" {
		return ""
	}
	f := base + ".view"
	cur, _ := firstLine(f)
	hidden := os.Getenv("FZF_PREVIEW_LINES") == "" || os.Getenv("FZF_PREVIEW_LINES") == "0"
	next := what
	if what+":"+cur == "screen:screen" || what+":"+cur == "help:help" {
		next = ""
	}
	out := ""
	if hidden {
		// Shown on request: the card first.
		if what == "screen" {
			next = ""
		}
		_, l := lsSize(true)
		out += fmt.Sprintf("change-preview-window(down,%d,border-top,wrap)+", l/2)
	} else if what+":"+cur == "screen:screen" && exists(base+".tight") {
		out += "hide-preview+"
	}
	if next != "" {
		os.WriteFile(f, []byte(next+"\n"), 0o644)
	} else {
		os.Remove(f)
	}
	return out + "refresh-preview+transform-footer(" + Quote(selfPath()) + " _footer)\n"
}

func selfPath() string { return self() }
