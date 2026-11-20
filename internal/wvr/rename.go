package wvr

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/robbowland/weaver/internal/cli"
	"github.com/robbowland/weaver/internal/git"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/oneline"
	"github.com/robbowland/weaver/internal/store"
)

// Rename is `wvr rename [PANE] [NAME...]`: the thread of the agent in PANE
// (else the one this pane belongs to: the agent's, or its review panel's)
// called NAME on the board and on its window, in place of the
// title its agent gives it; asked for when not given. An empty name gives
// the agent's title back.
func (e *Env) Rename(args []string) error {
	if err := e.toWeaverSession(); err != nil {
		return err
	}
	p, args, err := e.leadingPane(args)
	if err != nil {
		return err
	}
	if p == "" && e.MyPane != "" {
		p = e.agentOf(e.MyPane)
	}
	if p == "" {
		return errors.New("usage: wvr rename [pane-id] [name]")
	}
	cur, ok := e.shownName(p)
	if !ok {
		return errNotThread
	}
	name := strings.Join(args, " ")
	if len(args) == 0 {
		n, ok := askName(formLabel("RENAME"), cur, "its agent's title", "its agent's title",
			CGrey+"⏎"+COff+" "+CDim+"RENAME"+COff+"   "+CGrey+"esc"+COff+" "+CDim+"CANCEL"+COff)
		if !ok {
			return cli.ErrCancelled
		}
		name = n
	}
	return e.retitle(p, oneline.Line(strings.TrimSpace(name)))
}

// AgentTitle is `wvr title TITLE...`, which a thread's agent runs in its
// pane: its thread called TITLE on the board, its window and Rex's
// sidebar, unless you named it, which it keeps. No pane argument: a title
// may start with a number.
func (e *Env) AgentTitle(args []string) error {
	if err := e.toWeaverSession(); err != nil {
		return err
	}
	var p mux.PaneID
	if e.MyPane != "" {
		p = e.agentOf(e.MyPane)
	}
	title := oneline.Line(strings.TrimSpace(strings.Join(args, " ")))
	if title == "" {
		return errors.New("usage: wvr title title (in a thread's agent)")
	}
	if p == "" {
		return errors.New("wvr title names the thread of the agent it runs in: run it there")
	}
	th, err := store.ReadThread(e.ThreadFile(p))
	if err != nil {
		return errNotThread
	}
	if th.Title != "" {
		fmt.Printf("weaver: you named this thread %q: it keeps your name\n", th.Title)
		return nil
	}
	if !store.UpdateThread(e.ThreadFile(p), func(th *store.Thread) { th.AgentTitle = title }) {
		return errors.New("cannot write its thread record")
	}
	e.PushStatusAndLabels()
	fmt.Printf("weaver: this thread is now called %q\n", title)
	return nil
}

// RenameBranch is `wvr branch [PANE] NAME`: the branch of the thread of
// the agent in PANE (else the one this pane belongs to) renamed NAME
// (renameBranch), then the windows relabelled and the boards reloaded.
func (e *Env) RenameBranch(args []string) error {
	if err := e.toWeaverSession(); err != nil {
		return err
	}
	var p mux.PaneID
	switch {
	case len(args) == 2:
		q, ok := e.paneArg(args[0])
		if !ok {
			return errNoPane(args[0])
		}
		p = q
	case len(args) == 1 && e.MyPane != "":
		p = e.agentOf(e.MyPane)
	}
	if p == "" {
		return errors.New("usage: wvr branch [pane-id] name")
	}
	name := args[len(args)-1]
	old, err := e.renameBranch(p, name)
	if err != nil {
		return err
	}
	e.PushStatusAndLabels()
	fmt.Printf("weaver: %s is now %s\n", old, name)
	return nil
}

// renameBranch renames the branch of the thread whose agent is pane p to
// name, in git and in its record, which the board, its close card and the
// views go by: the name it had. Its worktree keeps its directory, which
// its agent works in. Refused once the remote has the branch: a pull
// request on it keeps the old name.
func (e *Env) renameBranch(p mux.PaneID, name string) (string, error) {
	th, err := store.ReadThread(e.ThreadFile(p))
	if err != nil {
		return "", errNotThread
	}
	old := th.Branch
	if rm := git.PreferredRemote(th.Repo); rm != "" && git.HasRemoteBranch(th.Repo, rm, old) {
		return "", fmt.Errorf("%s is on %s already: a pull request on it would keep the old name", old, rm)
	}
	if out, err := exec.Command("git", "-C", th.Worktree, "branch", "-m", old, name).CombinedOutput(); err != nil {
		return "", errors.New(strings.TrimSpace(string(out)))
	}
	if !store.UpdateThread(e.ThreadFile(p), func(th *store.Thread) {
		th.Branch = name
		if th.Name == old {
			th.Name = name
		}
	}) {
		return "", errors.New("renamed in git, but cannot write its thread record")
	}
	return old, nil
}

var errNotThread = errors.New("its agent is no thread: adopt it first (wvr adopt)")

// shownName is what the board calls the thread of the agent in pane p now
// (store.Thread.Shown); false when the agent is no thread.
func (e *Env) shownName(p mux.PaneID) (string, bool) {
	th, err := store.ReadThread(e.ThreadFile(p))
	if err != nil {
		return "", false
	}
	st, _ := store.ReadState(e.StateFile(p))
	return oneline.Line(th.Shown(st.Title)), true
}

// retitle sets the title of the thread whose agent is pane p ("" for none:
// its agent's own), and relabels its window and the boards.
func (e *Env) retitle(p mux.PaneID, title string) error {
	if !store.UpdateThread(e.ThreadFile(p), func(th *store.Thread) { th.Title = title }) {
		return errors.New("cannot write its thread record")
	}
	e.PushStatusAndLabels()
	return nil
}

// askName asks for a thread's name on the terminal, under border label l,
// starting from cur; ghost shows when it is empty, and empty says what
// that gives: what was typed, and false when cancelled.
func askName(l, cur, ghost, empty, footer string) (string, bool) {
	args := append([]string{"--disabled", "--reverse"}, formFzf...)
	args = append(args, "--border-label", l, "--prompt", formPrompt("name"), "--query", cur,
		"--ghost", ghost, "--color", FzfColors,
		"--header", "        "+CGrey+"what the board and its window call it; empty, "+empty+COff,
		"--footer", footer, "--bind", "enter:print-query")
	c := fzfCmd(args, "SHELL=/bin/sh")
	c.Stderr = os.Stderr
	b, err := c.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimRight(string(b), "\n"), true
}
