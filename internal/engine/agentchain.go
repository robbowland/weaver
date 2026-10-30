package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/robbowland/weaver/internal/git"
	"github.com/robbowland/weaver/internal/hooks"
	"github.com/robbowland/weaver/internal/session"
)

// Each agent's own changes (docs/design.md, Agent chains): for the main
// thread and every subagent, in each worktree it changed, two commits on
// refs/worktree/weaver/agents/<session_id>/<agent>. The first, start, is
// the worktree just before the agent's first change there; the tip is
// start with the agent's own files as it last left them. So tip^..tip is
// the agent's diff in that worktree, whoever else works there, and it moves
// as the agent works.

const agentRefs = "refs/worktree/weaver/agents/"

// mainAgent names the main thread's chain; a subagent's is its id.
const mainAgent = "main"

func agentRef(sid, agent string) string { return agentRefs + sid + "/" + agent }

// agentOf is the agent an event is of.
func (h *hk) agentOf() string {
	if h.Aid != "" {
		return h.Aid
	}
	return mainAgent
}

// agentChanges takes the changes cs of the event's agent into its chain in
// each worktree they are in, logs a chain that starts, and has nvim refresh
// a review that shows one.
func agentChanges(e *session.Env, h *hk, cs []hooks.Change) {
	if os.Getenv("WEAVER_SNAPSHOTS") == "0" || h.Sid == "" {
		return
	}
	byRoot := map[string][]hooks.Change{}
	var roots []string
	for _, c := range cs {
		r, ok := git.FileWorkTree(c.Path)
		if !ok {
			continue
		}
		if _, seen := byRoot[r]; !seen {
			roots = append(roots, r)
		}
		byRoot[r] = append(byRoot[r], c)
	}
	a := h.agentOf()
	for _, r := range roots {
		s, ok := agentSnapshot(r, h.Sid, a, byRoot[r])
		if !ok || !s.moved {
			continue
		}
		if s.started {
			h.logLine(e, logSnapshot{logHead: h.logHead("snapshot"), Root: r, Commit: s.tip, Point: "agent", AgentID: a})
		}
		if h.NV != "" {
			nvimEvent(e, h, nvimSnapshot{h.nvimHead("snapshot"), r, s.tip, "agent", agentRef(h.Sid, a)}, "", "")
		}
	}
}

// agentSnap is what agentSnapshot did: the chain's tip, whether the chain
// started, and whether its tip moved.
type agentSnap struct {
	tip            string
	started, moved bool
}

// agentSnapshot takes changes cs, made in the worktree at top by agent of
// session sid, into the agent's chain there, starting the chain first when
// there is none. False when git failed.
func agentSnapshot(top, sid, agent string, cs []hooks.Change) (agentSnap, bool) {
	cs = kept(top, cs)
	if len(cs) == 0 {
		return agentSnap{}, false
	}
	gd, err := git.Output(top, nil, "", "rev-parse", "--path-format=absolute", "--git-path", "weaver")
	if err != nil || os.MkdirAll(gd, 0o755) != nil {
		return agentSnap{}, false
	}
	idx := gd + "/index-" + sid + "-" + agent
	unlock, ok := lockIndex(idx)
	if !ok {
		return agentSnap{}, false
	}
	defer unlock()
	// The index is this call's alone, so no index is kept per agent and
	// worktree (a whole index each, for a month); under the lock, one git
	// left behind is stale.
	bestEffort(os.Remove(idx + ".lock"))
	defer os.Remove(idx)
	env := []string{"GIT_INDEX_FILE=" + idx}
	// The paths a call names are files, never patterns.
	lit := []string{env[0], "GIT_LITERAL_PATHSPECS=1"}
	ref := agentRef(sid, agent)
	trailers := "\n\nWeaver-Session: " + sid + "\nWeaver-Agent: " + agent + "\n"
	startMsg := "weaver: agent " + agent + " start" + trailers + "Weaver-Point: start\n"

	var s agentSnap
	tip, _ := git.Output(top, nil, "", "rev-parse", "-q", "--verify", ref+"^{commit}")
	start, restarted := tip, false
	if tip == "" {
		pruneSnapshots(top, gd, time.Now().Add(-snapKeep))
		// The worktree as it is, less what this call did where it says.
		bestEffort(os.Remove(idx))
		seedIndex(top, idx)
		addWorktree(top, env)
		for _, c := range cs {
			undo(top, lit, c)
		}
		tree, err := git.Output(top, env, "", "write-tree")
		if err != nil {
			return agentSnap{}, false
		}
		if start, ok = commitSnapshot(top, ref, tree, "", "", "weaver: agent "+agent+" start", startMsg); !ok {
			return agentSnap{}, false
		}
		tip, s.started = start, true
	} else {
		if p, err := git.Output(top, nil, "", "rev-parse", "-q", "--verify", tip+"^"); err == nil {
			start = p
		}
		// A file the agent had not changed yet, whose call says what it
		// held: the start holds that. The start was read after the calls
		// before it had run (the first of a batch), or another changed the
		// file since; either way that is not the agent's.
		if again, ok := restart(top, idx, lit, start, tip, cs, startMsg); ok {
			start, restarted = again, true
		}
		if _, err := git.Output(top, env, "", "read-tree", tip); err != nil {
			return agentSnap{}, false
		}
	}
	stage(top, lit, cs)
	tree, err := git.Output(top, env, "", "write-tree")
	if err != nil {
		return agentSnap{}, false
	}
	// A chain always has its tip on its start, so tip^..tip is its diff.
	if !s.started && !restarted {
		if t, err := git.Output(top, nil, "", "rev-parse", tip+"^{tree}"); err == nil && t == tree {
			return agentSnap{tip: tip}, true
		}
	}
	subject := "weaver: agent " + agent
	c, ok := commitSnapshot(top, ref, tree, start, tip, subject, subject+trailers+"Weaver-Point: tip\n")
	if !ok {
		if s.started {
			// No chain of a start alone: the next call starts it again.
			_, err := git.Output(top, nil, "", "update-ref", "-d", ref, start)
			bestEffort(err)
		}
		return agentSnap{}, false
	}
	s.tip, s.moved = c, true
	return s, true
}

