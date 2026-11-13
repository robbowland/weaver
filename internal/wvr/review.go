package wvr

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/robbowland/weaver/internal/cli"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/git"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/process"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
)

// The agent's review panel: `wvr review` (alt+v, v on the board) opens it
// beside the agent, or goes to it; `wvr _panel` is what runs in it, tuicr
// on the branch.

// openLeftOf opens a pane running argv left of pane target, in its window;
// the new pane.
func (e *Env) openLeftOf(target mux.PaneID, cwd, name string, argv ...string) (mux.PaneID, error) {
	return e.Mux().SplitLeft(target, mux.NewSplit{Spawn: mux.Spawn{Argv: argv, Dir: cwd}, Title: name})
}

// ThreadWidth is `wvr _width AGENT` (settleWidths' passes): threadWidth,
// of a pane as given, unless you moved its split since (Rex's app evens a
// split out, and weaver sets it back):
// then it is yours.
func (e *Env) ThreadWidth(agent string) {
	p, ok := mux.ParsePane(agent)
	if !ok {
		return
	}
	if e.Mux().Moved(p) {
		return
	}
	e.threadWidth(p)
}

// threadWidth: back to a thread's split once its panel reopens beside the
// agent: the agent at $WEAVER_AGENT_WIDTH on the right, while the panel
// and the agent are the window's only panes.
func (e *Env) threadWidth(agent mux.PaneID) {
	ps, _ := e.Mux().Panes()
	ap, ok := session.PaneIn(ps, agent)
	if !ok || len(windowPanes(ps, ap.Window)) != 2 {
		return
	}
	total, ok := e.windowWidth(ap.Window)
	if !ok {
		return
	}
	e.Mux().SetWidth(agent, agentCols(total))
}

// settleWidths puts thread agent's window back to its widths (ThreadWidth)
// once the Rex app has evened out its panes: it does that, within a second
// or so and with nothing to say when, to the window it shows whenever its
// panes change (a pane split in, or put in another's place). Detached, so
// what asked goes on at once; twice, for an app slow to it.
func (e *Env) settleWidths(agent mux.PaneID) {
	c := exec.Command("/bin/sh", "-c", `sleep 1; "$0" _width "$1"; sleep 1.5; exec "$0" _width "$1"`, e.Self, string(agent))
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	bestEffort(c.Start())
}

// panelLock is one `wvr review` per agent at a time, so a second alt+v
// finds the panel the first is opening (store.Lock: one whose holder has
// gone is taken over).
func (e *Env) panelLock(agent mux.PaneID) func() {
	bestEffort(os.MkdirAll(e.StateDir, 0o755))
	return store.Lock(e.PaneFile(agent, "opening"))
}

// focusedPane is the pane you are on (a popup or a key's wvr is not a
// pane: the session's active one).
func (e *Env) focusedPane() mux.PaneID {
	p, _ := e.Mux().Active()
	if p == e.MyPane {
		return ""
	}
	return p
}

// agentWhere is the worktree the agent in pane p is in (its state's root,
// else its directory, else its thread's worktree); false when weaver knows
// of no worktree it is in that is there. Its review panel opens in that worktree, so it is never a
// guess.
func (e *Env) agentWhere(p mux.PaneID) (root string, known bool) {
	if s, err := store.ReadState(e.StateFile(p)); err == nil {
		root = cmp.Or(s.Root, s.Cwd)
	} else if t, err := store.ReadThread(e.ThreadFile(p)); err == nil {
		root = t.Worktree
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return "", false
	}
	return root, true
}

