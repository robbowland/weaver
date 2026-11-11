// Package weaver holds what the weaver binary carries with it: Rex's keys,
// the agents' hook settings, and tuicr's theme and settings. The binary
// unpacks them once per build (see internal/setup/runtime.go).
package weaver

import "embed"

//go:embed config
var Files embed.FS