// restart is the start of a chain with the files of cs the agent had not
// changed yet (start..tip) as their calls say they were, when that differs.
func restart(top, idx string, lit []string, start, tip string, cs []hooks.Change, msg string) (string, bool) {
	var said []string
	for _, c := range cs {
		if c.Prior.Kind != hooks.PriorUnknown {
			said = append(said, relTo(top, c.Path))
		}
	}
	if len(said) == 0 {
		return "", false
	}
	out, err := git.Output(top, lit[1:], "", append([]string{"diff", "--name-only", "-z", start, tip, "--"}, said...)...)
	if err != nil {
		return "", false
	}
	touched := map[string]bool{}
	for _, p := range strings.Split(out, "\x00") {
		touched[p] = true
	}
	tmp := idx + ".start"
	env := []string{"GIT_INDEX_FILE=" + tmp, lit[1]}
	defer os.Remove(tmp)
	if _, err := git.Output(top, env, "", "read-tree", start); err != nil {
		return "", false
	}
	for _, c := range cs {
		if c.Prior.Kind != hooks.PriorUnknown && !touched[relTo(top, c.Path)] {
			undo(top, env, c)
		}
	}
	tree, err := git.Output(top, env, "", "write-tree")
	if err != nil {
		return "", false
	}
	if t, err := git.Output(top, nil, "", "rev-parse", start+"^{tree}"); err != nil || t == tree {
		return "", false
	}
	return commitTree(top, tree, "", msg)
}

// kept are the changes git would take into a snapshot: of files in the
// worktree at top that it does not ignore (what it tracks never is), big
// ones only if it tracks them.
func kept(top string, cs []hooks.Change) []hooks.Change {
	var rels []string
	byRel := map[string]hooks.Change{}
	for _, c := range cs {
		rel := relTo(top, c.Path)
		if _, dup := byRel[rel]; rel == "" || dup {
			continue
		}
		byRel[rel] = c
		rels = append(rels, rel)
	}
	if len(rels) == 0 {
		return nil
	}
	drop := ignored(top, rels)
	var big []string
	for _, r := range rels {
		if st, err := os.Lstat(filepath.Join(top, r)); err == nil && st.Mode().IsRegular() && st.Size() > snapMaxBytes() {
			big = append(big, r)
		}
	}
	if len(big) > 0 {
		tracked := map[string]bool{}
		out, _ := git.Output(top, []string{"GIT_LITERAL_PATHSPECS=1"}, "", append([]string{"ls-files", "-z", "--"}, big...)...)
		for _, p := range strings.Split(out, "\x00") {
			tracked[p] = true
		}
		for _, r := range big {
			drop[r] = drop[r] || !tracked[r]
		}
	}
	var out []hooks.Change
	for _, r := range rels {
		if !drop[r] {
			out = append(out, byRel[r])
		}
	}
	return out
}

// ignored are the paths (relative to the worktree at top) that git ignores.
func ignored(top string, rels []string) map[string]bool {
	set := map[string]bool{}
	// Exit status 1 when none is.
	out, _ := git.Output(top, nil, strings.Join(rels, "\x00")+"\x00", "check-ignore", "--stdin", "-z")
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			set[p] = true
		}
	}
	return set
}

