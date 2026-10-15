// weaver keeps track of the coding agents running in your tmux session and
// opens Neovim in the worktree each is really editing. One binary: what is
// compiled runs here; the rest, in the shell script it carries.
package main

import (
	"fmt"
	"os"

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
	if cmd == "status" && len(args) >= 2 && (args[1] == "--tmux" || args[1] == "--zjstatus") {
		fmt.Println(wv.StatusLine(args[1:]))
		return
	}
	e := wv.Load()
	if e.Mux == "tmux" {
		if rc, ok := run(e, cmd, args); ok {
			os.Exit(rc)
		}
	}
	if err := e.Script(args); err != nil {
		fmt.Fprintf(os.Stderr, "weaver: %v\n", err)
		os.Exit(1)
	}
}

// run is the compiled commands; ok false for the script's.
func run(e *wv.Env, cmd string, args []string) (int, bool) {
	needSession := func() bool {
		if e.Session == "" {
			fmt.Fprintln(os.Stderr, "weaver: not inside a zellij or tmux session")
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
				fmt.Fprintln(os.Stderr, "weaver: usage: weaver adapt codex [--bell]")
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
			fmt.Fprintln(os.Stderr, "weaver: usage: weaver go home")
			return 1, true
		}
		if !needSession() {
			return 1, true
		}
		e.GoHome()
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
		if len(args) < 2 || args[1] != "--push" {
			return 0, false
		}
		if !needSession() {
			return 1, true
		}
		e.PushStatus(false)
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
