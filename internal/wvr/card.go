package wvr

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

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/jsonx"
	"github.com/robbowland/weaver/internal/oneline"
	"github.com/robbowland/weaver/internal/store"
	"github.com/robbowland/weaver/internal/tmux"
)

// The dashboard's preview: an agent's resume card, its screen, the keys, or
// a lost thread's card.

const nbsp = " "

// cardAge is the card's age: 75 -> 1m, 2h ...
func cardAge(s int64) string { return age(s) }

// cardOne: runs of tabs, newlines and returns as one space.
var reTNR = regexp.MustCompile(`[\t\n\r]+`)

func cardOne(s string) string { return reTNR.ReplaceAllString(s, " ") }

// wrap: words into lines of at most n characters (a longer word is cut).
func wrap(t string, n int) []string {
	var ls []string
	for _, x := range strings.Split(t, " ") {
		if x == "" {
			continue
		}
		switch {
		case len(ls) == 0:
			ls = append(ls, oneline.Cut(x, n))
		case rlen(ls[len(ls)-1])+1+rlen(x) <= n:
			ls[len(ls)-1] += " " + x
		default:
			ls = append(ls, oneline.Cut(x, n))
		}
	}
	return ls
}

// field is a card's field: its label, then the value wrapped under itself,
// at most lines lines, in w columns.
func field(w int, label, text, color string, lines int) []string {
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
			lead = CDim + oneline.Cut(label+"           ", 11) + COff
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
		"  " + g + "n" + o + "           new thread " + d + "(wvr new)" + o + "\n" +
		"  " + g + "x" + o + "           close the thread, its nvim and its window\n" +
		"              " + d + "(and offers to remove a merged worktree)" + o + "\n" +
		"  " + g + "e  ^e" + o + "       jump and open its nvim\n" +
		"  " + g + "s  tab" + o + "      resume card ↔ screen\n" +
		"  " + g + "^r" + o + "          reload\n" +
		"  " + g + "?" + o + "           this help " + d + "(? again: back)" + o + "\n" +
		"  " + g + "q  esc" + o + "      close the dashboard " + d + "(home: back to your thread)" + o + "\n" +
		"  " + g + "^q" + o + "          quit the session, every agent and nvim " + d + "(the threads" + o + "\n" +
		"              " + d + "come back as wvr starts; wvr quit asks first)" + o + "\n" +
		"\n" +
		g + "THREADS" + o + "\n" +
		"  " + p(GNeed, CRed) + "  needs you    " + p(GDone, CGreen) + "  done, not yet seen    " + p(GWork, g) + "  working\n" +
		"  " + p(GSeen, d) + "  done, seen   " + p(GIdle, d) + "  idle    " + p(GInt, g) + "  interrupted\n" +
		"\n" +
		"  " + d + "×2" + o + "  " + d + "two agents share the worktree: their turn diffs mix" + o + "\n" +
		"\n" +
		g + "LOST" + o + " " + d + "(its session went: a crash, or quit)" + o + "\n" +
		"  " + g + "⏎" + o + "           resume it in a new window " + d + "(wvr resume)" + o + "\n" +
		"  " + g + "x" + o + "           forget it " + d + "(its worktree stays)" + o + "\n"
}

// Intro is the card of an empty dashboard.
func Intro() string {
	d, g, o := CDim, CGrey, COff
	return "No agents in this session yet.\n\n" +
		g + "n" + o + "       start a thread: a task, its worktree, and a tab with\n" +
		"        nvim and the agent " + d + "(wvr new)" + o + "\n" +
		g + "Alt N" + o + "   the same from anywhere in the session\n\n" +
		d + "Or start claude or codex in any pane: weaver follows it\n" +
		"through its hooks." + o + "\n"
}

var reRoot = regexp.MustCompile(`"root":"([^"]*)"`)
var reWorktree = regexp.MustCompile(`"worktree":"([^"]*)"`)

// Preview is `wvr _preview PANE TITLE ID`.
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
	p := tmux.PaneID(pane)
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
		scr, _ := e.Tmux("capture-pane", "-p", "-e", "-t", p.Target())
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
	sf := e.StateFile(p)
	re := reRoot
	if !exists(sf) {
		// A thread whose agent has not reported yet: its record.
		sf, re = e.threadFile(p), reWorktree
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
	// The work item its thread started from.
	from := ""
	t, terr := store.ReadThread(e.threadFile(p))
	if terr == nil {
		from = itemLine(t)
	}
	s, err := store.ReadState(sf)
	if sf == e.threadFile(p) {
		// Its agent has not reported: the thread as it starts.
		if terr != nil {
			return out
		}
		s = agent.Record{State: agent.StateStarting, Since: json.Number(strconv.FormatInt(t.Created, 10)), Prompt: t.Task, Agent: string(t.Agent)}
	} else if err != nil {
		return out
	}
	return out + card(s, from, previewCols(), head, ab, changed, nw, shared)
}