// stage brings the files of changes cs (kept) into the private index in env
// as they are now: changed, added, or gone.
func stage(top string, env []string, cs []hooks.Change) {
	var present, gone []string
	for _, c := range cs {
		r := relTo(top, c.Path)
		if _, err := os.Lstat(filepath.Join(top, r)); err != nil {
			gone = append(gone, r)
		} else {
			present = append(present, r)
		}
	}
	if len(present) > 0 {
		_, err := git.Output(top, env, "", append([]string{"add", "--all", "--"}, present...)...)
		bestEffort(err)
	}
	if len(gone) > 0 {
		_, err := git.Output(top, env, "", append([]string{"rm", "--cached", "-q", "--ignore-unmatch", "--"}, gone...)...)
		bestEffort(err)
	}
}

// undo puts into the private index in env what file c held before the call
// changed it, when the call said: so a chain's start is the worktree just
// before the agent's first change there.
func undo(top string, env []string, c hooks.Change) {
	rel := relTo(top, c.Path)
	p := c.Prior
	if p.Kind == hooks.PriorText && !strings.Contains(p.Text, "\r") {
		// A file with CRLF line ends, whose content the call gave with LF
		// ones (unconfirmed, but then every line would show as changed).
		if b, err := os.ReadFile(filepath.Join(top, rel)); err == nil && bytes.Contains(b, []byte("\r\n")) {
			p.Text = strings.ReplaceAll(p.Text, "\n", "\r\n")
		}
	}
	if p.Kind == hooks.PriorHunks {
		b, err := os.ReadFile(filepath.Join(top, rel))
		if err != nil && !os.IsNotExist(err) {
			return
		}
		text, ok := unapply(string(b), p.Hunks)
		if !ok {
			return
		}
		p = hooks.Prior{Kind: hooks.PriorText, Text: text}
	}
	switch p.Kind {
	case hooks.PriorAbsent:
		_, err := git.Output(top, env, "", "rm", "--cached", "-q", "--ignore-unmatch", "--", rel)
		bestEffort(err)
	case hooks.PriorText:
		blob, err := git.Output(top, env, p.Text, "hash-object", "-w", "--stdin", "--path="+rel)
		if err != nil {
			return
		}
		mode := "100644"
		if st, err := os.Stat(filepath.Join(top, rel)); err == nil && st.Mode()&0o111 != 0 {
			mode = "100755"
		}
		_, err = git.Output(top, env, "", "update-index", "--add", "--cacheinfo", mode+","+blob+","+rel)
		bestEffort(err)
	case hooks.PriorUnknown, hooks.PriorHunks:
		// Not said, or hunks the file no longer matches: it stays as it is.
	}
}

// unapply is what a file held before hunks made it into now, or false when
// now does not hold what they made (it changed again since).
func unapply(now string, hunks []hooks.Hunk) (string, bool) {
	var lines []string
	if now != "" {
		lines = strings.Split(strings.TrimSuffix(now, "\n"), "\n")
	}
	// Whether the old text ends in a newline: as now does (an empty file
	// says nothing), unless a hunk's "\ No newline at end of file" says
	// otherwise.
	eol, oldNoEOL, newNoEOL := now == "" || strings.HasSuffix(now, "\n"), false, false
	var out []string
	at := 0
	for _, h := range hunks {
		// A hunk that adds nothing names the line before it.
		from := h.NewStart - 1
		if h.NewLines == 0 {
			from = h.NewStart
		}
		if from < at || from > len(lines) {
			return "", false
		}
		out = append(out, lines[at:from]...)
		at = from
		prev := byte(' ')
		for _, l := range h.Lines {
			sign, text := byte(' '), ""
			if l != "" {
				sign, text = l[0], l[1:]
			}
			switch sign {
			case ' ', '+':
				if at >= len(lines) || lines[at] != text {
					return "", false
				}
				at++
				if sign == ' ' {
					out = append(out, text)
				}
			case '-':
				out = append(out, text)
			case '\\':
				switch prev {
				case ' ':
					oldNoEOL, newNoEOL = true, true
				case '-':
					oldNoEOL = true
				case '+':
					newNoEOL = true
				}
			default:
				return "", false
			}
			prev = sign
		}
	}
	out = append(out, lines[at:]...)
	switch {
	case oldNoEOL:
		eol = false
	case newNoEOL:
		eol = true
	}
	if len(out) == 0 {
		return "", true
	}
	s := strings.Join(out, "\n")
	if eol {
		s += "\n"
	}
	return s, true
}

// relTo is path p relative to the worktree at top (a physical path), or ""
// when it is not in it. p may name a file that no longer exists, through
// symlinked directories (macOS's /tmp).
func relTo(top, p string) string {
	d, rest := filepath.Dir(p), filepath.Base(p)
	for {
		if r, err := filepath.EvalSymlinks(d); err == nil {
			p = filepath.Join(r, rest)
			break
		}
		if d == "/" || d == "." {
			break
		}
		rest = filepath.Join(filepath.Base(d), rest)
		d = filepath.Dir(d)
	}
	if !strings.HasPrefix(p, top+"/") {
		return ""
	}
	return p[len(top)+1:]
}
