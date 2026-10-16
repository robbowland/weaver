// weaver keeps track of the coding agents running in your tmux session and
// opens Neovim in the worktree each is really editing.
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/robbowland/weaver/internal/cli"
	"github.com/robbowland/weaver/internal/setup"
	"github.com/robbowland/weaver/internal/wvr"
)

func main() {
	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	// tmux's status line asks for the counts on every redraw: no more than a
	// file read.
	if cmd == "status" && len(args) >= 1 && args[0] == "--tmux" {
		fmt.Println(wvr.StatusLine(args))
		return
	}
	switch cmd {
	case "help", "-h", "--help":
		fmt.Print(wvr.Usage)
		return
	case "version", "--version":
		fmt.Println("weaver " + buildVersion())
		return
	case "setup":
		exit(setup.Run(args, wvr.Load().Self, os.Stdout))
	case "_plugins":
		exit(setup.Plugins())
	case "_runtime":
		// install.sh: unpack this build's files now, so Neovim configs find
		// its plugin before weaver next runs.
		_, err := setup.Runtime()
		exit(err)
	}
	c, ok := commands[cmd]
	if !ok {
		fmt.Fprintf(os.Stderr, "weaver: unknown command: %s\n\n%s", cmd, wvr.Usage)
		os.Exit(2)
	}
	e := wvr.Load()
	if c.session && e.Session == "" {
		exit(errNoSession)
	}
	exit(c.run(e, args))
}

// exit ends wvr as err says: nothing, the usage, or what went wrong (said
// once, here, unless the command has said it already).
func exit(err error) {
	var x *cli.Exit
	switch {
	case err == nil:
		os.Exit(0)
	case errors.Is(err, cli.ErrHelp):
		fmt.Print(wvr.Usage)
		os.Exit(0)
	case errors.As(err, &x):
		if x.Err != nil {
			fmt.Fprintln(os.Stderr, "weaver: "+x.Err.Error())
		}
		os.Exit(x.Code)
	}
	fmt.Fprintln(os.Stderr, "weaver: "+err.Error())
	os.Exit(1)
}

var errNoSession = errors.New("not inside a tmux session (start one with `wvr`)")

// command is one of wvr's commands: whether it runs only inside a weaver
// tmux session, and what it does with its arguments.
type command struct {
	session bool
	run     func(e *wvr.Env, args []string) error
}

// arg is argument i, or "".
func arg(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}

// done is a command that cannot fail.
func done(f func(e *wvr.Env, args []string)) func(*wvr.Env, []string) error {
	return func(e *wvr.Env, args []string) error {
		f(e, args)
		return nil
	}
}

// prints is a command that prints what f makes.
func prints(f func(e *wvr.Env, args []string) string) func(*wvr.Env, []string) error {
	return done(func(e *wvr.Env, args []string) { fmt.Print(f(e, args)) })
}

// helped is a command that shows the usage for -h or --help.
func helped(f func(e *wvr.Env, args []string) error) func(*wvr.Env, []string) error {
	return func(e *wvr.Env, args []string) error {
		if slices.Contains(args, "-h") || slices.Contains(args, "--help") {
			return cli.ErrHelp
		}
		return f(e, args)
	}
}

