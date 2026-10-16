// Package git reads what weaver needs of git: a directory's work tree and
// repository, its remotes and default branches, and git's output.
package git

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/robbowland/weaver/internal/files"
)

// Line runs git -C dir with args; its output's first line, and whether it
// succeeded.
func Line(dir string, args ...string) (string, bool) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	s := strings.TrimRight(string(out), "\n")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s, err == nil
}

func OK(dir string, args ...string) bool {
	return exec.Command("git", append([]string{"-C", dir}, args...)...).Run() == nil
}

// RepoOf is the repository holding directory d: its main checkout and the
// checkout d is in; ok false outside one.
func RepoOf(d string) (repo, checkout string, ok bool) {
	out, err := exec.Command("git", "-C", d, "rev-parse", "--show-toplevel", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return "", "", false
	}
	ls := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(ls) < 2 {
		return "", "", false
	}
	checkout, common := ls[0], ls[1]
	repo = checkout
	if strings.HasSuffix(common, "/.git") {
		repo = strings.TrimSuffix(common, "/.git")
	}
	if st, err := os.Stat(repo); err != nil || !st.IsDir() {
		repo = checkout
	}
	return repo, checkout, true
}

var remotes sync.Map

// PreferredRemote is the remote to start from: origin, else the first. Asked
// once per repository (the header asks several times).
func PreferredRemote(repo string) string {
	if r, ok := remotes.Load(repo); ok {
		return r.(string)
	}
	r := remoteOf(repo)
	remotes.Store(repo, r)
	return r
}

func remoteOf(repo string) string {
	out, _ := exec.Command("git", "-C", repo, "remote").Output()
	first := ""
	for _, r := range strings.Fields(string(out)) {
		if first == "" {
			first = r
		}
		if r == "origin" {
			return r
		}
	}
	return first
}

// Refs are what a repository's refs say for the form, from one git call:
// each ref that matched (its symref target, or "" for a plain ref).
type Refs map[string]string

// RefFacts reads the refs the form asks about for branch br: br, locally
// and on any remote, the remotes' HEADs, and main and master. git remote
// runs alongside (PreferredRemote is memoized).
func RefFacts(repo, br string) Refs {
	go PreferredRemote(repo)
	pats := []string{"Refs/remotes/*/HEAD", "Refs/heads/main", "Refs/heads/master", "Refs/remotes/*/main", "Refs/remotes/*/master"}
	if br != "" {
		pats = append(pats, "Refs/heads/"+br, "Refs/remotes/*/"+br)
	}
	out, _ := exec.Command("git", append([]string{"-C", repo, "for-each-ref", "--format=%(refname)%09%(symref)"}, pats...)...).Output()
	m := Refs{}
	for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if r, sym, ok := strings.Cut(l, "\t"); ok {
			m[r] = sym
		}
	}
	return m
}

func (m Refs) Has(r string) bool { _, ok := m[r]; return ok }

// RemoteDefault is remote's default branch (origin/main) from the refs: its
// HEAD, else its main or master.
func (m Refs) RemoteDefault(remote string) string {
	if sym := m["Refs/remotes/"+remote+"/HEAD"]; strings.HasPrefix(sym, "Refs/remotes/") {
		return strings.TrimPrefix(sym, "Refs/remotes/")
	}
	for _, b := range []string{"main", "master"} {
		if m.Has("Refs/remotes/" + remote + "/" + b) {
			return remote + "/" + b
		}
	}
	return ""
}

// LocalDefault is the local default branch from the refs: the one the remote's
// HEAD names, else main or master.
func (m Refs) LocalDefault(remote string) string {
	r := ""
	if remote != "" {
		if sym := m["Refs/remotes/"+remote+"/HEAD"]; strings.HasPrefix(sym, "Refs/remotes/"+remote+"/") {
			r = strings.TrimPrefix(sym, "Refs/remotes/"+remote+"/")
		}
	}
	for _, b := range []string{r, "main", "master"} {
		if b != "" && m.Has("Refs/heads/"+b) {
			return b
		}
	}
	return ""
}

// RemoteDefault is remote's default branch (origin/main): its HEAD, else
// its main or master.
func RemoteDefault(repo, remote string) string {
	if r, ok := Line(repo, "symbolic-ref", "--quiet", "--short", "Refs/remotes/"+remote+"/HEAD"); ok {
		return r
	}
	for _, b := range []string{"main", "master"} {
		if OK(repo, "show-ref", "--verify", "--quiet", "Refs/remotes/"+remote+"/"+b) {
			return remote + "/" + b
		}
	}
	return ""
}

