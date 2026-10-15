// Package weaver holds what the weaver binary carries with it: the shell
// script that does what is not compiled yet, the Neovim plugin, and the
// multiplexers' configs. The binary unpacks them once per build (see
// internal/wv/runtime.go).
package weaver

import "embed"

//go:embed weaver nvim config
var Files embed.FS