// The commands (wvr help, and the ones tmux, fzf, nvim and the hooks run,
// named with a leading _).
var commands = map[string]command{
	// Outside a session it starts (or attaches to) one; inside, the board.
	"": {run: start}, "start": {run: start},
	"ls":   {session: true, run: func(e *wvr.Env, _ []string) error { return e.Ls() }},
	"home": {session: true, run: func(e *wvr.Env, _ []string) error { return e.Home() }},

	"new":    {run: (*wvr.Env).New},
	"edit":   {session: true, run: (*wvr.Env).Edit},
	"nvim":   {session: true, run: (*wvr.Env).Nvim},
	"close":  {session: true, run: (*wvr.Env).Close},
	"send":   {session: true, run: (*wvr.Env).Send},
	"view":   {session: true, run: (*wvr.Env).View},
	"resume": {run: helped((*wvr.Env).Resume)},
	"quit":   {run: helped((*wvr.Env).Quit)},
	"next":   {session: true, run: done(func(e *wvr.Env, _ []string) { e.Next() })},
	"go": {run: func(e *wvr.Env, args []string) error {
		if arg(args, 0) != "home" {
			return errors.New("usage: wvr go home")
		}
		if e.Session == "" {
			return errNoSession
		}
		e.GoHome()
		return nil
	}},
	"status": {session: true, run: done(func(e *wvr.Env, args []string) {
		if arg(args, 0) == "--push" {
			e.PushStatus()
		} else {
			fmt.Println(e.StatusText())
		}
	})},
	"gc": {run: done(func(e *wvr.Env, _ []string) { e.GC() })},

	// The agents' hooks, and other agents' events.
	"hook":  {run: done((*wvr.Env).HookMain)},
	"event": {run: func(e *wvr.Env, args []string) error { return e.Event("", slices.Contains(args, "--bell")) }},
	"adapt": {run: func(e *wvr.Env, args []string) error {
		if arg(args, 0) != "codex" {
			return errors.New("usage: wvr adapt codex [--bell]")
		}
		return e.Event("codex", slices.Contains(args[1:], "--bell"))
	}},
	"snapshot": {run: func(_ *wvr.Env, args []string) error {
		c, msg := wvr.Snapshot(args)
		if msg != "" {
			return errors.New(msg)
		}
		fmt.Println(c)
		return nil
	}},

	// The board's fzf, its preview and views.
	"_rows": {session: true, run: prints(func(e *wvr.Env, _ []string) string {
		if os.Getenv("LS_MODE") == "plain" {
			return e.PlainRows()
		}
		return e.Rows()
	})},
	"_preview": {session: true, run: prints((*wvr.Env).Preview)},
	"_view":    {run: prints(func(_ *wvr.Env, args []string) string { return wvr.View(arg(args, 0)) })},
	"_title":   {run: prints(func(*wvr.Env, []string) string { return wvr.Title() })},
	"_footer":  {run: prints(func(*wvr.Env, []string) string { return wvr.Footer() })},
	"_hf":      {run: prints(func(*wvr.Env, []string) string { return wvr.HF() + "\n" })},
	"_chrome":  {run: prints(func(e *wvr.Env, _ []string) string { return e.Chrome() })},
	"_goto":    {session: true, run: done(func(e *wvr.Env, args []string) { e.Goto(arg(args, 0), arg(args, 1)) })},

	// tmux's hooks.
	"_seen": {session: true, run: done(func(e *wvr.Env, args []string) { e.Seen(arg(args, 0)) })},
	"_fit":  {session: true, run: done(func(e *wvr.Env, args []string) { e.Fit(arg(args, 0)) })},
	"_boot": {session: true, run: done(func(e *wvr.Env, args []string) { e.Boot(arg(args, 0)) })},

	// The new-thread form, and the threads it opens.
	"_from": {session: true, run: (*wvr.Env).From},
	"_form": {run: func(e *wvr.Env, args []string) error {
		if len(args) < 2 {
			return &cli.Exit{Code: 1}
		}
		e.Form(args[0], args[1], arg(args, 2))
		return nil
	}},
	"_fetch": {run: func(e *wvr.Env, args []string) error {
		if len(args) < 3 {
			return &cli.Exit{Code: 1}
		}
		e.Fetch(args[0], args[1], args[2], arg(args, 3))
		return nil
	}},
	"_agent":  {session: true, run: func(e *wvr.Env, args []string) error { return e.AgentPane(arg(args, 0), arg(args, 1)) }},
	"_resume": {session: true, run: done(func(e *wvr.Env, _ []string) { e.ResumeSession() })},
	"_close":  {session: true, run: (*wvr.Env).CloseTail},
}

// start starts (or attaches to) a weaver session, named as given; inside
// one, the board.
func start(e *wvr.Env, args []string) error {
	if e.Session != "" {
		return e.Ls()
	}
	return e.Start(arg(args, 0))
}

// version is set at build time by a package (-ldflags "-X main.version=1.2.3").
var version string

// buildVersion is the version weaver was built as: the one a package set,
// else the module's (a tag, or go's pseudo-version from the commit, +dirty
// with changes), else "dev".
func buildVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return strings.TrimPrefix(bi.Main.Version, "v")
	}
	return "dev"
}
