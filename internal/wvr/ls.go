package wvr

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/robbowland/weaver/internal/cli"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/session"
)

// FzfColors are the dashboards' fzf colours.
const FzfColors = "fg:-1,bg:-1,hl:-1:underline,fg+:#000000:strip,bg+:#ffffff,hl+:#000000:underline,gutter:-1," +
	"pointer:#000000,marker:#ffffff,header:-1,footer:#616161,border:#616161,preview-border:#616161,query:#ffffff," +
	"prompt:#999999,info:#616161,ghost:#616161,spinner:#616161,separator:#616161,label:#999999,scrollbar:#616161"

// fzfCmd is fzf with args and weaver's settings only: yours
// ($FZF_DEFAULT_OPTS, or its file) would change how weaver's lists look
// and what their keys do. env is more for its environment.
func fzfCmd(args []string, env ...string) *exec.Cmd {
	// No gutter column (fzf's draws a bar down the left).
	c := exec.Command("fzf", append([]string{"--gutter", " "}, args...)...)
	c.Env = append(os.Environ(), append([]string{"FZF_DEFAULT_OPTS=", "FZF_DEFAULT_OPTS_FILE="}, env...)...)
	return c
}

// lsKeys are the dashboard's keys while browsing; searching (/) unbinds
// them so they type.
const lsKeys = "j,k,g,G,n,x,a,v,e,s,q,?,/,1,2,3,4,5,6,7,8,9"

// Home is the home tab's board (`wvr home`): the dashboard, full-size and
// for good. What the session needs as it starts (old states swept, lost
// threads back, the status line's counts and the tabs' labels) runs behind
// it (`wvr _boot`), and reloads it when done.
func (e *Env) Home() error {
	if e.WeaverSession() {
		os.MkdirAll(e.StateDir, 0o755)
		sock := e.SockDir + "/" + e.SKey + ".fzf." + strconv.Itoa(os.Getpid()) + ".sock"
		c := exec.Command(e.Self, "_boot", sock)
		if log, err := os.OpenFile(e.StateDir+"/hook.log", os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644); err == nil {
			c.Stderr = log
		}
		c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if c.Start() == nil {
			go c.Wait()
		}
	}
	os.Setenv("LS_HOME", "1")
	return e.Ls()
}

// Ls is the dashboard (`wvr ls`): fzf over the session's agents, which
// hooks reload through its socket as they change. Without fzf or a terminal,
// the board as text. The home board ($LS_HOME) stays: its keys act and it
// carries on; any other ends with a key, and acts.
func (e *Env) Ls() error {
	_, err := e.ls("", "")
	return err
}