// LocalDefault is the local default branch: the one the remote's HEAD
// names, else main or master; "" when there is none.
func LocalDefault(repo string) string {
	r := ""
	if rm := PreferredRemote(repo); rm != "" {
		if h, ok := Line(repo, "symbolic-ref", "--quiet", "--short", "Refs/remotes/"+rm+"/HEAD"); ok {
			r = h[strings.IndexByte(h, '/')+1:]
		}
	}
	for _, b := range []string{r, "main", "master"} {
		if b != "" && OK(repo, "show-ref", "--verify", "--quiet", "Refs/heads/"+b) {
			return b
		}
	}
	return ""
}

// WorkTree is the work tree holding directory dir: the nearest ancestor with
// a .git, as a physical path like git's.
func WorkTree(dir string) (string, bool) {
	d, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", false
	}
	if !filepath.IsAbs(d) {
		if d, err = filepath.Abs(d); err != nil {
			return "", false
		}
	}
	if st, err := os.Stat(d); err != nil || !st.IsDir() {
		return "", false
	}
	if d == "/" {
		d = ""
	}
	for {
		if files.Exists(d + "/.git") {
			if d == "" {
				return "/", true
			}
			return d, true
		}
		if d == "" {
			return "", false
		}
		d = d[:strings.LastIndexByte(d, '/')]
	}
}

// FileWorkTree is the work tree holding a file that may not exist yet, in
// directories that may not exist yet.
func FileWorkTree(f string) (string, bool) {
	d := f[:max(strings.LastIndexByte(f, '/'), 0)]
	for d != "" {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			break
		}
		d = d[:max(strings.LastIndexByte(d, '/'), 0)]
	}
	if d == "" {
		d = "/"
	}
	return WorkTree(d)
}

// Output runs git in dir; its output without the trailing newline.
func Output(dir string, env []string, stdin string, args ...string) (string, error) {
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if env != nil {
		c.Env = append(os.Environ(), env...)
	}
	if stdin != "" {
		c.Stdin = strings.NewReader(stdin)
	}
	var out bytes.Buffer
	c.Stdout = &out
	err := c.Run()
	return strings.TrimRight(out.String(), "\n"), err
}

// where is the repository and branch checked out at a root: read from its .git files without running git.
// Where is a work tree as weaver names it: its repository's name, its
// branch (or short commit), and its git directory.
type Where struct{ Repo, Branch, GitDir string }

// WhereOf is the work tree at root, read from its .git without running
// git.
func WhereOf(root string) Where {
	w := Where{Repo: root[strings.LastIndexByte(root, '/')+1:]}
	var gd, common string
	if st, err := os.Stat(root + "/.git"); err == nil && st.IsDir() {
		gd = root + "/.git"
		common = gd
	} else if err == nil && st.Mode().IsRegular() {
		l, _ := files.FirstLine(root + "/.git")
		gd = strings.TrimPrefix(l, "gitdir: ")
		if !strings.HasPrefix(gd, "/") {
			gd = root + "/" + gd
		}
		common = gd
		if files.Exists(gd + "/commondir") {
			l, _ := files.FirstLine(gd + "/commondir")
			switch {
			case strings.HasPrefix(l, "/"):
				common = l
			case l != "":
				common = gd + "/" + l
			}
		}
	} else {
		return w
	}
	up := 0
	for {
		switch {
		case strings.HasSuffix(common, "/.."):
			common = strings.TrimSuffix(common, "/..")
			up++
			continue
		case strings.HasSuffix(common, "/.") || strings.HasSuffix(common, "/"):
			common = strings.TrimSuffix(common, ".")
			common = strings.TrimSuffix(common, "/")
			continue
		}
		break
	}
	for ; up > 0; up-- {
		if i := strings.LastIndexByte(common, '/'); i >= 0 {
			common = common[:i]
		}
	}
	last := common[strings.LastIndexByte(common, '/')+1:]
	switch {
	case last == ".git" || last == ".bare":
		p := common[:max(strings.LastIndexByte(common, '/'), 0)]
		w.Repo = p[strings.LastIndexByte(p, '/')+1:]
	case strings.HasSuffix(last, ".git"):
		w.Repo = strings.TrimSuffix(last, ".git")
	}
	w.GitDir = gd
	head, _ := files.FirstLine(gd + "/HEAD")
	switch {
	case strings.HasPrefix(head, "ref: refs/heads/"):
		w.Branch = strings.TrimPrefix(head, "ref: refs/heads/")
	case head == "":
	default:
		r := []rune(head)
		w.Branch = string(r[:min(7, len(r))])
	}
	return w
}
