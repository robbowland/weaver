package wvr

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
	"github.com/robbowland/weaver/internal/tmux"
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

// keptSays is where a removed worktree's ignored files went, for what says
// it was removed; "" for none.
func keptSays(kept string) string {
	if kept == "" {
		return ""
	}
	return "; its ignored files kept in " + tildify(kept)
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
// Worktrunk when it is there (its hooks run), else git. Its ignored files,
// which would go with it, are kept first (keepIgnored): where, "" for none.
// What they print goes to out.
func (e *Env) removeWorktree(dir, repo string, out io.Writer) (string, error) {
	if repo == "" {
		repo = dir
	}
	kept, err := e.keepIgnored(dir)
	if err != nil {
		return "", fmt.Errorf("could not keep its ignored files: %w", err)
	}
	var c *exec.Cmd
	if _, err := exec.LookPath("wt"); err == nil && os.Getenv("WEAVER_WORKTRUNK") != "0" {
		c = exec.Command("wt", "-C", repo, "remove", "--no-delete-branch", "--foreground", dir)
	} else {
		c = exec.Command("git", "-C", repo, "worktree", "remove", dir)
	}
	c.Stdout, c.Stderr = out, out
	if err := c.Run(); err != nil {
		if kept != "" {
			os.RemoveAll(kept)
		}
		return "", err
	}
	return kept, nil
}

// keepIgnored copies worktree dir's ignored files (an .env, local
// settings: git does not have them, and a worktree removed takes them) to
// $STATE_DIR/trash/<name>-<when>/, kept a month; not whole ignored
// directories, which a build makes again (node_modules/, .venv/), nor
// Finder's .DS_Store. Where, "" when there were none.
func (e *Env) keepIgnored(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "status", "--porcelain", "-z", "--untracked-files=all", "--ignored=matching").Output()
	if err != nil {
		return "", err
	}
	var fs []string
	for _, l := range strings.Split(string(out), "\x00") {
		if p, ok := strings.CutPrefix(l, "!! "); ok && !strings.HasSuffix(p, "/") && filepath.Base(p) != ".DS_Store" {
			fs = append(fs, p)
		}
	}
	if len(fs) == 0 {
		return "", nil
	}
	to := filepath.Join(e.StateDir, "trash", filepath.Base(dir)+"-"+time.Now().Format("20060102-150405"))
	for _, f := range fs {
		if err := copyFile(filepath.Join(dir, f), filepath.Join(to, f)); err != nil {
			os.RemoveAll(to)
			return "", err
		}
	}
	return to, nil
}

// copyFile copies regular file from to to (its directories made), with its
// mode; a link as a link.
func copyFile(from, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	st, err := os.Lstat(from)
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		l, err := os.Readlink(from)
		if err != nil {
			return err
		}
		return os.Symlink(l, to)
	}
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, b, st.Mode().Perm())
}

// worktreeUsers is what uses worktree dir, as close's card says it:
// threads (open in any session, or lost) working there, and panes (on
// weaver's server, and the one we run in) in it, but panes except (a
// thread being closed: its agent, nvim and the pane closing it) and their
// threads. Close's offer, its last check, and the sweep all ask it.
func (e *Env) worktreeUsers(dir string, except ...tmux.PaneID) []string {
	skip := map[tmux.PaneID]bool{}
	for _, p := range except {
		if p != "" {
			skip[p] = true
		}
	}
	var us []string
	ms, _ := filepath.Glob(e.StateDir + "/*_*.thread")
	for _, m := range ms {
		if skip[session.PaneOfFile(m)] && session.KeyOfFile(m) == e.SKey {
			continue
		}
		if t, err := store.ReadThread(m); err == nil && t.Worktree == dir {
			us = append(us, fmt.Sprintf("thread %02d", t.Num))
		}
	}
	ls, _ := filepath.Glob(e.LostDir + "/*.json")
	for _, f := range ls {
		if l, err := store.ReadLost(f); err == nil && l.Thread.Worktree == dir {
			us = append(us, fmt.Sprintf("lost thread %02d", l.Thread.Num))
		}
	}
	seen := map[string]bool{}
	panes := func(args ...string) {
		out, err := session.RunTmux(append(args, "list-panes", "-a", "-F", "#{pane_id} #{pane_dead} #{pane_current_path}")...)
		if err != nil {
			return
		}
		for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			f := strings.SplitN(l, " ", 3)
			if len(f) != 3 || f[1] == "1" || seen[f[0]] {
				continue
			}
			seen[f[0]] = true
			if p, ok := tmux.ParsePane(f[0]); ok && !skip[p] && (f[2] == dir || strings.HasPrefix(f[2], dir+"/")) {
				us = append(us, "pane "+string(p))
			}
		}
	}
	panes(e.TSock...)
	if e.InTmux {
		panes()
	}
	return us
}

// worktreesMadeByThreads are the worktrees weaver made for threads, open
// and lost, for a list from before there was one.
func (e *Env) worktreesMadeByThreads() []madeWorktree {
	var made []madeWorktree
	add := func(t store.Thread) {
		if t.Worktree != "" && t.WorktreeCreated && !t.Here {
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
		if l, err := store.ReadLost(f); err == nil {
			add(l.Thread)
		}
	}
	return made
}

// SweepWorktrees removes the worktrees weaver made that nothing uses and
// nothing in would be lost (an hour after they were made, so as not to
// race a thread being opened), and says what it removed to out.
func (e *Env) SweepWorktrees(out io.Writer) []string {
	made := e.worktreesMadeByThreads()
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
		if time.Now().Unix()-w.at < 3600 || worktreeRemovable(w.dir) != "" || len(e.worktreeUsers(w.dir)) > 0 {
			continue
		}
		if kept, err := e.removeWorktree(w.dir, w.repo, io.Discard); err == nil {
			removed = append(removed, w.dir)
			fmt.Fprintln(out, "weaver: removed "+tildify(w.dir)+" (its branch stays)"+keptSays(kept))
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