// ls is the dashboard. In Alt y's popover (`wvr _peek`), peek is the file
// for what is chosen there (peekChoice) and at the pane whose row the
// cursor starts on; it ends with a key, and says which way tab and
// shift-tab went ("next", "prev"), else "".
func (e *Env) ls(peek, at string) (string, error) {
	home := os.Getenv("LS_HOME") != ""
	if _, err := exec.LookPath("fzf"); err != nil || !cli.IsTTY(1) {
		fmt.Print(e.PlainRows())
		return "", nil
	}
	q := session.Quote(e.Self)
	sh := "/bin/sh"
	if st, err := os.Stat("/bin/dash"); err == nil && st.Mode()&0o111 != 0 {
		sh = "/bin/dash"
	}
	var jumpAct, editAct, newAct, quitAct, resumeAct string
	if home {
		jumpAct = "execute-silent(" + q + " _goto {2} {3})"
		editAct = "execute-silent(" + q + " _goto {2} {3}; " + q + " edit {2})"
		// The form in the popup Alt N opens, over the board (which the new
		// thread's status push reloads). fzf skips any action whose
		// template names a field, such as {2}, on an empty board: this one
		// prints the field for the action it returns.
		newAct = "transform:if [ \"$FZF_TOTAL_COUNT\" = 0 ]; then echo 'execute-silent(" + q + " _popup new)'; else printf '%s2})\\n' 'execute-silent(" + q + " _popup new --from {'; fi"
		// q and esc: back to the thread you came from, as Alt y's last
		// step does (not on to the views).
		quitAct = "execute-silent(" + q + " go back)"
		// In the background, as a jump is: you land on its window (or are
		// told why not), with no flash of the pane's own screen between.
		resumeAct = "execute-silent(o=$(" + q + " resume {2} 2>&1) || tmux display-message -d 4000 \"$o\")+reload-sync(" + q + " _rows)"
	} else {
		jumpAct, editAct, newAct, quitAct, resumeAct = "print(jump)+accept", "print(ctrl-e)+accept", "print(new)+accept",
			"abort", "print(resume)+accept"
	}
	// x, a, tab and a right click open dialogs or go to other windows: in
	// the popover, which is in the way of both, they end it and say so.
	closeAct := "transform:case {1} in a*) echo 'execute-silent(" + q + " _popup close {2})' ;; l*) echo 'execute-silent(" + q + " _popup forget {2})' ;; esac"
	// An agent you started by hand, adopted; else the picker of
	// conversations (on an empty board too, as n's field is made).
	adoptAct := "transform:if [ \"$FZF_TOTAL_COUNT\" = 0 ]; then echo 'execute-silent(" + q + " _popup adopt)'; else printf '%s2})\\n' 'execute-silent(" + q + " _popup adopt {'; fi"
	nextAct, prevAct, menuAct := "execute-silent("+q+" go next)", "execute-silent("+q+" go prev)", "execute-silent("+q+" _menu board {1})"
	if peek != "" {
		closeAct = "transform:case {1} in a*|l*) echo 'print(close)+accept' ;; esac"
		adoptAct, nextAct, prevAct, menuAct = "print(adopt)+accept", "print(next)+accept", "print(prev)+accept", "ignore"
	}
	os.Setenv("LS_JUMP", jumpAct)
	if st, err := os.Stat(e.SockDir); err != nil || !st.IsDir() {
		os.MkdirAll(e.SockDir, 0o700)
		os.Chmod(e.SockDir, 0o700)
	}
	// Hooks reload this dashboard through its socket as agents change; its
	// files go with it, also when its pane is closed under it.
	base := e.SockDir + "/" + e.SKey + ".fzf." + strconv.Itoa(os.Getpid())
	if peek != "" {
		base = e.SockDir + "/" + e.SKey + ".fzf.p" + strconv.Itoa(os.Getpid())
	}
	sock := base + ".sock"
	side := []string{sock, base + ".keys", base + ".view", base + ".title", base + ".label", base + ".room",
		base + ".shown", base + ".tight", base + ".vp"}
	clean := func() {
		for _, f := range side {
			os.Remove(f)
		}
	}
	sig := make(chan os.Signal, 4)
	signal.Notify(sig, syscall.SIGHUP, syscall.SIGTERM, os.Interrupt)
	go func() {
		for s := range sig {
			switch s {
			case syscall.SIGHUP:
				clean()
				os.Remove(base + ".born")
				os.Exit(129)
			case syscall.SIGTERM:
				clean()
				os.Remove(base + ".born")
				os.Exit(143)
			}
			// ctrl-c is fzf's (it ends with 130); the board carries on.
		}
	}()
	defer func() {
		clean()
		os.Remove(base + ".born")
	}()
	// The home board outlives installs of weaver: when it started, for
	// _chrome to tell that a newer weaver is installed under it.
	if home {
		os.WriteFile(base+".born", nil, 0o644)
		// The status line's strip and counts as this weaver draws them (and
		// clicks them): an install may have changed that under a running
		// session, whose home board then starts again in it.
		e.SetStrip()
		e.PushStatusAndLabels()
	}
	for {
		for _, f := range side {
			if files.Exists(f) {
				os.Remove(f)
			}
		}
		os.Setenv("FZF_SOCK", sock)
		rows := e.Rows()
		items := strings.Count(rows, "\n")
		if items == 0 {
			os.Setenv("LS_EMPTY", "1")
		}
		foot := strings.TrimRight(Footer(), "\n")
		os.Unsetenv("LS_EMPTY")
		header := strings.TrimRight(Title(), "\n")
		os.Unsetenv("FZF_SOCK")
		pw, _ := files.FirstLine(base + ".room")
		label, _ := files.FirstLine(base + ".label")
		os.WriteFile(base+".shown", []byte(pw+"\n"), 0o644)
		if pw == "" {
			pw = "down,50%,border-top,wrap,hidden"
		}
		args := []string{"--listen", sock, "--with-shell", sh + " -c",
			"--track", "--id-nth", "1", "--delimiter", "\t", "--with-nth", "7..", "--nth", "2", "--tabstop", "1",
			"--no-sort", "--ansi", "--layout", "reverse",
			"--no-input", "--header", header, "--header-first",
			"--border", "sharp", "--border-label", label, "--border-label-pos", "3",
			"--highlight-line", "--pointer", " ", "--marker", " ", "--ellipsis", "…",
			"--scrollbar", "│", "--footer", foot, "--footer-border", "none",
			"--color", FzfColors,
			"--prompt", "/ ", "--ghost", "search threads", "--info", "hidden", "--no-separator",
			"--preview", q + " _preview {2} {6} {1}", "--preview-window", pw,
			"--bind", "resize:reload-sync(LS_REMEASURE=1 " + q + " _rows)", "--bind", "load:transform(" + q + " _chrome)",
			"--bind", `focus:transform:case {1} in a*) ;; *) case $FZF_ACTION in up|page-up|half-page-up|offset-up|scroll-up|up-match|prev-*) t={4}; [ -n "$t" ] || t={5} ;; *) t={5}; [ -n "$t" ] || t={4} ;; esac; [ -n "$t" ] && echo "pos($t)" ;; esac`,
			"--bind", "j:down,k:up,g,home:first,G,end:last,q:" + quitAct,
			"--bind", "enter,double-click:transform:case {1} in a*) echo '" + jumpAct + "' ;; l*) echo '" + resumeAct + "' ;; esac",
		}
		for _, r := range [][3]int{{1, 2, 3}, {4, 5, 6}, {7, 8, 9}} {
			var bs []string
			for _, n := range r {
				bs = append(bs, fmt.Sprintf("%d:transform(sed -n %dp '%s.keys')", n, n, base))
			}
			args = append(args, "--bind", strings.Join(bs, ","))
		}
		args = append(args,
			"--bind", "n:"+newAct,
			// Its card in a popup over the board, as the menus open it.
			"--bind", "x:"+closeAct,
			"--bind", "a:"+adoptAct,
			"--bind", "v,e,ctrl-e:transform:case {1} in a*) echo '"+editAct+"' ;; esac",
			"--bind", "s:transform("+q+" _view screen)",
			// Tab and shift-tab: the next and previous view, as a tab bar.
			"--bind", "tab:"+nextAct, "--bind", "btab:"+prevAct,
			"--bind", "?:transform("+q+" _view help)",
			// The footer's hints are buttons; a right click, the row's menu.
			"--bind", "click-footer:transform("+q+" _click board)",
			"--bind", "click-header:transform("+q+" _click title)",
			"--bind", "right-click:"+menuAct,
			"--bind", "ctrl-r:reload-sync("+q+" _rows)+refresh-preview",
			"--bind", "/:show-input+unbind("+lsKeys+")+transform("+q+" _hf)",
			"--bind", `esc:clear-query+transform{if [ "$FZF_INPUT_STATE" = hidden ]; then echo '`+quitAct+`'; else echo 'hide-input+rebind(`+lsKeys+`)+transform(`+q+` _hf)'; fi}`)
		if peek != "" {
			// Alt y again closes it, as a toggle; Alt N and Alt 1 work in it
			// as they do anywhere. It opens on the thread it is over.
			args = append(args, "--bind", "alt-y:abort", "--bind", "alt-N,alt-n:"+newAct, "--bind", "alt-1:print(alt-1)+accept")
			if i := rowOf(rows, "a"+at); at != "" && i > 0 {
				args = append(args, "--sync", "--bind", "start:pos("+strconv.Itoa(i)+")")
			}
		}
		c := fzfCmd(args)
		c.Stdin = strings.NewReader(rows)
		c.Stderr = os.Stderr
		outb, err := c.Output()
		rc := 0
		if x, ok := err.(*exec.ExitError); ok {
			rc = x.ExitCode()
		}
		out := strings.TrimRight(string(outb), "\n")
		clean()
		if home {
			// The home board does not end: ctrl-c starts it again. Not
			// without its terminal (the pane or the session closed).
			if rc == 2 {
				return "", nil
			}
			t, err := os.Open("/dev/tty")
			if err != nil {
				return "", nil
			}
			t.Close()
			// A newer weaver is installed (_chrome ended the board for it):
			// the board starts again in it, whose keys know its rows.
			if newer(e.Self, base+".born") {
				os.Remove(base + ".born")
				syscall.Exec(e.Self, []string{e.Self, "ls"}, os.Environ())
			}
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if out == "" {
			return "", nil
		}
		// A key fzf printed (print), then the item; the key alone on an
		// empty board; a bare item when fzf accepted on its own.
		key, sel := "", ""
		if k, s, ok := strings.Cut(out, "\n"); ok {
			key, sel = k, s
		} else if slices.Contains(boardKeys, out) {
			key = out
		} else {
			key, sel = "jump", out
		}
		if peek != "" {
			step, cmds := peekChoice(key, sel)
			return step, writePeek(peek, cmds)
		}
		pane, tab := "", ""
		if sel != "" && (sel[0] == 'a' || sel[0] == 'l') {
			if sel[0] == 'l' {
				key = "resume"
			}
			f := strings.Split(sel, "\t")
			if len(f) > 2 {
				pane, tab = f[1], f[2]
			}
		}
		switch key {
		case "resume":
			// A lost thread: back in a new window (where you land).
			if pane != "" {
				exec.Command(e.Self, "resume", pane).Run()
			}
			return "", nil
		case "new":
			a := []string{"new"}
			if pane != "" {
				a = append(a, "--from", pane)
			}
			c := exec.Command(e.Self, a...)
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
			err := c.Run()
			rc := 0
			if x, ok := err.(*exec.ExitError); ok {
				rc = x.ExitCode()
			}
			if rc == 0 {
				return "", nil
			}
			if rc != 130 {
				time.Sleep(1500 * time.Millisecond)
			}
			continue
		}
		if pane == "" {
			return "", nil
		}
		exec.Command(e.Self, "_goto", pane, tab).Run()
		if key == "ctrl-e" {
			syscall.Exec(e.Self, []string{e.Self, "edit", pane}, os.Environ())
		}
		return "", nil
	}
}

// boardKeys are the keys a board prints alone, on an empty board.
var boardKeys = []string{"new", "jump", "ctrl-e", "adopt", "next", "prev", "alt-1"}

// rowOf is the line (from 1) of the board's rows whose item id is id; 0
// when none is.
func rowOf(rows, id string) int {
	for i, l := range strings.Split(rows, "\n") {
		if f, _, _ := strings.Cut(l, "\t"); f == id {
			return i + 1
		}
	}
	return 0
}

// peekChoice is what key, pressed on the board's line sel ("" on an empty
// board) in Alt y's popover, asks for: tab and shift-tab's step to the
// views beside it ("next", "prev"), or the wvr commands to run once the
// popover has closed, which it would be in the way of.
func peekChoice(key, sel string) (string, [][]string) {
	kind, id, tab := "", "", ""
	if f := strings.Split(sel, "\t"); sel != "" && len(f) > 2 {
		kind, id, tab = sel[:1], f[1], f[2]
	}
	agentRow, lost := kind == "a", kind == "l"
	switch key {
	case "next", "prev":
		return key, nil
	case "alt-1":
		return "", [][]string{{"next"}}
	case "new":
		if agentRow {
			return "", [][]string{{"_popup", "new", "--from", id}}
		}
		return "", [][]string{{"_popup", "new"}}
	case "adopt":
		if agentRow {
			return "", [][]string{{"_popup", "adopt", id}}
		}
		return "", [][]string{{"_popup", "adopt"}}
	case "close":
		switch {
		case agentRow:
			return "", [][]string{{"_popup", "close", id}}
		case lost:
			return "", [][]string{{"_popup", "forget", id}}
		}
	case "ctrl-e":
		if agentRow {
			return "", [][]string{{"_goto", id, tab}, {"edit", id}}
		}
	case "jump", "resume":
		switch {
		case agentRow:
			return "", [][]string{{"_goto", id, tab}}
		case lost:
			return "", [][]string{{"resume", id}}
		}
	}
	return "", nil
}

// writePeek writes cmds to the popover's file f, one a line, its words
// tab-separated.
func writePeek(f string, cmds [][]string) error {
	var b strings.Builder
	for _, c := range cmds {
		b.WriteString(strings.Join(c, "\t") + "\n")
	}
	return os.WriteFile(f, []byte(b.String()), 0o600)
}
