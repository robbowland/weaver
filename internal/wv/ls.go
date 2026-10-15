package wv

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// FzfColors are the dashboards' fzf colours (the script's FZF_COLORS).
const FzfColors = "fg:-1,bg:-1,hl:-1:underline,fg+:#000000:strip,bg+:#ffffff,hl+:#000000:underline,gutter:-1," +
	"pointer:#000000,marker:#ffffff,header:-1,footer:#616161,border:#616161,preview-border:#616161,query:#ffffff," +
	"prompt:#999999,info:#616161,ghost:#616161,spinner:#616161,separator:#616161,label:#999999"

// lsKeys are the dashboard's keys while browsing; searching (/) unbinds
// them so they type.
const lsKeys = "j,k,g,G,n,x,e,s,q,?,/,1,2,3,4,5,6,7,8,9"

// Quote is a word for sh, as bash's printf %q would make it usable.
func Quote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789/._-+=:,@%") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func isTTY(fd int) bool {
	_, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	return err == nil
}

// Home is the home tab's board (`weaver home`): the dashboard, full-size and
// for good. What the session needs as it starts (old states swept, lost
// threads back, the status line's counts and the tabs' labels) runs behind
// it, in the script, and reloads it when done.
func (e *Env) Home() int {
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

// Ls is the dashboard (`weaver ls`): fzf over the session's agents, which
// hooks reload through its socket as they change. Without fzf or a terminal,
// the board as text. The home board ($LS_HOME) stays: its keys act and it
// carries on; any other ends with a key, and acts.
func (e *Env) Ls() int {
	home := os.Getenv("LS_HOME") != ""
	if _, err := exec.LookPath("fzf"); err != nil || !isTTY(1) {
		fmt.Print(e.Rows(true))
		return 0
	}
	q := Quote(e.Self)
	sh := "/bin/sh"
	if st, err := os.Stat("/bin/dash"); err == nil && st.Mode()&0o111 != 0 {
		sh = "/bin/dash"
	}
	var jumpAct, editAct, newAct, quitAct, resumeAct string
	if home {
		jumpAct = "execute-silent(" + q + " _goto {2} {3})"
		editAct = "execute-silent(" + q + " _goto {2} {3}; " + q + " edit {2})"
		// fzf skips any action whose template names a field, such as {2}, on
		// an empty board: this one prints the field for the action it returns.
		newAct = "transform:if [ \"$FZF_TOTAL_COUNT\" = 0 ]; then echo 'execute(" + q + " new)+reload(" + q +
			" _rows)'; else printf '%s2})+reload(%s _rows)\\n' 'execute(" + q + " new --from {' '" + q + "'; fi"
		// q and esc: back to the thread you came from, as Alt y does.
		quitAct = "execute-silent(" + q + " go home)"
		resumeAct = "execute(" + q + " resume {2})+reload(" + q + " _rows)"
	} else {
		jumpAct, editAct, newAct, quitAct, resumeAct = "print(jump)+accept", "print(ctrl-e)+accept", "print(new)+accept",
			"abort", "print(resume)+accept"
	}
	os.Setenv("LS_JUMP", jumpAct)
	if st, err := os.Stat(e.SockDir); err != nil || !st.IsDir() {
		os.MkdirAll(e.SockDir, 0o700)
		os.Chmod(e.SockDir, 0o700)
	}
	// Hooks reload this dashboard through its socket as agents change; its
	// files go with it, also when its pane is closed under it.
	base := e.SockDir + "/" + e.SKey + ".fzf." + strconv.Itoa(os.Getpid())
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
	}
	for {
		for _, f := range side {
			if exists(f) {
				os.Remove(f)
			}
		}
		os.Setenv("FZF_SOCK", sock)
		rows := e.Rows(false)
		items := strings.Count(rows, "\n")
		if items == 0 {
			os.Setenv("LS_EMPTY", "1")
		}
		foot := strings.TrimRight(Footer(), "\n")
		os.Unsetenv("LS_EMPTY")
		header := strings.TrimRight(Title(), "\n")
		os.Unsetenv("FZF_SOCK")
		pw, _ := firstLine(base + ".room")
		label, _ := firstLine(base + ".label")
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
			"--no-scrollbar", "--footer", foot, "--footer-border", "none",
			"--color", FzfColors,
			"--prompt", "/ ", "--ghost", "search threads", "--info", "hidden", "--no-separator",
			"--preview", q + " _preview {2} {6} {1}", "--preview-window", pw,
			"--bind", "resize:reload(LS_REMEASURE=1 " + q + " _rows)", "--bind", "load:transform(" + q + " _chrome)",
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
			"--bind", "x:transform:case {1} in a*) echo 'execute("+q+" close {2})+reload("+q+" _rows)' ;; l*) echo 'execute("+q+" resume --forget {2})+reload("+q+" _rows)' ;; esac",
			"--bind", "e,ctrl-e:transform:case {1} in a*) echo '"+editAct+"' ;; esac",
			"--bind", "s,tab:transform("+q+" _view screen)",
			"--bind", "?:transform("+q+" _view help)",
			"--bind", "ctrl-r:reload("+q+" _rows)+refresh-preview",
			"--bind", "/:show-input+unbind("+lsKeys+")+transform("+q+" _hf)",
			"--bind", `esc:clear-query+transform{if [ "$FZF_INPUT_STATE" = hidden ]; then echo '`+quitAct+`'; else echo 'hide-input+rebind(`+lsKeys+`)+transform(`+q+` _hf)'; fi}`)
		c := exec.Command("fzf", args...)
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
				return 0
			}
			t, err := os.Open("/dev/tty")
			if err != nil {
				return 0
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
			return 0
		}
		// A key fzf printed (print), then the item; the key alone on an
		// empty board; a bare item when fzf accepted on its own.
		key, sel := "", ""
		if k, s, ok := strings.Cut(out, "\n"); ok {
			key, sel = k, s
		} else if out == "new" || out == "jump" || out == "ctrl-e" {
			key = out
		} else {
			key, sel = "jump", out
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
			// A lost thread: back in a new tab (where you land).
			if pane != "" {
				exec.Command(e.Self, "resume", pane).Run()
			}
			return 0
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
				return 0
			}
			if rc != 130 {
				time.Sleep(1500 * time.Millisecond)
			}
			continue
		}
		if pane == "" {
			return 0
		}
		exec.Command(e.Self, "_goto", pane, tab).Run()
		if key == "ctrl-e" {
			syscall.Exec(e.Self, []string{e.Self, "edit", pane}, os.Environ())
		}
		return 0
	}
}
