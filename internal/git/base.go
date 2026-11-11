package git

import (
	"cmp"
	"os"
	"strings"
)

// baseRefs are where a branch's review base is looked for when nothing names
// it, in order: the upstream remote's default branch (a fork's base), then
// origin's, each where its HEAD points, else its main or master; then a local
// main or master.
var baseRefs = []string{
	"refs/remotes/upstream/HEAD",
	"refs/remotes/upstream/main",
	"refs/remotes/upstream/master",
	"refs/remotes/origin/HEAD",
	"refs/remotes/origin/main",
	"refs/remotes/origin/master",
	"refs/heads/main",
	"refs/heads/master",
}

// ReviewBase is what the branch checked out in dir is reviewed against:
// $WEAVER_BASE, else `git config branch.<branch>.weaverbase` (a pull
// request's base, as a thread from one sets it), else the default branch
// (baseRefs), each only when it names a commit; "" for none.
func ReviewBase(dir string) string {
	if b := os.Getenv("WEAVER_BASE"); b != "" && isCommit(dir, b) {
		return b
	}
	if br, ok := Line(dir, "symbolic-ref", "--quiet", "--short", "HEAD"); ok && br != "" {
		if b, ok := Line(dir, "config", "branch."+br+".weaverbase"); ok && b != "" && isCommit(dir, b) {
			return b
		}
	}
	return defaultBase(dir)
}

// ForkPoint is where the branch in dir left its review base: their merge
// base, as a SHA. A review is given the SHA, never the base's name: tuicr
// diffs a range's two ends, so with main..HEAD a file main gained after the
// fork would show as deleted. False with no base, or none in common.
func ForkPoint(dir string) (string, bool) {
	b := ReviewBase(dir)
	if b == "" {
		return "", false
	}
	sha, ok := Line(dir, "merge-base", b, "HEAD")
	return sha, ok && sha != ""
}

// defaultBase is the first of baseRefs dir's repository has, as a short name
// (upstream/main, main), from one git call: a remote's HEAD as the branch it
// points to (for-each-ref leaves out one pointing at a branch that has gone).
// "" for none.
func defaultBase(dir string) string {
	out, err := Output(dir, nil, "", append([]string{"for-each-ref", "--format=%(refname)%00%(symref)"}, baseRefs...)...)
	if err != nil {
		return ""
	}
	have := map[string]string{} // a ref: what it resolves to (itself, or a symref's target)
	for _, l := range strings.Split(out, "\n") {
		if ref, target, ok := strings.Cut(l, "\x00"); ok {
			have[ref] = cmp.Or(target, ref)
		}
	}
	for _, ref := range baseRefs {
		if r, ok := have[ref]; ok {
			return strings.TrimPrefix(strings.TrimPrefix(r, "refs/remotes/"), "refs/heads/")
		}
	}
	return ""
}

// isCommit: does rev name a commit in dir's repository?
func isCommit(dir, rev string) bool {
	return OK(dir, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
}
