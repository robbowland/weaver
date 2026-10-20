package wvr

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/cli"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/git"
	"github.com/robbowland/weaver/internal/oneline"
	"golang.org/x/sys/unix"
)

// Glyphs and colours: every state is a task cell, a mark between brackets.
const (
	GNeed = agent.CellNeedsYou
	GDone = agent.CellDone
	GSeen = agent.CellDone
	GWork = agent.CellWorking
	GIdle = agent.CellIdle
	GInt  = agent.CellInterrupted
	GEnd  = agent.CellEnded

	CRed   = "\033[38;2;255;59;47m"
	CGreen = "\033[38;2;57;217;122m"
	CGrey  = "\033[38;2;153;153;153m"
	CDim   = "\033[38;2;97;97;97m"
	CBold  = "\033[1m"
	CInk   = "\033[38;2;255;255;255m"
	COff   = "\033[0m"
)

func sortStable[T any](s []T, less func(a, b T) bool) {
	sort.SliceStable(s, func(i, j int) bool { return less(s[i], s[j]) })
}

// paint: a cell's brackets in dim, its mark in colour, each closed by off.
func paint(cell, colour, dim, off string) string {
	r := []rune(cell)
	for len(r) < 3 {
		r = append(r, ' ')
	}
	return dim + string(r[0]) + off + colour + string(r[1]) + off + dim + string(r[2]) + off
}

// age: 75s -> 1m, 2h ...
func age(s int64) string {
	switch {
	case s < 60:
		return strconv.FormatInt(s, 10) + "s"
	case s < 3600:
		return strconv.FormatInt(s/60, 10) + "m"
	case s < 172800:
		return strconv.FormatInt(s/3600, 10) + "h"
	}
	return strconv.FormatInt(s/86400, 10) + "d"
}

func rlen(s string) int { return utf8.RuneCountInString(s) }

// ttySize is the terminal's rows and columns, from /dev/tty.
func ttySize() (int, int, bool) {
	f, err := os.Open("/dev/tty")
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
	if err != nil || ws.Row == 0 || ws.Col == 0 {
		return 0, 0, false
	}
	return int(ws.Row), int(ws.Col), true
}

// lsSize is the dashboard's size, columns and lines:
// fzf's, which inside its border (border=true) is 4 columns and 2 lines
// short of the pane; else the terminal's.
func lsSize() (int, int) {
	c, ok1 := cli.EnvInt("FZF_COLUMNS")
	l, ok2 := cli.EnvInt("FZF_LINES")
	if !ok1 || !ok2 {
		c, l = 0, 0
	}
	if c > 0 && l > 0 {
		c, l = c+4, l+2
	}
	if c == 0 || l == 0 {
		if r, k, ok := ttySize(); ok {
			l, c = r, k
		} else {
			c, l = 100, 40
			if n, ok := cli.EnvInt("COLUMNS"); ok {
				c = n
			}
			if n, ok := cli.EnvInt("LINES"); ok {
				l = n
			}
		}
	}
	return c, l
}

// The card: beside the list in a dashboard 150 columns wide or more (74 of
// them, its border included), else below it, in about 12 lines.
const cardW, cardH = 74, 12

func cardRight(cols int) bool { return cols >= 150 }

// lsWidth is the width of the dashboard's list: cols, less the card when
// that is beside it.
func lsWidth(cols int) int {
	if cardRight(cols) {
		return cols - cardW - 2
	}
	return cols
}

// sockBase is $FZF_SOCK without .sock: the dashboard's side files' stem.
func sockBase() string {
	s := os.Getenv("FZF_SOCK")
	if s == "" {
		return ""
	}
	return strings.TrimSuffix(s, ".sock")
}

// cardRoom is where the card goes in a dashboard cols x lines with a list of
// items lines (fzf's --preview-window), kept next to the socket (.room,
// .tight) for _chrome and _view.
func cardRoom(items, cols, lines int) string {
	pw, hide := "", false
	switch {
	case items == 0:
		pw = "down,50%,border-top,wrap,hidden"
	case cardRight(cols):
		pw = fmt.Sprintf("right,%d,border-left,wrap", cardW)
	default:
		p := lines - items - 5
		if p < 6 {
			hide, pw = true, "down,50%,border-top,wrap,hidden"
		} else {
			pw = fmt.Sprintf("down,%d,border-top,wrap", p)
		}
	}
	if base := sockBase(); base != "" {
		os.WriteFile(base+".room", []byte(pw+"\n"), 0o644)
		if hide {
			os.WriteFile(base+".tight", nil, 0o644)
		} else if files.Exists(base + ".tight") {
			os.Remove(base + ".tight")
		}
	}
	return pw
}

