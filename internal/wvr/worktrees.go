package wvr

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/store"
)

// The worktrees weaver made for threads, kept in $STATE_DIR/worktrees (a
// line each: the worktree, its repository, when) until they go, or until
// you keep one on purpose (close's "keep"). As the session starts and on
// `wvr gc`, one no thread (open or lost) and no pane is using goes once
// nothing in it would be lost (worktreeRemovable: clean, and its commits
// merged or pushed), its branch kept: so the worktrees of threads closed
// before their work had landed, forgotten, or lost for good do not pile up.

func (e *Env) worktreesFile() string { return e.StateDir + "/worktrees" }

// madeWorktree is a worktree weaver made, as its line says.
type madeWorktree struct {
	dir, repo string
	at        int64
}

// worktreesMade reads the list, as is.
func (e *Env) worktreesMade() []madeWorktree {
	b, _ := os.ReadFile(e.worktreesFile())
	var ws []madeWorktree
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Split(l, "\t")
		if len(f) != 3 || f[0] == "" {
			continue
		}
		at, _ := strconv.ParseInt(f[2], 10, 64)
		ws = append(ws, madeWorktree{f[0], f[1], at})
	}
	return ws
}

// editWorktrees changes the list (under its lock) as edit says.
func (e *Env) editWorktrees(edit func([]madeWorktree) []madeWorktree) {
	f := e.worktreesFile()
	bestEffort(os.MkdirAll(e.StateDir, 0o755))
	defer store.Lock(f)()
	var b strings.Builder
	for _, w := range edit(e.worktreesMade()) {
		fmt.Fprintf(&b, "%s\t%s\t%d\n", w.dir, w.repo, w.at)
	}
	bestEffort(store.WriteAtomic(f, []byte(b.String())))
}

// noteWorktree adds a worktree weaver made for a thread.
func (e *Env) noteWorktree(dir, repo string) {
	e.editWorktrees(func(ws []madeWorktree) []madeWorktree {
		return append(dropDir(ws, dir), madeWorktree{dir, repo, time.Now().Unix()})
	})
}

// forgetWorktree takes dir off the list: removed, or yours to keep.
func (e *Env) forgetWorktree(dir string) {
	e.editWorktrees(func(ws []madeWorktree) []madeWorktree { return dropDir(ws, dir) })
}

func hasDir(ws []madeWorktree, dir string) bool {
	for _, w := range ws {
		if w.dir == dir {
			return true
		}
	}
	return false
}

func dropDir(ws []madeWorktree, dir string) []madeWorktree {
	var out []madeWorktree
	for _, w := range ws {
		if w.dir != dir {
			out = append(out, w)
		}
	}
	return out
}

// removeWorktree removes worktree dir of repo, its branch kept: with
// Worktrunk when it is there (its hooks run), else git. What they print
// goes to out.
func removeWorktree(dir, repo string, out io.Writer) error {
	if repo == "" {
		repo = dir
	}
	var c *exec.Cmd
	if _, err := exec.LookPath("wt"); err == nil && os.Getenv("WEAVER_WORKTRUNK") != "0" {
		c = exec.Command("wt", "-C", repo, "remove", "--no-delete-branch", "--foreground", dir)
	} else {
		c = exec.Command("git", "-C", repo, "worktree", "remove", dir)
	}
	c.Stdout, c.Stderr = out, out
	return c.Run()
}

// worktreesInUse are the worktrees open and lost threads have (made, those
// weaver made for them), and the directories the session's panes are in.
func (e *Env) worktreesInUse() (threads map[string]bool, made []madeWorktree, cwds []string) {
	threads = map[string]bool{}
	add := func(t store.Thread) {
		if t.Worktree == "" {
			return
		}
		threads[t.Worktree] = true
		if t.WorktreeCreated && !t.Here {
			made = append(made, madeWorktree{t.Worktree, t.Repo, time.Now().Unix()})
		}
	}
	ms, _ := filepath.Glob(e.StateDir + "/*_*.thread")
	for _, m := range ms {
		if t, err := store.ReadThread(m); err == nil {
			add(t)
		}
	}
	ls, _ := filepath.Glob(e.LostDir + "/*.json")
	for _, f := range ls {
		var l store.Lost
		if b, err := os.ReadFile(f); err == nil && json.Unmarshal(b, &l) == nil {
			add(l.Thread)
		}
	}
	if out, err := e.Tmux("list-panes", "-a", "-F", "#{pane_current_path}"); err == nil {
		cwds = strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	}
	return threads, made, cwds
}

// SweepWorktrees removes the worktrees weaver made that nothing uses and
// nothing in would be lost (an hour after they were made, so as not to
// race a thread being opened), and says what it removed to out.
func (e *Env) SweepWorktrees(out io.Writer) []string {
	threads, made, cwds := e.worktreesInUse()
	ws := e.worktreesMade()
	// Threads' worktrees from before the list (or from a lost list) join it.
	var missing []madeWorktree
	for _, m := range made {
		if !hasDir(ws, m.dir) && !hasDir(missing, m.dir) {
			missing = append(missing, m)
		}
	}
	if len(missing) > 0 {
		e.editWorktrees(func(ws []madeWorktree) []madeWorktree {
			for _, m := range missing {
				if !hasDir(ws, m.dir) {
					ws = append(ws, m)
				}
			}
			return ws
		})
		ws = e.worktreesMade()
	}
	var removed, gone []string
	for _, w := range ws {
		if !files.Exists(w.dir) {
			gone = append(gone, w.dir)
			continue
		}
		if threads[w.dir] || time.Now().Unix()-w.at < 3600 || worktreeRemovable(w.dir) != "" {
			continue
		}
		used := false
		for _, c := range cwds {
			if c == w.dir || strings.HasPrefix(c, w.dir+"/") {
				used = true
			}
		}
		if used {
			continue
		}
		if removeWorktree(w.dir, w.repo, io.Discard) == nil {
			removed = append(removed, w.dir)
			fmt.Fprintln(out, "weaver: removed "+tildify(w.dir)+" (its branch stays)")
		}
	}
	if len(removed)+len(gone) > 0 {
		e.editWorktrees(func(ws []madeWorktree) []madeWorktree {
			for _, d := range append(removed, gone...) {
				ws = dropDir(ws, d)
			}
			return ws
		})
	}
	return removed
}
