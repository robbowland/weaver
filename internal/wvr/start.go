package wvr

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/setup"
)

// Start is `wvr [start [session]]` outside a session: attach to the
// weaver session on weaver's own tmux server (with config/tmux.conf, read
// again each time, so an install reaches a running server), or start it
// with home in window 0. Its panes know their session
// ($WEAVER_TMUX_SESSION); an outer tmux's or zellij's variables stay out
// of it.
func (e *Env) Start(name string) error {
	if name == "" {
		name = os.Getenv("WEAVER_SESSION")
	}
	if name == "" {
		name = "weaver"
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		return errors.New("tmux is not installed: wvr doctor --fix installs what weaver needs")
	}
	e.SelfSetup()
	dir, err := setup.Runtime()
	if err != nil {
		return err
	}
	conf := dir + "/config/tmux.conf"
	if !files.Exists(conf) {
		return errors.New("missing " + conf)
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
		return fmt.Errorf("tmux: %w", syscall.Exec(tmux, append(base, "source-file", conf, ";", "attach-session", "-t", "="+name), env))
	}
	skey := "tmux-" + strings.ReplaceAll(name, "/", "_")
	os.MkdirAll(e.StateDir, 0o755)
	os.WriteFile(e.StateDir+"/"+skey+".layout", []byte("weaver\n"), 0o644)
	os.Remove(e.StateDir + "/" + skey + ".last")
	// The server keeps the directory it starts in: from /, never a worktree
	// weaver close may remove under it (its new panes would die at once).
	here, _ := os.Getwd()
	if os.Chdir("/") != nil {
		return errors.New("cannot cd /")
	}
	return fmt.Errorf("tmux: %w", syscall.Exec(tmux, append(base, "new-session", "-s", name, "-n", "home", "-c", here, "-e", "WEAVER_TMUX_SESSION="+name,
		e.Self, "home", ";", "set-environment", "-t", "="+name, "WEAVER_TMUX_SESSION", name, ";", "source-file", conf), env))
}

// Boot is `wvr _boot SOCKET`: what the weaver session needs as it starts,
// behind its home board (whose fzf listens on SOCKET): old states swept, the
// threads it lost back (unless WEAVER_RESTORE=0), and the status line's
// counts and the windows' labels, whose push reloads the board.
func (e *Env) Boot(sock string) {
	e.GC()
	if os.Getenv("WEAVER_RESTORE") != "0" {
		also := strings.TrimPrefix(e.SKey, "tmux-")
		for _, l := range e.LostRows() {
			if l.Skey == e.SKey || l.Skey == also {
				e.ResumeSession()
				break
			}
		}
	}
	for i := 0; i < 20; i++ {
		if st, err := os.Stat(sock); err == nil && st.Mode()&os.ModeSocket != 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	e.PushStatusAndLabels()
	if e.WeaverSession() {
		e.StartViews()
	}
}