// The board's groups: by state, then read-only threads (reviews,
// investigations: not ones you watch closely) in a group of their own, then
// the threads lost with their session.
var heads = [6]string{"NEEDS YOU", "DONE", "WORKING", "IDLE", "REVIEWS", "LOST"}

const (
	groupReviews = 4
	groupLost    = 5
)

type boardRow struct {
	group                                    int
	key, pane, tab, glyph, name, fact, where string
	age, label, root, num                    string
	lost                                     bool
}

// Rows is the dashboard's lines for fzf: one TAB-separated item per line,
// id pane tab prev next label display, with the border's label, the header
// and the 1-9 keys next to $FZF_SOCK.
func (e *Env) Rows() string { return e.rows(false) }

// PlainRows is the board as text, $COLUMNS wide (`wvr ls | cat`).
func (e *Env) PlainRows() string { return e.rows(true) }

func (e *Env) rows(plain bool) string {
	var b strings.Builder
	colour := os.Getenv("COLOR")
	if _, set := os.LookupEnv("COLOR"); !set && !plain {
		colour = "1"
	}
	red, green, grey, dim, bold, off := "", "", "", "", "", ""
	if colour == "1" {
		red, green, grey, dim, bold, off = CRed, CGreen, CGrey, CDim, CBold, COff
	}
	width := 100
	if n, ok := cli.EnvInt("COLUMNS"); ok {
		width = n
	}
	if width < 40 {
		width = 40
	}
	jump := os.Getenv("LS_JUMP")
	if jump == "" {
		jump = "print(jump)+accept"
	}
	agents, now := e.AgentRows()
	var rs []boardRow
	counts := [6]int{}
	states := [3]int{} // needs you, done and working, read-only threads too: the title's counts
	wn, wf, ww, numw := 0, 0, 0, 2
	nums := false
	for _, a := range agents {
		if a.Pane == "" {
			continue
		}
		since := now
		if a.Since > 0 {
			since = a.Since
		}
		name := a.Title
		if name == "" {
			name = a.Ttask
		}
		if name == "" {
			name = a.Prompt
		}
		if name == "" {
			kind := a.Agent
			if kind == "" {
				kind = string(agent.Claude)
			}
			name = "new " + kind + " session"
		}
		var g int
		var glyph, fact string
		switch a.State {
		case agent.StateNeedsYou:
			g, fact = 0, a.Waiting
			if fact == "" {
				fact = "input"
			}
			glyph = paint(GNeed, red, dim, off)
		case agent.StateWorking:
			g = 2
			glyph = paint(GWork, grey, dim, off)
			switch {
			case a.Doing != "":
				fact = "running " + a.Doing
			case a.Bg > 0:
				fact = strconv.Itoa(a.Bg) + " in background"
			case a.Nsub > 0:
				fact = strconv.Itoa(a.Nsub) + " subagent"
				if a.Nsub != 1 {
					fact += "s"
				}
			case a.Ttotal > 0:
				fact = strconv.Itoa(a.Tdone) + "/" + strconv.Itoa(a.Ttotal) + " tasks"
			case a.File != "":
				fact = "editing " + a.File
			default:
				// Nothing it runs or edits yet: the model at work.
				fact = "thinking"
			}
		case agent.StateDone:
			if a.Seen {
				g, glyph = 3, paint(GSeen, dim, dim, off)
			} else {
				g, glyph = 1, paint(GDone, green, dim, off)
			}
			roots := a.Troots
			if len(roots) == 0 {
				roots = []string{a.Root}
			}
			tf := e.turnFiles(a.Sid, strconv.Itoa(a.Turn), roots)
			last := a.Last
			switch tf {
			case "", "0":
				fact = last
			case "1":
				fact = "1 file"
				if last != "" {
					fact += " · " + last
				}
			default:
				fact = tf + " files"
				if last != "" {
					fact += " · " + last
				}
			}
		case agent.StateInterrupted:
			g, fact = 3, string(agent.StateInterrupted)
			glyph = paint(GInt, grey, dim, off)
		case agent.StateStarting:
			// Its agent reports within seconds, unless its hooks are not in
			// place (Codex runs them only once trusted in /hooks).
			if now-since < 120 {
				g, fact = 2, string(agent.StateStarting)
				glyph = paint(GWork, grey, dim, off)
			} else {
				g, fact = 3, "not reporting"
				glyph = paint(GIdle, dim, dim, off)
				if a.Agent == "codex" {
					fact = "no hooks: trust them in codex /hooks"
				}
			}
		case agent.StateIdle, agent.StateEnded:
			// Seen and quiet: as anything else, the idle cell.
			fallthrough
		default:
			g = 3
			glyph = paint(GIdle, dim, dim, off)
			if a.Turn == 0 {
				fact = "no prompt yet"
			}
		}
		w := git.WhereOf(a.Root)
		where := w.Repo
		if w.Branch != "" {
			where += "/" + w.Branch
		}
		// Needs you, done and working: longest first; idle: latest first.
		k := since
		if g == 3 {
			k = 10000000000 - since
		}
		if g < 3 {
			states[g]++
		}
		// A read-only thread: in its own group, by state there.
		key := fmt.Sprintf("%d%011d", g, k)
		if a.Review {
			g, key = groupReviews, strconv.Itoa(groupReviews)+key
		}
		pn, _ := strconv.Atoi(string(a.Pane))
		r := boardRow{group: g, key: fmt.Sprintf("%s%08d", key, pn), pane: string(a.Pane), tab: string(a.Tab), glyph: glyph,
			name: name, fact: fact, where: where, age: age(now - since), root: a.Root}
		r.label = name + " · " + where + " · turn " + strconv.Itoa(a.Turn)
		if a.Tnum > 0 {
			r.num = strconv.Itoa(a.Tnum)
			nums = true
			if len(r.num) > numw {
				numw = len(r.num)
			}
		}
		counts[g]++
		wn = max(wn, rlen(name))
		wf = max(wf, rlen(fact))
		rs = append(rs, r)
	}
	live := len(rs)
	// Threads lost with their session (a crash, or quit), last: enter
	// resumes one in a new window (wvr resume).
	for _, l := range e.LostRows() {
		since := now
		if digits(l.Lost) {
			since, _ = strconv.ParseInt(l.Lost, 10, 64)
		}
		// Named as a live thread is: its task.
		name := oneline.Line(l.Task)
		if name == "" {
			name = l.Title
		}
		if name == "" {
			name = l.Name
		}
		fact := "its session went · ⏎ resumes"
		w := git.WhereOf(l.Dir)
		where := w.Repo
		if w.Branch != "" {
			where += "/" + w.Branch
		}
		r := boardRow{group: groupLost, key: fmt.Sprintf("%d%011d%08d", groupLost, 10000000000-since, len(rs)), pane: l.Sid,
			glyph: paint(GEnd, grey, dim, off), lost: true, name: name, fact: fact, where: where,
			age: age(now - since), root: l.Dir, label: name + " · " + where + " · lost"}
		counts[groupLost]++
		wn = max(wn, rlen(name))
		wf = max(wf, rlen(fact))
		rs = append(rs, r)
	}
	n := len(rs)
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool { return rs[order[i]].key < rs[order[j]].key })
	// Two agents in one worktree mix their per-turn diffs: say so (×2).
	for i := 0; i < n; i++ {
		if i < live {
			c := 0
			for j := 0; j < live; j++ {
				if rs[j].root == rs[i].root {
					c++
				}
			}
			if c > 1 {
				rs[i].where += " ×" + strconv.Itoa(c)
			}
		}
		ww = max(ww, rlen(rs[i].where))
	}

	// Columns: key and glyph, name, fact, where (when there is room), age.
	// fzf searches only what follows the TAB after the glyph (--nth 2).
	const ageW = 3
	pre, sep, cw, groups, gaps := 5, "  ", 0, 0, true
	lsCols, lsLines := 0, 0
	if !plain {
		pre, sep = numw+6, " \t"
		for g := range counts {
			if counts[g] > 0 {
				groups++
			}
		}
		// The layout.
		nn := min(wn+2, 42)
		nf, nw := 0, 0
		if wf > 0 {
			nf = min(wf+2, 58)
		}
		if ww > 0 {
			nw = min(ww+2, 30)
		}
		// Each group's heading and the gap under it (the last one's
		// between the list and its keys).
		items := 0
		if n > 0 {
			items = n + groups*2
		}
		lsCols, lsLines = lsSize()
		if n > 0 && items+4 > lsLines {
			gaps, items = false, n+groups
		}
		width = lsWidth(lsCols) - 7
		// Repo/branch only when the name and the fact keep 24 and 20 columns.
		if nw > 0 && width-nw >= pre+min(nn, 24)+min(nf, 20)+ageW {
			cw = nw
		}
		cardRoom(items, lsCols, lsLines)
	} else if width >= 96 && ww > 0 {
		cw = min(ww+2, 30)
	}
	k := width - pre - ageW - cw
	// The name comes first: at least 24 columns, 45% of the room once there
	// is plenty, but never past 42.
	cn := k * 45 / 100
	cn = max(cn, 24)
	cn = min(cn, 42)
	if wn > 0 && wn+2 < cn {
		cn = wn + 2
	}
	cf := k - cn
	if cf < 10 {
		cn, cf = k, 0
	}
	// No wider than the facts need: repo/branch and age stay close by.
	if cf > wf+2 {
		cf = wf + 2
	}
	if wf == 0 {
		cf = 0
	}
	width = pre + cn + cf + cw + ageW

	// The title: the thread count, and the status bar's counts at the right.
	titleL := "[ WEAVER :: THREADS ]"
	if live > 0 {
		titleL += " " + strconv.Itoa(live)
	}
	tk := bold
	if !plain && bold != "" {
		tk = CBold + CInk
	}
	tl := dim + "[" + off + " " + tk + "WEAVER" + off + " " + dim + "::" + off + " " + grey + "THREADS" + off + " " + dim + "]" + off
	if live > 0 {
		tl += " " + grey + strconv.Itoa(live) + off
	}
	titleR, tc := "", ""
	for g := 0; g < 3; g++ {
		if states[g] == 0 {
			continue
		}
		shown, c := GWork, grey
		switch g {
		case 0:
			shown, c = GNeed, red
		case 1:
			shown, c = GDone, green
		}
		titleR += shown + " " + strconv.Itoa(states[g]) + "  "
		tc += paint(shown, c, dim, off) + " " + grey + strconv.Itoa(states[g]) + off + "  "
	}
	titleR, tc = strings.TrimSuffix(titleR, "  "), strings.TrimSuffix(tc, "  ")
	// Home is in the weaver session, whose status line shows the counts.
	if os.Getenv("LS_HOME") != "" {
		titleR, tc = "", ""
	}
	gap := ""
	if !plain {
		k := max(lsCols-4-rlen(titleL)-rlen(titleR)-4, 1)
		if titleR != "" {
			gap = " " + dim + strings.Repeat("─", k) + off + " "
		}
	} else {
		gap = strings.Repeat(" ", max(width-rlen(titleL)-rlen(titleR), 2))
	}
	base := sockBase()
	if !plain {
		// The border's label, and (no agents) what to do, for the header.
		if base != "" {
			bestEffort(files.Put(base+".label", " "+tl+gap+tc+" "))
			out := ""
			if n == 0 {
				c, l := lsSize()
				out, _ = welcome(c, l, len(e.viewNames()) > 0)
			}
			bestEffort(files.Put(base+".title", out))
			if n == 0 {
				os.WriteFile(base+".keys", nil, 0o644)
			}
		}
		if n == 0 {
			return ""
		}
	} else {
		b.WriteString(tl + gap + tc + "\n\n")
		if n == 0 {
			b.WriteString("No agents in session " + e.Session + ".\n")
			return b.String()
		}
	}

	// Positions first (headings and gaps take one each), so each heading and
	// gap knows the agents around it.
	type item struct {
		id, pane, tab, text, label string
		isAgent                    bool
	}
	var ls []item
	var keyl [10]string
	lastG, a := -1, 0
	for _, i := range order {
		r := rs[i]
		g := r.group
		if g != lastG {
			if lastG != -1 && gaps {
				ls = append(ls, item{id: "g" + strconv.Itoa(g)})
			}
			t := grey + heads[g] + off + dim + " · " + off + grey + strconv.Itoa(counts[g]) + off
			if plain {
				t = heads[g] + " · " + strconv.Itoa(counts[g])
			}
			ls = append(ls, item{id: "h" + strconv.Itoa(g), text: t})
			lastG = g
		}
		lead := ""
		if r.lost {
			// A lost thread: no number, no 1-9 key.
			if !plain {
				lead = strings.Repeat(" ", numw) + " "
			}
		} else if !plain {
			a++
			// Threads' stable numbers when they have them, else the rows'
			// order; two digits, like the vault's numbered sections (01).
			c, shown := "", ""
			if nums {
				c, shown = r.num, r.num
			} else {
				c = strconv.Itoa(a)
				if a <= 9 {
					shown = c
				}
			}
			if shown != "" {
				v, _ := strconv.Atoi(shown)
				shown = fmt.Sprintf("%02d", v)
			}
			lead = dim + fmt.Sprintf("%*s", numw, shown) + off + " "
			if len(c) == 1 && c >= "1" && c <= "9" {
				keyl[c[0]-'0'] = "pos(" + strconv.Itoa(len(ls)+1) + ")+" + jump
			}
		}
		f := oneline.Fit(r.name, cn-2)
		if g >= 2 {
			f = grey + f + off
		}
		out := lead + r.glyph + sep + f + "  "
		if cf > 0 {
			out += grey + oneline.Fit(r.fact, cf-2) + off + "  "
		}
		if cw > 0 {
			out += dim + oneline.Fit(r.where, cw-2) + off + "  "
		}
		out += dim + fmt.Sprintf("%*s", ageW, r.age) + off
		id := "a" + r.pane
		if r.lost {
			id = "l" + r.pane
		}
		ls = append(ls, item{id: id, pane: r.pane, tab: r.tab, text: out, label: r.label, isAgent: true})
	}
	// A gap under the last group too, between the list and its keys, as a
	// view has.
	if gaps && lastG != -1 && !plain {
		ls = append(ls, item{id: "gz"})
	}
	if plain {
		for _, it := range ls {
			if it.id[0] == 'g' {
				b.WriteString("\n")
			} else {
				b.WriteString(it.text + "\n")
			}
		}
		return b.String()
	}
	// 1-9: fzf actions that pick those agents.
	if base != "" {
		keys := ""
		for c := 1; c <= 9; c++ {
			keys += keyl[c] + "\n"
		}
		bestEffort(files.Put(base+".keys", keys))
	}
	for i, it := range ls {
		if it.isAgent {
			fmt.Fprintf(&b, "%s\t%s\t%s\t\t\t%s\t%s\n", it.id, it.pane, it.tab, it.label, it.text)
			continue
		}
		prevp, nextp := "", ""
		for j := i - 1; j >= 0; j-- {
			if ls[j].isAgent {
				prevp = strconv.Itoa(j + 1)
				break
			}
		}
		for j := i + 1; j < len(ls); j++ {
			if ls[j].isAgent {
				nextp = strconv.Itoa(j + 1)
				break
			}
		}
		fmt.Fprintf(&b, "%s\t\t\t%s\t%s\t\t%s\t\n", it.id, prevp, nextp, it.text)
	}
	return b.String()
}

