package wvr

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/robbowland/weaver/internal/setup"
)

// weaver sets itself up as it starts (`wvr`), so nothing needs doing by
// hand after an install or upgrade: the agents' hooks run this binary, and
// the Neovim plugins its review needs are there.

// installed: is this binary the wvr on PATH (not a build run from a
// checkout, whose path the hooks must not take)?
func (e *Env) installed() bool {
	p, err := exec.LookPath("wvr")
	if err != nil {
		return false
	}
	a, err1 := os.Stat(p)
	b, err2 := os.Stat(e.Self)
	return err1 == nil && err2 == nil && os.SameFile(a, b)
}

// SelfSetup is what `wvr` does as it starts, when it is the installed one
// and $WEAVER_SELF_SETUP is not 0: the hooks brought up to date (a line on
// stderr when they changed, with the backup), and the plugins fetched in the
// background.
func (e *Env) SelfSetup() {
	if os.Getenv("WEAVER_SELF_SETUP") == "0" || !e.installed() {
		return
	}
	for _, c := range setup.Configs() {
		// Only for an agent you have: its config directory is there.
		if _, err := os.Stat(filepath.Dir(c.File)); err != nil {
			continue
		}
		ch, err := setup.Plan(c, e.Self)
		if err != nil || !ch.Changed() {
			continue
		}
		backup, err := ch.Apply(setup.Timestamp())
		if err != nil {
			fmt.Fprintf(os.Stderr, "%swvr: %v%s\n", CGrey, err, COff)
			continue
		}
		bk := ""
		if backup != "" {
			bk = " (the old file backed up beside it)"
		}
		fmt.Fprintf(os.Stderr, "%swvr: %s now runs weaver's hooks by %s%s: restart its running sessions to pick them up%s\n",
			CGrey, tildify(c.File), tildify(e.Self), bk, COff)
	}
	if !setup.PluginsCurrent() {
		c := exec.Command(e.Self, "_plugins")
		c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		c.Start()
	}
}