// itemLine is a thread record's work item for its card: PR o/r#45, issue
// o/r#12 or ENG-42, and review when it is one; "" without one.
func itemLine(t store.Thread) string {
	if t.Item == nil || t.Item.Ref == "" {
		return ""
	}
	l := t.Item.Ref
	switch t.Item.Kind {
	case "pr":
		l = "PR " + l
	case "issue":
		l = "issue " + l
	}
	if t.Mode == "review" {
		l += " · review, read-only"
	}
	return l
}

func card(s agent.Record, from string, w int, head, ab string, changed, nw, shared int) string {
	now := time.Now().Unix()
	st := s.State
	if st == "" {
		st = agent.StateIdle
	}
	since := now
	if v := numberOr(s.Since, 0); s.Since != "" {
		since = int64(v)
	} else if s.TS != 0 {
		since = s.TS
	}
	word := string(st)
	switch st {
	case agent.StateNeedsYou:
		word = CRed + "needs you" + COff
	case agent.StateDone:
		if !s.Seen {
			word = CGreen + string(agent.StateDone) + COff
		}
	case agent.StateWorking, agent.StateIdle, agent.StateInterrupted, agent.StateEnded, agent.StateStarting:
		// The state's own word.
	}
	first := word + CDim + " · " + COff + cardAge(now-since)
	if s.Agent != "" && s.Agent != string(agent.Claude) {
		first += CDim + " · " + COff + s.Agent
	}
	ls := []string{first, ""}
	add := func(f []string) { ls = append(ls, f...) }
	if from != "" {
		add(field(w, "from", from, "", 1))
	}
	add(field(w, "asked", s.Prompt, "", 4))
	if st == agent.StateNeedsYou {
		waits := s.Waiting
		if waits == "" {
			waits = "you"
		}
		add(field(w, "waits", waits, "", 3))
	}
	if s.Doing != "" && st == agent.StateWorking {
		add(field(w, "runs", s.Doing, "", 3))
	}
	if s.Background > 0 {
		add(field(w, "background", s.BackgroundTasks, "", 2))
	}
	if s.LastMessage != "" && st != agent.StateWorking {
		add(field(w, "said", s.LastMessage, CGrey, 6))
	}
	if ts := s.Tasks; len(ts) > 0 {
		if len(ts) > 12 {
			ts = ts[len(ts)-12:]
		}
		var ps []string
		for _, t := range ts {
			g := GIdle + " "
			if t.Done {
				g = GSeen + " "
			}
			ps = append(ps, strings.ReplaceAll(cardOne(g+t.Subject), " ", nbsp))
		}
		add(field(w, "tasks", strings.Join(ps, nbsp+" "), "", 3))
	}
	if len(s.Subagents) > 0 {
		var ps []string
		for _, x := range s.Subagents {
			sn := now
			if x.Since != "" {
				sn = int64(numberOr(x.Since, float64(now)))
			}
			ps = append(ps, x.Type+" "+cardAge(now-sn))
		}
		add(field(w, "subagents", strings.Join(ps, " · "), "", 2))
	}
	if fs := s.Files; len(fs) > 0 {
		last := fs
		if len(last) > 6 {
			last = last[len(last)-6:]
		}
		var ps []string
		for i := len(last) - 1; i >= 0; i-- {
			ps = append(ps, strings.ReplaceAll(filepath.Base(last[i]), " ", nbsp))
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
	var lo store.Lost
	if json.Unmarshal(jsonx.First(b), &lo) != nil {
		return out
	}
	st := lo.State
	now := time.Now().Unix()
	lost := now
	if lo.Lost != 0 {
		lost = int64(lo.Lost)
	}
	w := previewCols()
	home := os.Getenv("HOME")
	dir := lo.Dir()
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
	ls = append(ls, field(w, "asked", lo.Task(), "", 4)...)
	if l := st.LastMessage; l != "" {
		ls = append(ls, field(w, "said", l, CGrey, 6)...)
	}
	ls = append(ls, field(w, "in", dir, CGrey, 1)...)
	ls = append(ls, field(w, "session", strings.TrimPrefix(lo.Key(), "tmux-")+" · "+st.SessionID, CDim, 2)...)
	ls = append(ls, "", CDim+"⏎ resume it in a new window   x forget it"+COff)
	return out + strings.Join(ls, "\n") + "\n"
}

// View is the preview's next view, as fzf actions (`wvr _view screen`,
// s or tab: card, screen, card; `wvr _view help`, ?: the keys, and back).
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
		_, l := lsSize()
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
