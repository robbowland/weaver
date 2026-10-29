package engine

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/git"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/nvim"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
)

// hookBg is `wvr hook --bg`: everything a hook does besides writing the
// state, after it, so the agent never waits on nvim,
// tmux, git or Worktrunk.
func hookBg(e *session.Env) {
	h := &hk{}
	if json.Unmarshal([]byte(os.Getenv("WEAVER_BG")), h) != nil {
		return
	}
	bestEffort(os.Unsetenv("WEAVER_BG"))
	h.sf = e.StateFile(h.Pane)
	if h.FollowBash != "" {
		followBash(e, h)
	}
	// nvim first: following is what you see.
	if h.NV != "" {
		if h.EvFile != "" {
			nvimEditEvent(e, h, "edit", h.Root, h.EvFile, h.Tool)
		} else if h.Root != h.Old.Root {
			nvimEvent(e, h, nvimRoot{h.nvimHead("root"), h.Root}, h.Root, "")
		}
		// The bar shows the state, turn, prompt and title: any change to them.
		if h.State != h.Old.State || h.Turn != h.sess().Turn || h.Prompt != h.Old.Prompt || h.Title != h.sess().Title || h.Doing != h.sess().Doing ||
			h.Waiting != h.Old.Waiting {
			nvimStateEvent(e, h)
		}
	}
	// The window of an agent that has just finished is marked, for it to
	// be seen when you look (tmux: config/tmux.conf's pane-focus-in hook).
	if h.State != h.Old.State {
		if h.State == agent.StateDone {
			e.Mux().MarkUnseen(h.Pane, true)
		} else if h.Old.State == agent.StateDone {
			e.Mux().MarkUnseen(h.Pane, false)
		}
	}
	if h.State != h.Old.State || h.Event == "UserPromptSubmit" || h.Event == "SessionStart" || h.Title != h.sess().Title || h.Root != h.Old.Root ||
		h.Doing != h.sess().Doing || h.Waiting != h.Old.Waiting {
		// A thread's label is its title (else its task or prompt) and branch.
		if h.Event == "UserPromptSubmit" || h.Event == "SessionStart" || h.Title != h.sess().Title || h.Root != h.Old.Root {
			e.PushStatusAndLabels()
		} else {
			e.PushStatus()
		}
	}
	if h.NV != "" && h.Root != h.Old.Root && sameRoot(h) {
		e.Mux().Title(h.NV, "nvim: "+filepath.Base(h.Root))
	}
	if h.MarkDir != h.Old.MarkedRoot || h.MarkDir != "" && h.Marker != h.Old.Marker {
		if h.Old.MarkedRoot != "" && h.Old.MarkedRoot != h.MarkDir {
			wtMarker(h.Old.MarkedRoot, "")
		}
		if h.MarkDir != "" {
			wtMarker(h.MarkDir, h.Marker)
		}
	}
	if h.Event == "SessionStart" {
		e.GC()
	}
	hookSnapshots(e, h)
}

// sameRoot: is h's root still the agent's? A later hook may have moved it
// on while this one waited.
func sameRoot(h *hk) bool {
	l, _ := files.FirstLine(h.sf)
	m := regexp.MustCompile(`"root":"([^"]*)"`).FindStringSubmatch(l)
	return m == nil || m[1] == h.Root
}

// wtMarker sets or clears a Worktrunk branch marker in dir.
func wtMarker(dir, m string) {
	if _, err := exec.LookPath("wt"); err != nil {
		return
	}
	args := []string{"-C", dir, "config", "state", "marker", "clear"}
	if m != "" {
		args = []string{"-C", dir, "config", "state", "marker", "set", m}
	}
	bestEffort(exec.Command("wt", args...).Run()) // a branch's mark, nothing more
}

// nvimEvent sends event ev (docs/design.md) to the agent's nvim: WeaverEvent,
// or AgentFollow / AgentChecktime on a weaver.lua that predates it, over its
// socket (msgpack-RPC; no nvim client started). An nvim that did not
// answer in time is left alone for 15s.
func nvimEvent(e *session.Env, h *hk, m nvimMessage, r, f string) {
	sock := e.NvimSock(h.Pane)
	if st, err := os.Stat(sock); err != nil || st.Mode()&os.ModeSocket == 0 {
		return
	}
	stalled := sock + ".stalled"
	if t, err := files.FirstLine(stalled); err == nil {
		if n, ok := atoi(t); ok && h.Now-int64(n) < 15 {
			return
		}
	}
	b, err := jsonLine(m)
	if err != nil {
		return
	}
	j, ev := strings.TrimSuffix(string(b), "\n"), m.name()
	q := func(s string) string { return strings.ReplaceAll(s, "'", "''") }
	compat := "0"
	switch ev {
	case "edit", "root", "focus":
		compat = "v:lua.AgentFollow('" + q(r) + "','" + q(f) + "')"
	case "checktime":
		compat = "v:lua.AgentChecktime()"
	}
	expr := "luaeval('_G.WeaverEvent ~= nil') ? v:lua.WeaverEvent('" + q(j) + "') : " + compat
	if nvim.Eval(sock, expr, 2*time.Second) == nvim.Stalled {
		bestEffort(os.WriteFile(stalled, []byte(strconv.FormatInt(h.Now, 10)+"\n"), 0o644))
	} else if files.Exists(stalled) {
		os.Remove(stalled)
	}
}

