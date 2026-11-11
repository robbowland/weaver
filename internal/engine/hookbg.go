package engine

import (
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/git"
	"github.com/robbowland/weaver/internal/hooks"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
)

// hookBg is `wvr hook --bg`: everything a hook does besides writing the
// state, after it, so the agent never waits on Rex, git or Worktrunk.
func hookBg(e *session.Env) {
	h := &hk{}
	if json.Unmarshal([]byte(os.Getenv("WEAVER_BG")), h) != nil {
		return
	}
	bestEffort(os.Unsetenv("WEAVER_BG"))
	h.actOn(e)
	h.sf = e.StateFile(h.Pane)
	if h.FollowBash != "" {
		followBash(e, h)
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
	if h.AgentSnap != "" {
		agentChanges(e, h, []hooks.Change{{Path: h.EvFile}})
	}
}

// actOn has e act on the session the hook acted on, not the one its
// environment names (a background run's may be another session's). A run
// started by an older weaver says none: its environment's, as then.
func (h *hk) actOn(e *session.Env) {
	if h.Session != "" {
		e.Session, e.SKey, e.RexID = h.Session, h.SKey, h.RexID
	}
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

// followBashEdits: a Bash command or MCP tool that changed files (sed -i, a
// heredoc, a formatter, an MCP write) counts as an edit. Which files: what
// the call says (Claude Code's bashEditDiff), else, in the repos the command
// names, the one it ran in and the agent's root, the files git sees as
// changed since the previous such call. The newest is followed. False when
// nothing changed.
func followBashEdits(e *session.Env, h *hk) bool {
	stamp := e.PaneFile(h.Pane, "bash")
	nw := stamp + "." + strconv.Itoa(os.Getpid())
	if os.WriteFile(nw, nil, 0o644) != nil {
		return false
	}
	var ps []string
	if h.ChangedKnown {
		bestEffort(os.Rename(nw, stamp))
		ps = followable(h.Changed)
	} else {
		sst, err := os.Stat(stamp)
		if err != nil {
			bestEffort(os.Rename(nw, stamp)) // without it, the next command looks further back
			return false
		}
		ps = changedSince(h, sst.ModTime())
		bestEffort(os.Rename(nw, stamp))
	}
	type cand struct {
		p string
		t time.Time
	}
	var cs []cand
	for _, p := range ps {
		if st, err := os.Lstat(p); err == nil {
			cs = append(cs, cand{p, st.ModTime()})
		}
	}
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
	return true
}

// followable are the files of paths that following can show: on disk, in
// a worktree, and not ignored there (what git status would list).
func followable(paths []string) []string {
	byRoot := map[string][]string{}
	var roots []string
	for _, p := range paths {
		st, err := os.Lstat(p)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		r, ok := git.FileWorkTree(p)
		rel := relTo(r, p)
		if !ok || rel == "" {
			continue
		}
		if _, seen := byRoot[r]; !seen {
			roots = append(roots, r)
		}
		byRoot[r] = append(byRoot[r], rel)
	}
	var ps []string
	for _, r := range roots {
		drop := ignored(r, byRoot[r])
		for _, rel := range byRoot[r] {
			if !drop[rel] {
				ps = append(ps, r+"/"+rel)
			}
		}
	}
	return ps
}

// changedSince are the files git sees as changed, modified after t, in the
// repos a command names, the one it ran in and the agent's root (four at
// most): regular files, not deletions.
func changedSince(h *hk, t time.Time) []string {
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
	var ps []string
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
			if err != nil || !st.Mode().IsRegular() || !st.ModTime().After(t) {
				continue
			}
			ps = append(ps, p)
		}
	}
	return ps
}