// turnFiles is how many files the agent's last turn changed, from the end
// snapshots of turn turn of session sid in roots; empty when there is none.
// Each snapshot commit is asked about once; the answer is kept under
// $STATE_DIR/turns/<commit>.
func (e *Env) turnFiles(sid, turn string, roots []string) string {
	if sid == "" || turn == "0" {
		return ""
	}
	total, counted := 0, false
	for _, r := range roots {
		if r == "" {
			continue
		}
		w := git.WhereOf(r)
		if w.GitDir == "" {
			continue
		}
		sha, _ := files.FirstLine(w.GitDir + "/refs/worktree/weaver/sessions/" + sid)
		if sha == "" || strings.Trim(sha, "0123456789abcdef") != "" {
			continue
		}
		line, _ := files.FirstLine(e.StateDir + "/turns/" + sha)
		if line == "" {
			line = gitTurn(r, sha)
			f := strings.Fields(line)
			if len(f) != 3 || !digits(f[0][:1]) || f[1][0] < 'a' || f[1][0] > 'z' || !digits(f[2][:1]) {
				continue
			}
			os.MkdirAll(e.StateDir+"/turns", 0o755)
			tmp := e.StateDir + "/turns/" + sha + "." + strconv.Itoa(os.Getpid())
			if os.WriteFile(tmp, []byte(line+"\n"), 0o644) == nil {
				os.Rename(tmp, e.StateDir+"/turns/"+sha)
			}
		}
		f := strings.Fields(line)
		if len(f) < 3 || f[0] != turn || !strings.Contains(line, " end ") {
			continue
		}
		c, _ := strconv.Atoi(f[2])
		total += c
		counted = true
	}
	if !counted {
		return ""
	}
	return strconv.Itoa(total)
}