// nvimEditEvent: an edit (or a focus) at file f in root r.
func nvimEditEvent(e *session.Env, h *hk, ev, r, f, tool string) {
	nvimEvent(e, h, nvimEdit{h.nvimHead(ev), r, f, tool, h.Aid}, r, f)
}

func nvimStateEvent(e *session.Env, h *hk) {
	nvimEvent(e, h, nvimState{h.nvimHead("state"), h.Prompt, h.Waiting, h.Title, h.Doing}, "", "")
}

// followBashEdits: a Bash command or MCP tool that changed files (sed -i, a
// heredoc, a formatter, an MCP write) counts as an edit: in the repos the
// command names, the one it ran in and the agent's root, the files git sees
// as changed since the previous such call; the newest is followed. False
// when nothing changed.
func followBashEdits(e *session.Env, h *hk) bool {
	stamp := e.PaneFile(h.Pane, "bash")
	nw := stamp + "." + strconv.Itoa(os.Getpid())
	if os.WriteFile(nw, nil, 0o644) != nil {
		return false
	}
	sst, err := os.Stat(stamp)
	if err != nil {
		bestEffort(os.Rename(nw, stamp)) // without it, the next command looks further back
		return false
	}
	var roots []string
	seen := map[string]bool{}
	cands := append([]string{h.Cwd, h.Root}, h.Paths...)
	for _, p := range cands {
		if !strings.HasPrefix(p, "/") {
			continue
		}
		d := p
		for d != "" {
			if st, err := os.Stat(d); err == nil && st.IsDir() {
				break
			}
			d = d[:max(strings.LastIndexByte(d, '/'), 0)]
		}
		if d == "" {
			continue
		}
		r, ok := git.WorkTree(d)
		if !ok || seen[r] {
			continue
		}
		seen[r] = true
		roots = append(roots, r)
		if len(roots) >= 4 {
			break
		}
	}
	type cand struct {
		p string
		t time.Time
	}
	var cs []cand
	for _, r := range roots {
		c := exec.Command("git", "-C", r, "status", "--porcelain", "-z", "-uall")
		c.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
		out, _ := c.Output()
		es := strings.Split(string(out), "\x00")
		for i := 0; i < len(es); i++ {
			en := es[i]
			if len(en) < 4 {
				continue
			}
			xy := en[:2]
			// A rename is followed by its original path.
			if strings.ContainsAny(xy, "RC") {
				i++
			}
			if strings.Contains(xy, "D") {
				continue
			}
			p := r + "/" + en[3:]
			st, err := os.Lstat(p)
			if err != nil || !st.Mode().IsRegular() || !st.ModTime().After(sst.ModTime()) {
				continue
			}
			cs = append(cs, cand{p, st.ModTime()})
		}
	}
	bestEffort(os.Rename(nw, stamp))
	if len(cs) == 0 {
		return false
	}
	sort.SliceStable(cs, func(i, j int) bool {
		if !cs[i].t.Equal(cs[j].t) {
			return cs[i].t.After(cs[j].t)
		}
		return cs[i].p < cs[j].p
	})
	newest := cs[0].p
	r, ok := git.FileWorkTree(newest)
	if !ok {
		return false
	}
	if files.Exists(h.sf) {
		store.UpdateState(h.sf, func(st *agent.Record) {
			st.Root, st.File = r, newest
			var fs []string
			for _, f := range st.Files {
				if f != newest {
					fs = append(fs, f)
				}
			}
			fs = append(fs, newest)
			if len(fs) > 200 {
				fs = fs[len(fs)-200:]
			}
			st.Files = fs
			if h.Turn > 0 && !slices.Contains(st.TurnRoots, r) {
				st.TurnRoots = append(st.TurnRoots, r)
			}
		})
	}
	// A root first touched this turn starts its chain from HEAD.
	if !strings.Contains(rs+h.TurnRoots+rs, rs+r+rs) && h.Turn > 0 && h.Sid != "" {
		snapRoots(e, h, "base", h.Turn, r, "", h.PromptID)
	}
	if h.Tool == "Bash" {
		h.logLine(e, logBash{h.logHead("bash"), h.Cmd, newest})
	} else {
		h.logLine(e, logEdit{logHead: h.logHead("edit"), File: newest, Root: r, Tool: h.Tool})
	}
	if h.NV == "" {
		return true
	}
	nvimEditEvent(e, h, "edit", r, newest, h.Tool)
	if r != h.Old.Root {
		e.Mux().Title(h.NV, "nvim: "+filepath.Base(r))
	}
	return true
}

// Focus tells the nvim of the agent in pane p, which rec says (its kind
// alone before it reports), to show file in root.
func Focus(e *session.Env, p mux.PaneID, rec agent.Record, root, file string) {
	now := time.Now()
	h := &hk{Pane: p, AgentKind: rec.Agent, Sid: rec.SessionID, State: rec.State, Turn: rec.Turn, Now: now.Unix(), NowMs: now.UnixMilli()}
	if h.AgentKind == "" {
		h.AgentKind = string(agent.Claude)
	}
	nvimEditEvent(e, h, "focus", root, file, "")
}
