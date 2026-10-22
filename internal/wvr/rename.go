package wvr

import (
	"errors"
	"os"
	"strings"

	"github.com/robbowland/weaver/internal/cli"
	"github.com/robbowland/weaver/internal/oneline"
	"github.com/robbowland/weaver/internal/store"
	"github.com/robbowland/weaver/internal/tmux"
)

// Rename is `wvr rename [PANE] [NAME...]`: the thread of the agent in PANE
// (else the one this pane belongs to: the agent's, or its nvim's) called
// NAME on the board, on its window and on the status line, in place of the
// title its agent gives it; asked for when not given. An empty name gives
// the agent's title back.
func (e *Env) Rename(args []string) error {
	if err := e.toWeaverSession(); err != nil {
		return err
	}
	var p tmux.PaneID
	if len(args) > 0 {
		if q, ok := paneArg(args[0]); ok {
			p, args = q, args[1:]
		}
	}
	if p == "" && e.MyPane != "" {
		p = e.agentOf(e.MyPane)
	}
	if p == "" {
		return errors.New("usage: wvr rename [pane-id] [name]")
	}
	th, err := store.ReadThread(e.ThreadFile(p))
	if err != nil {
		return errors.New("its agent is no thread: adopt it first (wvr adopt)")
	}
	name := strings.Join(args, " ")
	if len(args) == 0 {
		// What the board calls it now: your name, its agent's title, else
		// its task (or the item it started from).
		cur := th.Title
		if cur == "" {
			st, _ := store.ReadState(e.StateFile(p))
			cur = st.Title
		}
		if cur == "" {
			cur = oneline.Line(strings.SplitN(th.About(), "\n", 2)[0])
		}
		n, ok := askName(formLabel("WEAVER", "RENAME"), cur, "its agent's title", "its agent's title",
			CGrey+"⏎"+COff+" "+CDim+"RENAME"+COff+"   "+CGrey+"esc"+COff+" "+CDim+"CANCEL"+COff)
		if !ok {
			return cli.ErrCancelled
		}
		name = n
	}
	return e.retitle(p, oneline.Line(strings.TrimSpace(name)))
}

// retitle sets the title of the thread whose agent is pane p ("" for none:
// its agent's own), and relabels its window and the boards.
func (e *Env) retitle(p tmux.PaneID, title string) error {
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