// boardHints are the dashboard's footer: the keys that work now, as many as
// fit the list's width, ? always last.
func boardHints() []choice {
	browse := "⏎ JUMP|1-9|n NEW|x CLOSE|/ SEARCH|s SCREEN|v NVIM|tab VIEWS|ctrl+q QUIT"
	search := "⏎ JUMP|esc BACK|ctrl+e NVIM|tab VIEWS"
	// A toggle says where it goes next: s from the screen (or the card
	// hidden) to the card, ? from the help back to it.
	showing := ""
	if b := sockBase(); b != "" {
		showing, _ = files.FirstLine(b + ".view")
	}
	if pl := os.Getenv("FZF_PREVIEW_LINES"); showing == "screen" || sockBase() != "" && files.Exists(sockBase()+".tight") && (pl == "" || pl == "0") {
		browse = strings.Replace(browse, "s SCREEN", "s CARD", 1)
	}
	helpWhat := "HELP"
	if showing == "help" {
		helpWhat = "CARD"
	}
	cols, _ := lsSize()
	width := lsWidth(cols) - 6
	w := 0
	var items []string
	help := false
	total := os.Getenv("FZF_TOTAL_COUNT")
	switch {
	case searching():
		items = strings.Split(search, "|")
	case os.Getenv("LS_EMPTY") == "1" || total == "0":
		if os.Getenv("LS_HOME") != "" {
			items = []string{"n NEW THREAD", "ctrl+q QUIT"}
		} else {
			items = []string{"n NEW THREAD", "q BACK", "ctrl+q QUIT"}
		}
	default:
		items = strings.Split(browse, "|")
		help, w = true, 6
	}
	var hs []choice
	for _, it := range items {
		if w+rlen(it)+2 > width {
			break
		}
		k, rest, _ := strings.Cut(it, " ")
		hs = append(hs, choice{k, rest})
		w += rlen(it) + 2
	}
	if help {
		hs = append(hs, choice{"?", helpWhat})
	}
	return hs
}

