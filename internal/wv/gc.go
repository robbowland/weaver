package wv

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	reGcPid   = regexp.MustCompile(`"pid":([0-9]+)`)
	reGcStart = regexp.MustCompile(`"proc_start":"([^"]*)"`)
	reGcState = regexp.MustCompile(`"state":"([a-z-]+)"`)
	reGcTs    = regexp.MustCompile(`"ts":([0-9]+)`)
)

func sub1(re *regexp.Regexp, l string) string {
	if m := re.FindStringSubmatch(l); m != nil {
		return m[1]
	}
	return ""
}

// tmuxSessionKeys are the running tmux sessions' keys (tmux-<name>), on
// weaver's server and the one we run in; nil when neither answers.
func (e *Env) tmuxSessionKeys() map[string]bool {
	m := map[string]bool{}
	ok := false
	add := func(args ...string) {
		out, err := runTmux(args...)
		if err != nil {
			return
		}
		ok = true
		for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			if l != "" {
				m["tmux-"+strings.ReplaceAll(l, "/", "_")] = true
			}
		}
	}
	add(append(append([]string{}, e.TSock...), "list-sessions", "-F", "#S")...)
	if e.InTmux {
		add("list-sessions", "-F", "#S")
	}
	if !ok {
		return nil
	}
	return m
}

// GC forgets agents that are gone (the script's gc_states), for tmux
// sessions (zellij's are the script's): a dead agent is retired (its thread
// kept as lost when its session went), a state without a pid goes an hour
// after its session, an ended one an hour after it ended; then old files.
func (e *Env) GC() {
	now := time.Now().Unix()
	var sessions map[string]bool
	asked := false
	running := func(skey string) bool {
		if !asked {
			sessions, asked = e.tmuxSessionKeys(), true
		}
		// Nobody answered: keep everything.
		return sessions == nil || sessions[skey]
	}
	fs, _ := filepath.Glob(e.StateDir + "/tmux-*_*.json")
	for _, f := range fs {
		l, _ := firstLine(f)
		pid, start, st := sub1(reGcPid, l), sub1(reGcStart, l), sub1(reGcState, l)
		ts, _ := strconv.ParseInt(sub1(reGcTs, l), 10, 64)
		if pid != "" {
			if !agentAlive(pid, start, nil) {
				e.retire(f, pid)
				continue
			}
		} else {
			b := filepath.Base(f)
			if !running(b[:strings.LastIndexByte(b, '_')]) && now-ts > 3600 {
				e.forget(f, "")
				continue
			}
		}
		if st == "ended" && now-ts > 3600 {
			e.forget(f, pid)
		}
	}
	ns, _ := filepath.Glob(e.StateDir + "/tmux-*_*.nvim")
	for _, f := range ns {
		if exists(strings.TrimSuffix(f, ".nvim") + ".json") {
			continue
		}
		b, _ := os.ReadFile(f)
		ls := strings.SplitN(string(b), "\n", 3)
		alive := false
		if len(ls) > 1 {
			if n, ok := atoi(ls[1]); ok {
				alive = agentAlive(ls[1], "", nil) && n > 0
			}
		}
		if !alive {
			os.Remove(f)
		}
	}
	// A thread whose agent never reported (no state) goes with its session.
	ths, _ := filepath.Glob(e.StateDir + "/tmux-*_*.thread")
	for _, f := range ths {
		if exists(strings.TrimSuffix(f, ".thread") + ".json") {
			continue
		}
		b := filepath.Base(f)
		if !running(b[:strings.LastIndexByte(b, '_')]) {
			os.Remove(f)
		}
	}
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
	old(e.StateDir+"/fetch", "*", day, false)
	if st, err := os.Stat(e.StateDir + "/close.log"); err == nil && st.Size() > 100000 {
		os.Remove(e.StateDir + "/close.log")
	}
	old(e.LogDir, "*.jsonl", month, false)
	old(e.LostDir, "*.json", month, false)
	old(e.StateDir+"/turns", "*", month, false)
}

// Boot is `weaver _boot SOCKET`: what the weaver session needs as it starts,
// behind its home board (whose fzf listens on SOCKET): old states swept, the
// threads it lost back (unless WEAVER_RESTORE=0), and the status line's
// counts and the windows' labels, whose push reloads the board.
func (e *Env) Boot(sock string) {
	e.GC()
	if os.Getenv("WEAVER_RESTORE") != "0" {
		also := strings.TrimPrefix(e.SKey, "tmux-")
		for _, l := range e.LostRows() {
			if l.Skey == e.SKey || l.Skey == also {
				// Restoring starts agents in new windows: the script's.
				e.ScriptRun("_resume")
				break
			}
		}
	}
	for i := 0; i < 20; i++ {
		if st, err := os.Stat(sock); err == nil && st.Mode()&os.ModeSocket != 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	e.PushStatus(true)
}
