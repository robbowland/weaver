// Package weaver holds what the weaver binary carries with it: the Neovim
// plugin, and tmux's config and the agents' hook settings. The binary
// unpacks them once per build (see internal/wvr/runtime.go).
package weaver

import "embed"

//go:embed nvim config
var Files embed.FS
