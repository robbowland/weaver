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
