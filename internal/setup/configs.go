package setup

import (
	"os"
	"path/filepath"
)

// Config is an agent's hooks file, as weaver sets it up.
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
