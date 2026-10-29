// Package process is the one rule for a process id weaver recorded:
// whether it still runs.
package process

import "syscall"

// Alive: does process p, as weaver recorded it, run? A pid of 1 or less is
// none weaver could have recorded (kill(0, 0) signals this process's own
// group, and always succeeds; 1 is launchd): unknown, so never alive.
func Alive(p int) bool { return p > 1 && syscall.Kill(p, 0) == nil }