// Footer is the dashboard's footer: its hints, each a button.
func Footer() string { return hintLine(boardHints(), 2) + "\n" }

// welcome is the empty board's header: what to do, centred in the room
// the list has on a terminal cols x lines (a third of the way down, as a
// card sits), and for each of its lines (from 1) what a click on it does,
// "" for none: each key line is its key's button.
func welcome(cols, lines int, views bool) (string, []string) {
	type row struct{ key, what, act string }
	rows := []row{{"n", "start a thread here", "trigger(n)"}, {"alt+N", "start one from anywhere", "execute-silent(tmux send-keys -K M-N)"},
		{"a", "resume a conversation as a thread", "trigger(a)"}}
	if views {
		rows = append(rows, row{"alt+y", "your pull requests and issues", "execute-silent(tmux send-keys -K M-y)"})
	}
	about := "a thread is a task, its worktree, and an agent beside its nvim"
	note := "or start claude or codex in any pane: weaver follows it"
	text := []string{CBold + CInk + "No threads yet" + COff, CGrey + about + COff, ""}
	acts := []string{"", "", ""}
	w := max(rlen(about), rlen(note))
	for _, r := range rows {
		text = append(text, CInk+fmt.Sprintf("%-8s", r.key)+COff+CGrey+r.what+COff)
		acts = append(acts, r.act)
		w = max(w, 8+rlen(r.what))
	}
	text = append(text, "", CDim+note+COff)
	acts = append(acts, "", "")
	// The room (no card beside an empty list): less the borders and the
	// gutter.
	pad := strings.Repeat(" ", max((cols-4-w)/2, 0))
	top := max((lines-4-len(text))/3, 0)
	var b strings.Builder
	for range top {
		b.WriteString("\n")
	}
	for i, t := range text {
		if i > 0 {
			b.WriteString("\n")
		}
		if t != "" {
			b.WriteString(pad + t)
		}
	}
	return b.String(), append(make([]string, top), acts...)
}

