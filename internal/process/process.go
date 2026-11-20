// Package process is what weaver asks the kernel of a process: whether one
// it recorded still runs (by its pid, and its start time where weaver has
// it), when it started, whether one runs under another, and when the
// machine started.
package process

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/robbowland/weaver/internal/cli"
	"golang.org/x/sys/unix"
)

// Alive: does process p, as weaver recorded it, run? A pid of 1 or less is
// none weaver could have recorded (kill(0, 0) signals this process's own
// group, and always succeeds; 1 is launchd): unknown, so never alive.
func Alive(p int) bool { return p > 1 && syscall.Kill(p, 0) == nil }

// StillRuns: does the process weaver recorded as pid (digits), started at
// start (as ps prints it; empty when not recorded), still run? Alive alone
// would trust a pid taken again by another process. starts are start times
// already read (Starts), else it is read. No pid at all cannot be checked:
// it runs.
func StillRuns(pid, start string, starts map[int]string) bool {
	n, err := strconv.Atoi(pid)
	if err != nil || !cli.Digits(pid) {
		return true
	}
	if !Alive(n) {
		return false
	}
	if start == "" {
		return true
	}
	now, ok := starts[n]
	if !ok {
		if now = Starts([]int{n})[n]; now == "" {
			return true
		}
	}
	return strings.Join(strings.Fields(now), " ") == strings.Join(strings.Fields(start), " ")
}

// startLayout is a process's start as ps prints it (lstart, TZ=UTC,
// LC_ALL=C): "Thu Oct 15 15:38:35 2026", "Thu Oct  5 15:38:35 2026".
const startLayout = "Mon Jan _2 15:04:05 2006"

// Starts are processes' start times as ps prints them (startLayout), by
// pid, from the kernel without ps.
func Starts(pids []int) map[int]string {
	m := map[int]string{}
	for _, p := range pids {
		k, err := unix.SysctlKinfoProc("kern.proc.pid", p)
		if err != nil || k.Proc.P_pid != int32(p) {
			continue
		}
		t := time.Unix(int64(k.Proc.P_starttime.Sec), 0).UTC()
		m[p] = t.Format(startLayout)
	}
	return m
}

// ParseStart is the time of a process's start s as weaver records it
// (Starts, ps, Claude Code's procStart: startLayout in UTC, its runs of
// spaces any width); false when s is none.
func ParseStart(s string) (time.Time, bool) {
	t, err := time.ParseInLocation(startLayout, strings.Join(strings.Fields(s), " "), time.UTC)
	return t, err == nil
}

// BootTime is when the machine started (epoch s), from the kernel; 0 when
// it does not say.
func BootTime() int64 {
	tv, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return 0
	}
	return tv.Sec
}

// Runs: does a process named name (its command, as the kernel keeps it, at
// most 16 bytes) run as pid, or under it?
func Runs(pid int, name string) bool {
	if pid <= 1 {
		return false
	}
	ks, err := unix.SysctlKinfoProcSlice("kern.proc.uid", os.Getuid())
	if err != nil {
		return false
	}
	kids := map[int][]int{}
	comm := map[int]string{}
	for _, k := range ks {
		p := int(k.Proc.P_pid)
		kids[int(k.Eproc.Ppid)] = append(kids[int(k.Eproc.Ppid)], p)
		comm[p] = unix.ByteSliceToString(k.Proc.P_comm[:])
	}
	for q := []int{pid}; len(q) > 0; q = q[1:] {
		if comm[q[0]] == name {
			return true
		}
		q = append(q, kids[q[0]]...)
	}
	return false
}
