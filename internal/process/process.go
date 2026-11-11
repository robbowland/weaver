// Package process is what weaver asks the kernel of a process: whether one
// it recorded still runs (by its pid, and its start time where weaver has
// it), when it started, its arguments and environment, and ending it.
package process

import (
	"bytes"
	"encoding/binary"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

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
	if err != nil || !digits(pid) {
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

func digits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

// Starts are processes' start times as ps prints them (TZ=UTC, LC_ALL=C:
// "Thu Oct 15 15:38:35 2026"), by pid, from the kernel without ps.
func Starts(pids []int) map[int]string {
	m := map[int]string{}
	for _, p := range pids {
		k, err := unix.SysctlKinfoProc("kern.proc.pid", p)
		if err != nil || k.Proc.P_pid != int32(p) {
			continue
		}
		t := time.Unix(int64(k.Proc.P_starttime.Sec), 0).UTC()
		m[p] = t.Format("Mon Jan _2 15:04:05 2006")
	}
	return m
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

// Args are process pid's arguments and environment, as the kernel keeps
// them (kern.procargs2); false when it will not say (another user's
// process, or one macOS keeps them of).
func Args(pid int) ([]string, map[string]string, bool) {
	b, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return nil, nil, false
	}
	return parseArgs(b)
}

// parseArgs reads kern.procargs2's layout: argc (int32), the program's
// path, NUL padding, then argc arguments and the environment, each ended by
// a NUL.
func parseArgs(b []byte) ([]string, map[string]string, bool) {
	if len(b) < 4 {
		return nil, nil, false
	}
	argc := int(binary.LittleEndian.Uint32(b[:4]))
	b = b[4:]
	i := bytes.IndexByte(b, 0)
	if i < 0 {
		return nil, nil, false
	}
	b = bytes.TrimLeft(b[i:], "\x00")
	var argv []string
	env := map[string]string{}
	for _, s := range strings.Split(string(b), "\x00") {
		if len(argv) < argc {
			argv = append(argv, s)
			continue
		}
		if s == "" {
			break
		}
		if k, v, ok := strings.Cut(s, "="); ok {
			env[k] = v
		}
	}
	return argv, env, len(argv) == argc
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
