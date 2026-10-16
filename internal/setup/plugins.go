package setup

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// The Neovim plugins weaver's review needs, and the agents' hook files.

// The diffview-plus.nvim weaver's review is built on, at the commit its
// tests run against. Fetched, not carried: it is GPL-3.0, weaver MIT.
const (
	diffviewRepo = "https://github.com/dlyongemallo/diffview-plus.nvim"
	DiffviewRev  = "2a2d89a07f5d5d3cef7f542b99c3e28050ca205c"
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

func PluginsCurrent() bool {
	b, err := os.ReadFile(PluginsDir() + "/diffview-plus.nvim/.weaver-rev")
	return err == nil && string(bytes.TrimSpace(b)) == DiffviewRev
}

// Plugins is `wvr _plugins`: diffview-plus.nvim fetched at DiffviewRev into
// PluginsDir, swapped in whole. Quiet unless it fails.
func Plugins() error {
	if PluginsCurrent() {
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
		{"fetch", "-q", "--depth", "1", diffviewRepo, DiffviewRev},
		{"-c", "advice.detachedHead=false", "checkout", "-q", "FETCH_HEAD"},
	} {
		c := exec.Command("git", append([]string{"-C", tmp}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			return fmt.Errorf("fetching diffview-plus.nvim: git %s: %s", args[0], bytes.TrimSpace(out))
		}
	}
	if err := os.WriteFile(tmp+"/.weaver-rev", []byte(DiffviewRev+"\n"), 0o644); err != nil {
		return err
	}
	dst := dir + "/diffview-plus.nvim"
	old := dst + ".old"
	os.RemoveAll(old)
	// None there yet is fine: the new one goes in all the same.
	_ = os.Rename(dst, old)
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Rename(old, dst) // the old one back, if there was one
		return err
	}
	os.RemoveAll(old)
	return nil
}

type Config struct {
	Label string // the agent
	File  string // its hooks file
	Hooks string // weaver's hooks for it, in the embedded files
}

// Configs are the agents' hook files weaver sets up: Claude Code's
// settings, and Codex's hooks when ~/.codex exists.
func Configs() []Config {
	home := os.Getenv("HOME")
	claude := os.Getenv("CLAUDE_CONFIG_DIR")
	if claude == "" {
		claude = home + "/.claude"
	}
	cs := []Config{{"claude", claude + "/settings.json", "config/claude-hooks.json"}}
	cdir := os.Getenv("CODEX_HOME")
	if cdir == "" {
		cdir = home + "/.codex"
	}
	if st, err := os.Stat(cdir); err == nil && st.IsDir() {
		cs = append(cs, Config{"codex", filepath.Join(cdir, "hooks.json"), "config/codex-hooks.json"})
	}
	return cs
}
