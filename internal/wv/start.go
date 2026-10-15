package wv

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// Start is `weaver [start [session]]` outside a session: attach to the
// weaver session on weaver's own tmux server (with config/tmux.conf, read
// again each time, so an install reaches a running server), or start it
// with home in window 0. Its panes know their session
// ($WEAVER_TMUX_SESSION); zellij's variables stay out of it.
func (e *Env) Start(name string) int {
	if name == "" {
		name = os.Getenv("WEAVER_SESSION")
	}
	if name == "" {
		name = "weaver"
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		fmt.Fprintln(os.Stderr, "weaver: tmux is not installed")
		return 1
	}
	dir, err := Runtime()
	if err != nil {
		fmt.Fprintln(os.Stderr, "weaver: "+err.Error())
		return 1
	}
	conf := dir + "/config/tmux.conf"
	if !exists(conf) {
		fmt.Fprintln(os.Stderr, "weaver: missing "+conf)
		return 1
	}
	var env []string
	for _, v := range os.Environ() {
		k, _, _ := strings.Cut(v, "=")
		switch k {
		case "ZELLIJ", "ZELLIJ_SESSION_NAME", "ZELLIJ_PANE_ID", "TMUX", "TMUX_PANE":
			continue
		}
		env = append(env, v)
	}
	base := append(append([]string{tmux}, e.TSock...), "-f", conf)
	has := exec.Command(base[0], append(append([]string{}, base[1:]...), "has-session", "-t", "="+name)...)
	has.Env = env
	if has.Run() == nil {
		syscall.Exec(tmux, append(base, "source-file", conf, ";", "attach-session", "-t", "="+name), env)
		return 1
	}
	skey := "tmux-" + strings.ReplaceAll(name, "/", "_")
	os.MkdirAll(e.StateDir, 0o755)
	os.WriteFile(e.StateDir+"/"+skey+".layout", []byte("weaver\n"), 0o644)
	os.Remove(e.StateDir + "/" + skey + ".last")
	// The server keeps the directory it starts in: from /, never a worktree
	// weaver close may remove under it (its new panes would die at once).
	here, _ := os.Getwd()
	if os.Chdir("/") != nil {
		fmt.Fprintln(os.Stderr, "weaver: cannot cd /")
		return 1
	}
	syscall.Exec(tmux, append(base, "new-session", "-s", name, "-n", "home", "-c", here, "-e", "WEAVER_TMUX_SESSION="+name,
		e.Self, "home", ";", "set-environment", "-t", "="+name, "WEAVER_TMUX_SESSION", name, ";", "source-file", conf), env)
	return 1
}
