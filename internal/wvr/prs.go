package wvr

import (
	"maps"
	"os"
	"os/exec"
	"strings"

	"github.com/robbowland/weaver/internal/git"
	"github.com/robbowland/weaver/internal/github"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
)

// A thread's pull request on the board: the one the GITHUB view's cache
// holds for the thread's item or its branch, else, its branch merged into
// its remote's default branch as git says, merged. The home board's tender
// reads them (notePRs) into $STATE_DIR/<skey>.prs, a pane's each; the
// board's rows and cards read that file alone, so a redraw asks neither
// GitHub nor git.

// prState is how a thread's pull request stands.
type prState string

const (
	prOpen   prState = "open"
	prMerged prState = "merged"
	prClosed prState = "closed"
)

// prChecks is how a pull request's checks stand: failing when one fails,
// else running when one runs, else passing; checksNone for none.
type prChecks string

const (
	checksNone    prChecks = ""
	checksFailing prChecks = "failing"
	checksRunning prChecks = "running"
	checksPassing prChecks = "passing"
)

// prReview is where a pull request's review stands: changes requested,
// else approved, else asked for; reviewNone for none asked.
type prReview string

const (
	reviewNone      prReview = ""
	reviewChanges   prReview = "changes requested"
	reviewApproved  prReview = "approved"
	reviewRequested prReview = "requested"
)

// threadPR is a thread's pull request: its number (none when git alone
// says its branch merged), how it stands, whether it is a draft, its
// checks and its review. Into is the branch git says it merged into
// (origin/main), when git said so.
type threadPR struct {
	Num    int      `json:"num,omitempty"`
	State  prState  `json:"state"`
	Draft  bool     `json:"draft,omitempty"`
	Checks prChecks `json:"checks,omitempty"`
	Review prReview `json:"review,omitempty"`
	Into   string   `json:"into,omitempty"`
}

// prOfItem is pull request it as weaver reads it: what the GITHUB view's
// fact (ghFact) and card, a thread's card (prLine) and its row
// (rowSignals) say of it.
func prOfItem(it github.Item) threadPR {
	pr := threadPR{Num: it.Number, State: prOpen, Draft: it.Draft}
	switch strings.ToUpper(it.State) {
	case "MERGED":
		pr.State = prMerged
	case "CLOSED":
		pr.State = prClosed
	}
	switch {
	case len(it.FailingChecks) > 0:
		pr.Checks = checksFailing
	case it.RunningChecks > 0:
		pr.Checks = checksRunning
	case it.PassingChecks > 0:
		pr.Checks = checksPassing
	}
	switch {
	case it.Review == "CHANGES_REQUESTED":
		pr.Review = reviewChanges
	case it.Review == "APPROVED":
		pr.Review = reviewApproved
	case len(it.Requested) > 0 || it.Review == "REVIEW_REQUIRED":
		pr.Review = reviewRequested
	}
	return pr
}

// prSignal is the colour of what is said of a pull request whose checks
// and review stand so: red when a check fails or changes are requested,
// else green when it is approved, else grey.
func prSignal(c prChecks, r prReview) string {
	switch {
	case c == checksFailing || r == reviewChanges:
		return CRed
	case r == reviewApproved:
		return CGreen
	}
	return CGrey
}

// prFor is the pull request among its (the view's cache's) for the
// thread k ties (firstTied): the one its item names, else the one whose
// branch, as a thread from it names it (prBranch), is the thread's.
func prFor(k threadKey, its []github.Item) (threadPR, bool) {
	var prs []github.Item
	for _, it := range its {
		if it.Kind == github.PRs {
			prs = append(prs, it)
		}
	}
	j := firstTied(len(prs), func(j int) (threadKey, itemKey) {
		return k, itemKey{ref: prs[j].Ref(), repo: prs[j].Repo, branch: prBranch(prs[j])}
	})
	if j < 0 {
		return threadPR{}, false
	}
	return prOfItem(prs[j]), true
}

// prsFile keeps each thread's pull request, by its agent's pane.
func (e *Env) prsFile() string { return e.StateDir + "/" + e.SKey + ".prs" }

// readPRs is each thread's pull request as the tender last read them, by
// pane; none when it has not.
func (e *Env) readPRs() map[string]threadPR {
	m := map[string]threadPR{}
	bestEffort(store.Read(e.prsFile(), &m))
	return m
}

// prMemo is what the tender keeps between its rounds: each repository's
// GitHub name, and what git said of each worktree as it was then.
type prMemo struct {
	slugs  slugMemo
	merged map[string]string // root, HEAD and the default's tip: what it merged into ("" not merged)
}

func newPRMemo() *prMemo {
	return &prMemo{slugs: slugMemo{}, merged: map[string]string{}}
}

// mergedInto is the remote's default branch (origin/main) the branch the
// worktree at root is on has merged into, as git says: its HEAD is in it,
// and the branch made commits of its own (its reflog has one), which a
// branch never committed to, whose HEAD is in it from the start, has not.
// "" when it has not, or git cannot say (no remote default, no reflog,
// the default branch itself, a detached HEAD). One git call when nothing
// moved since the last round.
func (m *prMemo) mergedInto(root, branch string) string {
	if root == "" || branch == "" {
		return ""
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return ""
	}
	def := git.RemoteDefault(root, git.PreferredRemote(root))
	if def == "" || def[strings.IndexByte(def, '/')+1:] == branch {
		return ""
	}
	out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD", def).Output()
	if err != nil {
		return ""
	}
	key := root + "\n" + strings.Join(strings.Fields(string(out)), " ")
	if into, ok := m.merged[key]; ok {
		return into
	}
	into := ""
	if git.OK(root, "merge-base", "--is-ancestor", "HEAD", def) && committed(root, branch) {
		into = def
	}
	m.merged[key] = into
	return into
}

// committed: did branch take commits of its own in the repository at
// root, as its reflog says (a commit or cherry-pick made on it)?
func committed(root, branch string) bool {
	out, err := exec.Command("git", "-C", root, "log", "-g", "--format=%gs", "refs/heads/"+branch, "--").Output()
	if err != nil {
		return false
	}
	for _, l := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(l, "commit") || strings.HasPrefix(l, "cherry-pick") {
			return true
		}
	}
	return false
}

// notePRs reads each thread's pull request (the view's cache, then git)
// into prsFile, and has the boards show them when they changed. A thread's
// pull request merged as git says keeps the number the cache gave it
// before.
func (e *Env) notePRs(m *prMemo) {
	before := e.readPRs()
	c := readGitHub(e)
	its := c.items()
	now := map[string]threadPR{}
	for _, f := range e.SessionFiles("thread") {
		th, err := store.ReadThread(f)
		if err != nil {
			continue
		}
		pane := string(session.PaneOfFile(f))
		k := m.slugs.keyOf(th)
		pr, ok := prFor(k, its)
		if into := m.mergedInto(th.Worktree, k.branch); into != "" {
			if !ok {
				pr = before[pane]
				if pr.State != prOpen && pr.State != prMerged {
					pr = threadPR{}
				}
			}
			pr.State, pr.Into, ok = prMerged, into, true
			pr.Checks, pr.Review = "", ""
		}
		if ok {
			now[pane] = pr
		}
	}
	if maps.Equal(now, before) {
		return
	}
	bestEffort(os.MkdirAll(e.StateDir, 0o755))
	if store.Write(e.prsFile(), now) == nil {
		e.PushStatus()
	}
}