// welcomeClick is the action for a click on line (from 1) of the empty
// board's welcome, when it shows.
func welcomeClick(views bool) string {
	if t, _ := files.ReadTrim(sockBase() + ".title"); t == "" {
		return ""
	}
	line, _ := cli.EnvInt("FZF_CLICK_HEADER_LINE")
	c, l := lsSize()
	if _, acts := welcome(c, l, views); line >= 1 && line <= len(acts) && acts[line-1] != "" {
		return acts[line-1] + "\n"
	}
	return ""
}

// Title is the dashboard's header: with no agents,
// what to do; else a blank line while browsing, whose place the search input
// takes.
func Title() string {
	t, _ := files.ReadTrim(sockBase() + ".title")
	if t != "" {
		return t + "\n"
	}
	if in := os.Getenv("FZF_INPUT_STATE"); in == "" || in == "hidden" {
		return COff + "\n"
	}
	return ""
}

// fzfAct is fzf's action with text as its argument, in the first of fzf's
// delimiters whose closing one the text does not hold.
func fzfAct(action, text string) (string, bool) {
	for _, d := range []string{"()", "[]", "{}", "<>", "~~", "!!", "@@", "##", "%%", "^^", "**", ";;", "//", "||"} {
		if strings.IndexByte(text, d[1]) >= 0 {
			continue
		}
		return action + d[:1] + text + d[1:], true
	}
	return "", false
}

