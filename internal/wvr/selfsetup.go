package wvr

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// weaver sets itself up as it starts (`wvr`), so nothing needs doing by
// hand after an install or upgrade: the agents' hooks run this binary, and
// the Neovim plugins its review needs are there.

// The diffview-plus.nvim weaver's review is built on, at the commit its
// tests run against. Fetched, not carried: it is GPL-3.0, weaver MIT.
const (
	diffviewRepo = "https://github.com/dlyongemallo/diffview-plus.nvim"
	diffviewRev  = "2a2d89a07f5d5d3cef7f542b99c3e28050ca205c"
)

func dataDir() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return d
	}
	return os.Getenv("HOME") + "/.local/share"
}

// PluginsDir holds the Neovim plugins weaver fetches; its nvims load them
// when your config has none of its own.
func PluginsDir() string { return dataDir() + "/weaver/plugins" }

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
	for _, c := range hookConfigs() {
		f := c.file
		// Only for an agent you have: its config directory is there.
		if _, err := os.Stat(filepath.Dir(f)); err != nil {
			continue
		}
		old, _ := os.ReadFile(f)
		if !configureHooks(io.Discard, c.label, f, c.hooks, e.Self, false, timestamp()) {
			continue
		}
		if now, _ := os.ReadFile(f); !bytes.Equal(old, now) {
			bk := ""
			if len(old) > 0 {
				bk = " (the old file backed up beside it)"
			}
			fmt.Fprintf(os.Stderr, "%swvr: %s now runs weaver's hooks by %s%s: restart its running sessions to pick them up%s\n",
				CGrey, tildify(f), tildify(e.Self), bk, COff)
		}
	}
	if !pluginsCurrent() {
		c := exec.Command(e.Self, "_plugins")
		c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		c.Start()
	}
}

func pluginsCurrent() bool {
	b, err := os.ReadFile(PluginsDir() + "/diffview-plus.nvim/.weaver-rev")
	return err == nil && string(bytes.TrimSpace(b)) == diffviewRev
}

// Plugins is `wvr _plugins`: diffview-plus.nvim fetched at diffviewRev into
// PluginsDir, swapped in whole. Quiet unless it fails.
func Plugins() error {
	if pluginsCurrent() {
		return nil
	}
	dir := PluginsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(dir, ".diffview-plus.")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for _, args := range [][]string{
		{"init", "-q"},
		{"fetch", "-q", "--depth", "1", diffviewRepo, diffviewRev},
		{"-c", "advice.detachedHead=false", "checkout", "-q", "FETCH_HEAD"},
	} {
		c := exec.Command("git", append([]string{"-C", tmp}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			return fmt.Errorf("fetching diffview-plus.nvim: git %s: %s", args[0], bytes.TrimSpace(out))
		}
	}
	os.WriteFile(tmp+"/.weaver-rev", []byte(diffviewRev+"\n"), 0o644)
	dst := dir + "/diffview-plus.nvim"
	old := dst + ".old"
	os.RemoveAll(old)
	os.Rename(dst, old)
	if err := os.Rename(tmp, dst); err != nil {
		os.Rename(old, dst)
		return err
	}
	os.RemoveAll(old)
	return nil
}

type hookConfig struct{ label, file, hooks string }

// hookConfigs are the agents' hook files weaver sets up: Claude Code's
// settings, and Codex's hooks when ~/.codex exists.
func hookConfigs() []hookConfig {
	home := os.Getenv("HOME")
	claude := os.Getenv("CLAUDE_CONFIG_DIR")
	if claude == "" {
		claude = home + "/.claude"
	}
	cs := []hookConfig{{"claude", claude + "/settings.json", "config/claude-hooks.json"}}
	cdir := os.Getenv("CODEX_HOME")
	if cdir == "" {
		cdir = home + "/.codex"
	}
	if st, err := os.Stat(cdir); err == nil && st.IsDir() {
		cs = append(cs, hookConfig{"codex", filepath.Join(cdir, "hooks.json"), "config/codex-hooks.json"})
	}
	return cs
}
