package wvr

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/git"
	"github.com/robbowland/weaver/internal/items"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
	"github.com/robbowland/weaver/internal/tmux"
)

// checkoutFor is the main checkout of GitHub repository owner/name, from
// those weaver knows: first (the one you are in), the repositories of
// recent threads and agents, and those zoxide knows of the same name. ""
// when none is.
func (e *Env) checkoutFor(repo, first string) string {
	name := strings.ToLower(repo[strings.IndexByte(repo, '/')+1:])
	var named, rest []string
	for _, l := range strings.Split(e.recentRepos(first), "\n") {
		d, _, _ := strings.Cut(l, "\t")
		if d == "" {
			continue
		}
		if strings.ToLower(filepath.Base(d)) == name {
			named = append(named, d)
		} else {
			rest = append(rest, d)
		}
	}
	if h := os.Getenv("HOME"); h != "" {
		for _, p := range []string{"code", "src", "dev", "projects"} {
			if d := filepath.Join(h, p, repo[strings.IndexByte(repo, '/')+1:]); files.Exists(d + "/.git") {
				named = append(named, d)
			}
		}
	}
	// zoxide's directories of another name are many: only weaver's own.
	for i, d := range append(named, rest...) {
		if i >= len(named) && i-len(named) >= 20 {
			break
		}
		if strings.EqualFold(items.GitHubRepo(d), repo) {
			if r, _, ok := git.RepoOf(d); ok {
				return r
			}
		}
	}
	return ""
}

// threadOnBranch is the agent pane of a running thread of this session on
// branch of repo, or "".
func (e *Env) threadOnBranch(repo, branch string) tmux.PaneID {
	live := map[tmux.PaneID]bool{}
	ps, _ := e.Panes()
	for _, p := range ps {
		if !p.Exited {
			live[p.ID] = true
		}
	}
	for _, f := range e.SessionFiles("thread") {
		t, err := store.ReadThread(f)
		if err != nil || t.Repo != repo || t.Branch != branch {
			continue
		}
		if p := session.PaneOfFile(f); live[p] {
			return p
		}
	}
	return ""
}