// HF is the dashboard's header and footer as fzf actions.
func HF() string {
	h := strings.TrimRight(Title(), "\n")
	f := strings.TrimRight(Footer(), "\n")
	var acts []string
	if a, ok := fzfAct("change-header", h); ok {
		acts = append(acts, a)
	}
	if a, ok := fzfAct("change-footer", f); ok {
		acts = append(acts, a)
	}
	return strings.Join(acts, "+")
}

// Chrome is what a reload changes besides the list (a transform on fzf's
// load): the header, the footer, the title on the border, and the card's
// room when the reload changed it.
func (e *Env) Chrome() string {
	base := sockBase()
	// The home board, with a newer weaver installed since it started: it
	// ends, for the board to start again in that weaver.
	if os.Getenv("LS_HOME") != "" && files.Exists(base+".born") && newer(e.Self, base+".born") {
		return "abort\n"
	}
	out := ""
	if hf := HF(); hf != "" {
		out = hf + "+"
	}
	pw, _ := files.FirstLine(base + ".room")
	last, _ := files.FirstLine(base + ".shown")
	if pw != "" && pw != last {
		os.WriteFile(base+".shown", []byte(pw+"\n"), 0o644)
		out += "change-preview-window(" + pw + ")+"
	}
	label, _ := files.FirstLine(base + ".label")
	return out + "change-border-label:" + label + "\n"
}

// newer: is a's modification time after b's (test -nt)?
func newer(a, b string) bool {
	sa, err1 := os.Stat(a)
	sb, err2 := os.Stat(b)
	if err1 != nil || err2 != nil {
		return err1 == nil && err2 != nil
	}
	return sa.ModTime().After(sb.ModTime())
}

// gitTurn asks git about a snapshot commit: its turn and point (trailers),
// then how many files it changed, "turn point n".
func gitTurn(root, sha string) string {
	out, err := exec.Command("git", "-C", root, "show", "--no-renames", "--name-only",
		"--format=%(trailers:key=Weaver-Turn,valueonly,separator=) %(trailers:key=Weaver-Point,valueonly,separator=)",
		sha, "--").Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(string(out), "\n")
	f := strings.Fields(lines[0])
	for len(f) < 2 {
		f = append(f, "")
	}
	n := 0
	for _, l := range lines[1:] {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return f[0] + " " + f[1] + " " + strconv.Itoa(n)
}
