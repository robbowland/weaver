package setup

import (
	"cmp"
	"os"
)

// Config is an agent's hooks file, as weaver sets it up.
type Config struct {
	Label string // the agent
	File  string // its hooks file
	Hooks string // weaver's hooks for it, in the embedded files
}

// Configs are the agents' hook files weaver sets up: Claude Code's
// settings.
func Configs() []Config {
	claude := cmp.Or(os.Getenv("CLAUDE_CONFIG_DIR"), os.Getenv("HOME")+"/.claude")
	return []Config{{"claude", claude + "/settings.json", "config/claude-hooks.json"}}
}