// Review is `wvr review [--full] [PANE]` (alt+v, v on the board): go to the
// review panel of the focused agent (or PANE's), or open one on its left in
// the worktree it is in; in a pane that is no agent's, a panel beside it in
// its directory. --full: the panel fills the window.
func (e *Env) Review(args []string) error {
	full, target := false, mux.PaneID("")
	for _, a := range args {
		switch {
		case a == "--full":
			full = true
		case strings.HasPrefix(a, "-"):
			return errors.New("usage: wvr review [--full] [pane-id]")
		default:
			target, _ = mux.ParsePane(a)
		}
	}
	release := e.uiLock()
	defer release()
	panes, ok := e.Panes()
	if !ok {
		return errors.New("could not list the panes: Rex did not answer")
	}
	if target == "" {
		if target = e.focusedPane(); target == "" {
			return errors.New("cannot find the focused pane")
		}
	}
	agentPane := e.agentOf(target)
	cwd := ""
	if p, ok := session.PaneIn(panes, target); ok {
		cwd = p.Cwd
	}
	if st, err := os.Stat(cwd); cwd == "" || err != nil || !st.IsDir() {
		cwd, _ = os.Getwd()
	}
	if agentPane == "" {
		rp, err := e.openLeftOf(target, cwd, panelTitle(cwd), e.Self, "_panel")
		if err == nil && full {
			e.Mux().Zoom(rp)
		}
		return err
	}
	e.MarkSeen(agentPane)
	root, known := e.agentWhere(agentPane)
	if !known {
		root = cwd
	}
	unlock := e.panelLock(agentPane)
	defer unlock()
	// Fresh, now that no other press is opening a panel for this agent.
	panes, _ = e.Panes()
	rp := e.PanelPane(agentPane)
	if p, ok := session.PaneIn(panes, rp); rp == "" || !ok || p.Exited {
		var err error
		if rp, err = e.openLeftOf(agentPane, root, panelTitle(root), e.Self, "_panel", string(agentPane)); err != nil {
			return err
		}
		e.bindPanel(agentPane, rp, 0)
		// A thread's window gets its split back.
		if files.Exists(e.ThreadFile(agentPane)) {
			e.threadWidth(agentPane)
			e.settleWidths(agentPane)
		}
	} else {
		e.focus(rp)
	}
	if full {
		e.Mux().Zoom(rp)
	}
	return nil
}

// panelTitle is the title of a review panel in dir.
func panelTitle(dir string) string { return "review: " + filepath.Base(dir) }

// Panel is `wvr _panel [AGENT]`, what runs in a review panel, in the
// worktree it reviews: bound to the agent (its pane and pid, as it starts),
// then tuicr ($WEAVER_TUICR, else tuicr) on the branch, started again
// whenever it ends (q), so it reads the branch afresh: tuicr takes its range
// once, as it starts, and a commit made while it is open joins its commit
// list only then. It ends with its pane.
func (e *Env) Panel(args []string) error {
	a, ok := mux.ParsePane(arg(args, 0))
	if ok && e.MyPane != "" {
		e.bindPanel(a, e.MyPane, os.Getpid())
	}
	tuicr, err := exec.LookPath(cmp.Or(os.Getenv("WEAVER_TUICR"), "tuicr"))
	if err != nil {
		fmt.Println("weaver: tuicr is not installed (wvr doctor --fix installs it)")
		_, _ = os.Stdin.Read(make([]byte, 1))
		return &cli.Exit{Code: 1}
	}
	dir, _ := os.Getwd()
	// tuicr's e opens $EDITOR: nvim, whatever the pane's environment says
	// (Rex runs a key's command without your shell's).
	env := append(os.Environ(), "EDITOR=nvim")
	// ctrl+c is tuicr's while it runs, never the loop's.
	sig := make(chan os.Signal, 1)
	for {
		args := reviewArgs(dir, e.reviewBase(a))
		if args == nil {
			fmt.Print("\033[2J\033[H" + CGrey + "no changes on this branch yet" + COff + "\n")
			time.Sleep(2 * time.Second)
			continue
		}
		was := spanOf(dir, e.reviewBase(a))
		c := exec.Command(tuicr, args...)
		c.Dir, c.Env, c.Stdin, c.Stdout, c.Stderr = dir, env, os.Stdin, os.Stdout, os.Stderr
		signal.Notify(sig, os.Interrupt, syscall.SIGQUIT)
		start := time.Now()
		ended, err := untilRewritten(c, rewriteCheck(dir, func() string { return e.reviewBase(a) }, was))
		signal.Stop(sig)
		if ended {
			// Out of tuicr's screen, the terminal as it was, and at once
			// on the range as it is now.
			fmt.Print("\033[?1049l\033[?25h")
			stty := exec.Command("stty", "sane")
			stty.Stdin = os.Stdin
			_ = stty.Run()
			continue
		}
		// One that ends at once (failing, or a q at once) waits out a
		// second, or two when it failed: never a spin.
		wait := time.Second
		if err != nil {
			wait = 2 * time.Second
		}
		time.Sleep(wait - time.Since(start))
	}
}

// span is a review's range as tuicr was launched on it: HEAD and the fork
// point (tuicr takes its range once, as it starts).
type span struct{ head, fork string }

