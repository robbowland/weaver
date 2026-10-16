package wvr

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// snapMsg are a snapshot commit's message fields.
type snapMsg struct{ sid, pane, prompt, promptID, transcript string }

// snapshot commits the worktree holding dir onto ref (docs/design.md): a
// private index (named by key) so your staging area
// is never touched, a tree, a commit on the tip, and a compare-and-swap
// update of ref. Point "base" commits HEAD's tree as the start of a chain
// instead of reading the worktree. An "end" on top of an end of the same
// turn replaces it. Its commit, whether it made one, and false when dir is
// not in a repository.
func snapshot(dir, ref, point string, n int, key string, m snapMsg) (string, bool, bool) {
	top, err := gitOut(dir, nil, "", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", false, false
	}
	gd, err := gitOut(top, nil, "", "rev-parse", "--path-format=absolute", "--git-path", "weaver")
	if err != nil || os.MkdirAll(gd, 0o755) != nil {
		return "", false, false
	}
	idx := gd + "/index-" + key
	// One snapshot of a chain at a time; a lock whose owner died is removed.
	lockd := idx + ".lock.d"
	for i := 0; ; i++ {
		if os.Mkdir(lockd, 0o755) == nil {
			break
		}
		if owner, err := firstLine(lockd + "/pid"); err == nil && owner != "" {
			if p, ok := atoi(owner); ok && syscall.Kill(p, 0) != nil {
				os.RemoveAll(lockd)
				continue
			}
		}
		if i >= 600 {
			return "", false, false
		}
		time.Sleep(100 * time.Millisecond)
	}
	os.WriteFile(lockd+"/pid", []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644)
	defer os.RemoveAll(lockd)

	label := point
	tip, _ := gitOut(top, nil, "", "rev-parse", "-q", "--verify", ref+"^{commit}")
	if tip == "" && key != "reviewed" {
		pruneSnapshots(top, gd, time.Now().Add(-snapKeep))
	}
	var tree string
	if point == "base" {
		label = "start"
		// Only a chain that does not exist yet gets a base.
		t, err := gitOut(top, nil, "", "rev-parse", "-q", "--verify", "HEAD^{tree}")
		if tip != "" || err != nil {
			return tip, false, true
		}
		tree = t
	} else {
		if !exists(idx) {
			if real, err := gitOut(top, nil, "", "rev-parse", "--path-format=absolute", "--git-path", "index"); err == nil {
				if b, err := os.ReadFile(real); err == nil {
					os.WriteFile(idx, b, 0o644)
				}
			}
		}
		// Untracked files over WEAVER_SNAPSHOT_MAX_KB (5 MB) stay out: a
		// download or a build artefact must not bloat the object store.
		maxKB := int64(5120)
		if v, ok := atoi(os.Getenv("WEAVER_SNAPSHOT_MAX_KB")); ok {
			maxKB = int64(v)
		}
		env := []string{"GIT_INDEX_FILE=" + idx}
		var big []string
		if out, err := gitOut(top, nil, "", "ls-files", "-z", "-o", "--exclude-standard"); err == nil {
			for _, p := range strings.Split(out, "\x00") {
				if p == "" {
					continue
				}
				st, err := os.Lstat(filepath.Join(top, p))
				if err == nil && st.Mode().IsRegular() && st.Size() > maxKB*1024 {
					big = append(big, p)
				}
			}
		}
		args := []string{"add", "--all", "--", "."}
		if len(big) > 0 {
			for _, p := range big {
				args = append(args, ":(exclude,literal)"+p)
			}
			// One that was small when an earlier snapshot took it: out.
			gitOut(top, env, strings.Join(big, "\n")+"\n", "rm", "--cached", "-f", "-q", "--ignore-unmatch", "--pathspec-from-file=-")
		}
		gitOut(top, env, "", args...)
		t, err := gitOut(top, env, "", "write-tree")
		if err != nil {
			return "", false, false
		}
		tree = t
	}
	parent := tip
	if tip != "" && point == "end" {
		if c, err := gitOut(top, nil, "", "cat-file", "commit", tip); err == nil {
			ls := strings.Split(c, "\n")
			hasTurn, hasEnd := false, false
			for _, l := range ls {
				hasTurn = hasTurn || l == "Weaver-Turn: "+strconv.Itoa(n)
				hasEnd = hasEnd || l == "Weaver-Point: end"
			}
			if hasTurn && hasEnd {
				parent, _ = gitOut(top, nil, "", "rev-parse", "-q", "--verify", tip+"^")
			}
		}
	}
	if tip != "" {
		if t, err := gitOut(top, nil, "", "rev-parse", tip+"^{tree}"); err == nil && t == tree {
			return tip, false, true
		}
	}
	subject := "weaver: turn " + strconv.Itoa(n) + " " + label
	if point == "reviewed" {
		subject = "weaver: reviewed"
	}
	body := ""
	if m.prompt != "" && point != "reviewed" {
		p := firstRunes(m.prompt, 72)
		if utf8.RuneCountInString(m.prompt) > 72 {
			p = firstRunes(m.prompt, 71) + "…"
		}
		subject += ": " + p
		body = firstRunes(m.prompt, 2000) + "\n\n"
	}
	msg := subject + "\n\n" + body
	if m.sid != "" {
		msg += "Weaver-Session: " + m.sid + "\n"
	}
	if point != "reviewed" {
		msg += "Weaver-Turn: " + strconv.Itoa(n) + "\n"
	}
	msg += "Weaver-Point: " + label + "\n"
	if m.pane != "" {
		msg += "Weaver-Pane: " + m.pane + "\n"
	}
	if m.promptID != "" {
		msg += "Prompt-Id: " + m.promptID + "\n"
	}
	if m.transcript != "" {
		msg += "Transcript: " + m.transcript + "\n"
	}
	ident := []string{"GIT_AUTHOR_NAME=weaver", "GIT_AUTHOR_EMAIL=weaver@localhost", "GIT_COMMITTER_NAME=weaver", "GIT_COMMITTER_EMAIL=weaver@localhost"}
	ct := []string{"commit-tree", tree}
	if parent != "" {
		ct = append(ct, "-p", parent)
	}
	commit, err := gitOut(top, ident, msg, ct...)
	if err != nil || commit == "" {
		return tip, false, true
	}
	if _, err := gitOut(top, nil, "", "update-ref", "-m", subject, ref, commit, tip); err != nil {
		return tip, false, true
	}
	return commit, true, true
}

