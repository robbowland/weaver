// weaver keeps track of the coding agents running in your tmux session and
// opens Neovim in the worktree each is really editing.
package main

import (
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"github.com/robbowland/weaver/internal/wv"
)

func main() {
	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}
	// tmux's status line asks for the counts on every redraw: no more than a
	// file read.
	if cmd == "status" && len(args) >= 2 && args[1] == "--tmux" {
		fmt.Println(wv.StatusLine(args[1:]))
		return
	}
	switch cmd {
	case "help", "-h", "--help":
		fmt.Print(wv.Usage)
		return
	case "version", "--version":
		fmt.Println("weaver " + buildVersion())
		return
	case "setup":
		os.Exit(wv.Setup(args[1:], wv.Load().Self, os.Stdout))
	case "_runtime":
		// install.sh: unpack this build's files now, so Neovim configs find
		// its plugin before weaver next runs.
		if _, err := wv.Runtime(); err != nil {
			fmt.Fprintf(os.Stderr, "weaver: %v\n", err)
			os.Exit(1)
		}
		return
	}
	e := wv.Load()
	rc, ok := run(e, cmd, args)
	if !ok {
		fmt.Fprintf(os.Stderr, "weaver: unknown command: %s\n\n%s", cmd, wv.Usage)
		os.Exit(2)
	}
	os.Exit(rc)
}

// run runs cmd; ok false when there is no such command.
func run(e *wv.Env, cmd string, args []string) (int, bool) {
	needSession := func() bool {
		if e.Session == "" {
			fmt.Fprintln(os.Stderr, "weaver: not inside a tmux session (start one with `wvr`)")
			return false
		}
		return true
	}
	switch cmd {
	case "hook":
		e.HookMain(args[1:])
	case "event", "adapt":
		bell, adapter := false, ""
		rest := args[1:]
		if cmd == "adapt" {
			if len(rest) == 0 || rest[0] != "codex" {
				fmt.Fprintln(os.Stderr, "weaver: usage: wvr adapt codex [--bell]")
				return 1, true
			}
			adapter, rest = "codex", rest[1:]
		}
		for _, a := range rest {
			if a == "--bell" {
				bell = true
			}
		}
		return e.Event(adapter, bell), true
	case "snapshot":
		c, msg := wv.Snapshot(args[1:])
		if msg != "" {
			fmt.Fprintln(os.Stderr, "weaver: "+msg)
			return 1, true
		}
		fmt.Println(c)
	case "_preview":
		if !needSession() {
			return 1, true
		}
		fmt.Print(e.Preview(args[1:]))
	case "_view":
		w := ""
		if len(args) > 1 {
			w = args[1]
		}
		fmt.Print(wv.View(w))
	case "_goto":
		if !needSession() {
			return 1, true
		}
		p, t := "", ""
		if len(args) > 1 {
			p = args[1]
		}
		if len(args) > 2 {
			t = args[2]
		}
		e.Goto(p, t)
	case "go":
		if len(args) < 2 || args[1] != "home" {
			fmt.Fprintln(os.Stderr, "weaver: usage: wvr go home")
			return 1, true
		}
		if !needSession() {
			return 1, true
		}
		e.GoHome()
	case "view":
		if !needSession() {
			return 1, true
		}
		return e.View(args[1:]), true
	case "_from":
		if !needSession() {
			return 1, true
		}
		return e.From(args[1:]), true
	case "next":
		if !needSession() {
			return 1, true
		}
		e.Next()
	case "_seen", "_fit":
		if !needSession() {
			return 1, true
		}
		w := ""
		if len(args) > 1 {
			w = args[1]
		}
		if cmd == "_seen" {
			e.Seen(w)
		} else {
			e.Fit(w)
		}
	case "_boot":
		if !needSession() {
			return 1, true
		}
		sock := ""
		if len(args) > 1 {
			sock = args[1]
		}
		e.Boot(sock)
	case "status":
		if !needSession() {
			return 1, true
		}
		if len(args) > 1 && args[1] == "--push" {
			e.PushStatus(false)
		} else {
			fmt.Println(e.StatusText())
		}
	case "gc":
		e.GC()
	case "new":
		return e.New(args[1:])
	case "_form":
		if len(args) < 3 {
			return 1, true
		}
		q := ""
		if len(args) > 3 {
			q = args[3]
		}
		e.Form(args[1], args[2], q)
	case "_fetch":
		if len(args) < 4 {
			return 1, true
		}
		sf := ""
		if len(args) > 4 {
			sf = args[4]
		}
		e.Fetch(args[1], args[2], args[3], sf)
	case "", "start":
		if e.Session != "" {
			return e.Ls(), true
		}
		n := ""
		if cmd == "start" && len(args) > 1 {
			n = args[1]
		}
		return e.Start(n), true
	case "edit":
		if !needSession() {
			return 1, true
		}
		return e.Edit(args[1:]), true
	case "nvim":
		if !needSession() {
			return 1, true
		}
		return e.Nvim(args[1:]), true
	case "_agent":
		if !needSession() {
			return 1, true
		}
		tok, kind := "", ""
		if len(args) > 1 {
			tok = args[1]
		}
		if len(args) > 2 {
			kind = args[2]
		}
		return e.AgentPane(tok, kind), true
	case "resume":
		for _, a := range args[1:] {
			if a == "-h" || a == "--help" {
				fmt.Print(wv.Usage)
				return 0, true
			}
		}
		return e.Resume(args[1:]), true
	case "_resume":
		if !needSession() {
			return 1, true
		}
		e.ResumeSession()
	case "close":
		if !needSession() {
			return 1, true
		}
		return e.Close(args[1:]), true
	case "_close":
		if !needSession() {
			return 1, true
		}
		return e.CloseTail(args[1:]), true
	case "send":
		if !needSession() {
			return 1, true
		}
		return e.Send(args[1:]), true
	case "quit":
		for _, a := range args[1:] {
			if a == "-h" || a == "--help" {
				fmt.Print(wv.Usage)
				return 0, true
			}
		}
		return e.Quit(args[1:]), true
	case "_rows":
		if !needSession() {
			return 1, true
		}
		fmt.Print(e.Rows(os.Getenv("LS_MODE") == "plain"))
	case "_title":
		fmt.Print(wv.Title())
	case "_footer":
		fmt.Print(wv.Footer())
	case "_hf":
		fmt.Println(wv.HF())
	case "_chrome":
		fmt.Print(e.Chrome())
	case "ls":
		if !needSession() {
			return 1, true
		}
		return e.Ls(), true
	case "home":
		if !needSession() {
			return 1, true
		}
		return e.Home(), true
	default:
		return 0, false
	}
	return 0, true
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
