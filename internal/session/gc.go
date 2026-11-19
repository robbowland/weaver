package session

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/cli"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/process"
	"github.com/robbowland/weaver/internal/rex"
	"github.com/robbowland/weaver/internal/store"
)

// GC forgets agents that are gone: a dead agent is retired (its thread
// kept as lost when its session went), a state without a pid is retired an
// hour after its session went, or at once when its session was made since
// (an earlier server's, whose pane number a new pane takes), an ended one
// forgotten an hour after it ended; then old files and dead sockets.
func (e *Env) GC() {
	e.gcAgents()
	e.gcFiles()
	rex.Prune(e.RexDir())
}

// gcAgents is GC's agents, their panels' bindings and their threads: of
// every session, each multiplexer asked once a run (lives).
func (e *Env) gcAgents() {
	now := time.Now().Unix()
	l := e.newLives()
	for _, f := range paneFiles(e.StateDir, "json") {
		pk := Peek(f)
		pid, start, st, ts := pk.Pid, pk.Start, pk.State, pk.TS
		skey := KeyOfFile(f)
		switch {
		case pid != "":
			if !process.StillRuns(pid, start, nil) {
				if rec, err := store.ReadState(f); err == nil && l.kept(skey, rec) {
					continue
				}
				l.retire(f, pid)
				continue
			}
		case l.madeAfter(f, ts), !l.running(skey, ts) && now-ts > 3600:
			l.retire(f, "")
			continue
		}
		if st == agent.StateEnded && now-ts > 3600 {
			e.Forget(f, pid)
		}
	}
	for _, f := range paneFiles(e.StateDir, "panel") {
		if files.Exists(strings.TrimSuffix(f, ".panel") + ".json") {
			continue
		}
		b, _ := os.ReadFile(f)
		ls := strings.SplitN(string(b), "\n", 3)
		alive := false
		if len(ls) > 1 {
			if n, ok := cli.Atoi(ls[1]); ok {
				alive = process.Alive(n)
			}
		}
		if !alive {
			os.Remove(f)
		}
	}
	// A thread whose agent never reported (no state) is lost with its
	// session, or once its session is one made since it was written (an
	// earlier server's, whose pane number a new pane would take it for):
	// kept, to open again, never dropped.
	for _, f := range paneFiles(e.StateDir, "thread") {
		if files.Exists(strings.TrimSuffix(f, ".thread") + ".json") {
			continue
		}
		skey := KeyOfFile(f)
		fi, err := os.Stat(f)
		if err == nil && (!l.running(skey, fi.ModTime().Unix()) || l.madeAfter(f, fi.ModTime().Unix())) {
			e.LoseThread(f)
		}
	}
}

// gcFiles is GC's old files and dead sockets.
func (e *Env) gcFiles() {
	old := func(dir, pat string, age time.Duration, all bool) {
		ms, _ := filepath.Glob(filepath.Join(dir, pat))
		for _, m := range ms {
			st, err := os.Lstat(m)
			if err != nil || time.Since(st.ModTime()) <= age {
				continue
			}
			if all {
				os.RemoveAll(m)
			} else if st.Mode().IsRegular() {
				os.Remove(m)
			}
		}
	}
	day, month := 24*time.Hour, 31*24*time.Hour
	// Tabs that never finished opening leave their meeting place behind.
	old(e.StateDir+"/new", "*", day, true)
	old(e.StateDir, "form.*", day, false)
	// A close cut short leaves the record it set aside (Closing).
	old(e.StateDir, "*.closing", day, false)
	old(e.StateDir+"/fetch", "*", day, false)
	if st, err := os.Stat(e.StateDir + "/close.log"); err == nil && st.Size() > 100000 {
		os.Remove(e.StateDir + "/close.log")
	}
	// Threads lost with their session a month ago; never one you held,
	// which waits until you resume it.
	ls, _ := filepath.Glob(e.LostDir + "/*.json")
	for _, f := range ls {
		if st, err := os.Lstat(f); err != nil || !st.Mode().IsRegular() || time.Since(st.ModTime()) <= month {
			continue
		}
		if l, err := store.ReadLost(f); err == nil && l.Held > 0 {
			continue
		}
		os.Remove(f)
	}
	// A removed worktree's ignored files (wvr keepIgnored).
	old(e.StateDir+"/trash", "*", month, true)
	e.gcThreadDirs(time.Hour)
	e.sweepSockets(day)
}

// gcThreadDirs removes the threads' own directories (store.Thread.Dir: the
// context its agent is told, the review comments it was given) that no
// record of a thread names any more, once unchanged for age: a thread
// closed, or lost and forgotten. A directory lives as long as a record of
// its thread does, open or held, and only this removes it. A thread being
// made can write its directory before its record (age covers that).
func (e *Env) gcThreadDirs(age time.Duration) {
	ds, _ := filepath.Glob(e.StateDir + "/threads/*")
	if len(ds) == 0 {
		return
	}
	used := e.threadDirsUsed()
	for _, d := range ds {
		st, err := os.Lstat(d)
		if err != nil || !st.IsDir() || used[d] || time.Since(st.ModTime()) <= age {
			continue
		}
		os.RemoveAll(d)
	}
}

// threadDirsUsed are the directories the records of threads name: open
// (.thread), being closed (.closing), being opened (new/), lost or held
// (lost/). A record moves between these, written where it goes before it
// leaves where it was, or renamed, so all are read twice: one moving as
// they are read is seen at one end of its move or the other.
func (e *Env) threadDirsUsed() map[string]bool {
	used := map[string]bool{}
	thread := func(f string) {
		if th, err := store.ReadThread(f); err == nil {
			used[th.Dir(e.StateDir)] = true
		}
	}
	lost := func(f string) {
		if lo, err := store.ReadLost(f); err == nil {
			used[lo.Thread.Dir(e.StateDir)] = true
		}
	}
	for range 2 {
		for _, f := range paneFiles(e.StateDir, "thread") {
			thread(f)
		}
		for _, f := range paneFiles(e.StateDir, "closing") {
			thread(f)
		}
		ls, _ := filepath.Glob(e.LostDir + "/*.json")
		for _, f := range ls {
			lost(f)
		}
		ns, _ := filepath.Glob(e.StateDir + "/new/*/thread.json")
		for _, f := range ns {
			thread(f)
		}
		ns, _ = filepath.Glob(e.StateDir + "/new/*/lost.json")
		for _, f := range ns {
			lost(f)
		}
	}
	return used
}

// sweepSockets removes sockets nothing listens on any more (a board or a
// view that was killed leaves its own) once they are older than age.
func (e *Env) sweepSockets(age time.Duration) {
	ms, _ := filepath.Glob(e.SockDir + "/*.sock")
	for _, m := range ms {
		st, err := os.Lstat(m)
		if err != nil || st.Mode()&os.ModeSocket == 0 || time.Since(st.ModTime()) <= age {
			continue
		}
		c, err := net.DialTimeout("unix", m, 200*time.Millisecond)
		if err != nil {
			os.Remove(m)
			continue
		}
		_ = c.Close()
	}
}

// paneFiles are dir's per-pane files with extension ext, of any session:
// <key>_<digits>.<ext>.
func paneFiles(dir, ext string) []string {
	ms, _ := filepath.Glob(dir + "/*_*." + ext)
	var out []string
	for _, m := range ms {
		b := strings.TrimSuffix(filepath.Base(m), "."+ext)
		if i := strings.LastIndexByte(b, '_'); i > 0 && cli.Digits(b[i+1:]) {
			out = append(out, m)
		}
	}
	return out
}