func firstRunes(s string, n int) string { return runeSlice(s, n) }

// snapKeep is how long a session's snapshots outlive its last turn: as long
// as weaver keeps its activity logs and turns.
const snapKeep = 31 * 24 * time.Hour

// pruneSnapshots drops this worktree's session chains whose last snapshot is
// older than before, and the private indexes (gd/index-*) no snapshot has
// used since: without it a main checkout would keep every session's chain,
// and a copy of its index for each, for good. Run as a chain starts, which
// is once per session in each worktree.
func pruneSnapshots(top, gd string, before time.Time) {
	out, err := gitOut(top, nil, "", "for-each-ref", "--format=%(refname) %(objectname) %(committerdate:unix)", "refs/worktree/weaver/sessions/")
	if err == nil {
		for _, l := range strings.Split(out, "\n") {
			f := strings.Fields(l)
			if len(f) != 3 {
				continue
			}
			if t, ok := atoi(f[2]); ok && int64(t) < before.Unix() {
				gitOut(top, nil, "", "update-ref", "-d", f[0], f[1])
			}
		}
	}
	ms, _ := filepath.Glob(gd + "/index-*")
	for _, m := range ms {
		if strings.HasSuffix(m, ".lock.d") || strings.HasSuffix(m, "index-reviewed") {
			continue
		}
		if st, err := os.Lstat(m); err == nil && st.Mode().IsRegular() && st.ModTime().Before(before) {
			os.Remove(m)
		}
	}
}

// snapRoots snapshots each root (RS-separated) onto the session's chain;
// logs and tells nvim about each new commit.
func (e *Env) snapRoots(h *hk, point string, turn int, roots, prompt, promptID string) {
	seen := map[string]bool{}
	ref := "refs/worktree/weaver/sessions/" + h.Sid
	for _, r := range strings.Split(roots, rs) {
		if r == "" || seen[r] {
			continue
		}
		if st, err := os.Stat(r); err != nil || !st.IsDir() {
			continue
		}
		seen[r] = true
		c, made, ok := snapshot(r, ref, point, turn, h.Sid, snapMsg{h.Sid, h.Pane, prompt, promptID, h.Transcript})
		if !ok || !made {
			continue
		}
		if point == "base" {
			point = "start"
		}
		x := `,"root":` + jsonStr(r) + `,"commit":"` + c + `","point":"` + point + `"`
		h.logLine(e, "snapshot", x)
		if h.NV != "" {
			e.nvimEvent(h, "snapshot", x+`,"ref":"`+ref+`"`, "", "", true)
		}
	}
}

// hookSnapshots are the turn's snapshots, from the hook's background run.
func (e *Env) hookSnapshots(h *hk) {
	if os.Getenv("WEAVER_SNAPSHOTS") == "0" || h.Sid == "" {
		return
	}
	if h.SnapPrev != "" {
		n, _ := atoi(h.SnapPrev)
		e.snapRoots(h, "end", n, h.OTurnRoots, h.PrevPrompt, h.OPromptID)
	}
	if h.SnapStart != "" {
		// The agent's root, and the roots of its previous turn: changes made
		// there since are not this turn's.
		e.snapRoots(h, "start", h.Turn, h.Root+rs+h.OTurnRoots, h.PromptLong, h.PromptID)
	}
	if h.FirstTouch != "" {
		e.snapRoots(h, "base", h.Turn, h.FirstTouch, "", h.PromptID)
	}
	if h.SnapEnd != "" {
		tr := h.TurnRoots
		if tr != "" {
			tr += rs
		}
		e.snapRoots(h, "end", h.Turn, tr+h.Root, h.TurnPrompt, h.PromptID)
	}
}

// Snapshot is `wvr snapshot --reviewed [dir]`: mark the worktree holding
// dir reviewed; prints its commit.
func Snapshot(args []string) (string, string) {
	dir := ""
	for _, a := range args {
		switch {
		case a == "--reviewed":
		case strings.HasPrefix(a, "-"):
			return "", "usage: wvr snapshot --reviewed [dir]"
		default:
			dir = a
		}
	}
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if _, err := gitOut(dir, nil, "", "rev-parse", "--show-toplevel"); err != nil {
		return "", dir + " is not in a git repository"
	}
	c, _, ok := snapshot(dir, "refs/worktree/weaver/reviewed", "reviewed", 0, "reviewed", snapMsg{})
	if !ok || c == "" {
		return "", "cannot snapshot " + dir
	}
	return c, ""
}