func spanOf(dir, base string) span {
	h, _ := git.Line(dir, "rev-parse", "--verify", "--quiet", "HEAD")
	f, _ := git.ForkPoint(dir, base)
	return span{h, f}
}

// rewritten: has the branch moved from s to now so that a review launched
// on s shows it wrongly? Its fork point moved (a rebase onto a newer base,
// the base merged in, its base changed), or HEAD is no longer s's HEAD nor
// after it (an amend, a squash, a reset). New commits on top are not: q
// picks them up, and a review ended for them would drop a comment half
// typed.
func (s span) rewritten(dir string, now span) bool {
	if now.fork != s.fork {
		return true
	}
	return now.head != s.head && !git.OK(dir, "merge-base", "--is-ancestor", s.head, now.head)
}

// rewriteCheck is untilRewritten's look, once a second, at whether the
// range was rewritten since was: worked out with git (span.rewritten)
// only when the files it is worked out from have changed (git.Stamp, and
// the thread's base), or every tenth look all the same (a ref nested in
// a directory of its own that the stamp does not see). A panel per thread
// ran three gits a second, every second, each one's review open.
func rewriteCheck(dir string, base func() string, was span) func() bool {
	stamp, b, looks := git.Stamp(dir), base(), 0
	return func() bool {
		looks++
		now, nb := git.Stamp(dir), base()
		if now == stamp && nb == b && now != "" && looks%10 != 0 {
			return false
		}
		stamp, b = now, nb
		return was.rewritten(dir, spanOf(dir, nb))
	}
}

// untilRewritten runs c until it ends, looking every second whether its
// range was rewritten (moved), and ending it when it was: whether it was
// ended for that, and how it ended. Never while an nvim runs under it
// (tuicr's e): ended, tuicr would leave it reading the terminal beside
// the next tuicr, its edits unsaved; the end waits for it.
func untilRewritten(c *exec.Cmd, moved func() bool) (bool, error) {
	if err := c.Start(); err != nil {
		return false, err
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	ended := false
	for {
		select {
		case err := <-done:
			return ended, err
		case <-tick.C:
			if moved() && !process.Runs(c.Process.Pid, "nvim") {
				ended = true
				_ = c.Process.Signal(syscall.SIGTERM)
			}
		}
	}
}

// reviewArgs are tuicr's arguments for the branch in dir as it is now: its
// commits since it left base, with what is not committed yet (`-r
// FORK..HEAD -w`, FORK the fork point's SHA: git.ForkPoint), which Rob's
// fork of tuicr opens on the work tree while the branch has no commits of
// its own; with no base, what is not committed (`-w`), else tuicr's own
// pick of a commit. nil with nothing at all, no commits since the fork
// point and nothing uncommitted: the one review tuicr refuses ("No changes
// to review").
func reviewArgs(dir, base string) []string {
	look := []string{"--theme", "micrographics", "--no-update-check"}
	head, _ := git.Line(dir, "rev-parse", "--verify", "--quiet", "HEAD")
	fork, hasBase := git.ForkPoint(dir, base)
	uncommitted := dirty(dir)
	switch {
	case !uncommitted && (head == "" || hasBase && head == fork):
		return nil
	case hasBase:
		return append([]string{"-r", fork + "..HEAD", "-w"}, look...)
	case uncommitted:
		return append([]string{"-w"}, look...)
	}
	return look
}

// reviewBase is the branch agent's thread is reviewed against, as its
// record says now (a pull request's follows its base): "" for none, the
// default branch.
func (e *Env) reviewBase(agent mux.PaneID) string {
	th, _ := store.ReadThread(e.ThreadFile(agent))
	return th.Base
}

// dirty: has the work tree at dir changes not committed, or files git does
// not track (and does not ignore)?
func dirty(dir string) bool {
	out, err := git.Output(dir, nil, "", "status", "--porcelain")
	return err == nil && out != ""
}

// bindPanel records review panel rp (its process pid) as agent's:
// <skey>_<agent>.panel. A pid of 1 or less is Rex not saying: the pane
// alone, never over the record the panel made of itself in that pane.
func (e *Env) bindPanel(agent, rp mux.PaneID, pid int) {
	f := e.PaneFile(agent, "panel")
	n := ""
	if pid > 1 {
		n = strconv.Itoa(pid)
	} else if l, err := files.FirstLine(f); err == nil && l == string(rp) {
		return
	}
	bestEffort(files.WriteAtomic(f, []byte(string(rp)+"\n"+n+"\n")))
}
