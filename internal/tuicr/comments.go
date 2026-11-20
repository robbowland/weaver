// Package tuicr reads the review comments you write in tuicr, the review
// panel (Rob's fork, 0.28): its own files, read-only, as a hook can afford
// on every prompt (no tuicr process). What a thread's agent has been given
// of them is weaver's, kept per thread.
package tuicr

import (
	"bufio"
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/robbowland/weaver/internal/store"
)

// Comment is one of your review comments.
type Comment struct {
	ID string
	// Location is where it is, as `tuicr review comments` says it:
	// path:line, path:start-end, with " [old]" on a deleted line; the path
	// alone for a file's comment, "review" for the review's.
	Location string
	// Type is its type as your tuicr settings name it (issue, suggestion);
	// "" for none.
	Type    string
	Text    string
	Created time.Time
}

// Dir is tuicr's reviews directory: its data directory's (the directories
// crate's: ~/Library/Application Support/tuicr on macOS, else
// $XDG_DATA_HOME/tuicr or ~/.local/share/tuicr) reviews.
func Dir() string {
	home := os.Getenv("HOME")
	data := home + "/Library/Application Support"
	if runtime.GOOS != "darwin" {
		data = cmp.Or(os.Getenv("XDG_DATA_HOME"), home+"/.local/share")
	}
	return data + "/tuicr/reviews"
}

// index is tuicr's index of its sessions (reviews/index.json): each
// session's file, and for a checkout's (local) session its checkout and
// how many comments it holds.
type index struct {
	Entries map[string][]struct {
		Path string `json:"path"`
		Kind struct {
			Type string `json:"type"`
		} `json:"kind"`
		Repo    string `json:"canonical_repo_path"`
		Display struct {
			Comments int `json:"comment_count"`
		} `json:"display"`
	} `json:"entries"`
}

// session is what weaver reads of a session's file.
type session struct {
	Review []comment `json:"review_comments"`
	Files  map[string]struct {
		File  []comment            `json:"file_comments"`
		Lines map[string][]comment `json:"line_comments"`
	} `json:"files"`
}

type comment struct {
	ID      string    `json:"id"`
	Content string    `json:"content"`
	Type    string    `json:"comment_type"`
	Created time.Time `json:"created_at"`
	Side    string    `json:"side"`
	Range   *struct {
		Start int `json:"start"`
		End   int `json:"end"`
	} `json:"line_range"`
	Author string `json:"author"`
}

// Comments are your comments in tuicr's local review sessions (reviews,
// its directory) of the checkouts dirs, written at or after since (epoch
// s), oldest first. Yours: written as you (tuicr's author `user`, or your
// tuicr settings' username), never an agent's (`tuicr review add
// --username`). None when tuicr has none, or no files at all.
func Comments(reviews string, since int64, dirs ...string) []Comment {
	var ix index
	if store.Read(reviews+"/index.json", &ix) != nil {
		return nil
	}
	want := map[string]bool{}
	for _, d := range dirs {
		if d == "" {
			continue
		}
		// tuicr keys a checkout by its canonical path (Rust's
		// fs::canonicalize: links resolved, no trailing slash).
		if r, err := filepath.EvalSymlinks(d); err == nil {
			want[r] = true
		}
	}
	var out []Comment
	seen := map[string]bool{}
	var me []string
	for _, es := range ix.Entries {
		for _, en := range es {
			// The index counts each session's comments as it saves it:
			// a session with none is not read.
			if en.Kind.Type != "local" || !want[filepath.Clean(en.Repo)] || en.Display.Comments == 0 {
				continue
			}
			var s session
			if store.Read(filepath.Join(reviews, en.Path), &s) != nil {
				continue
			}
			if me == nil {
				me = authors()
			}
			for _, c := range s.comments() {
				if seen[c.ID] || !slices.Contains(me, c.author) || c.Created.Unix() < since {
					continue
				}
				seen[c.ID] = true
				out = append(out, c.Comment)
			}
		}
	}
	slices.SortStableFunc(out, func(a, b Comment) int {
		return cmp.Or(a.Created.Compare(b.Created), strings.Compare(a.Location, b.Location))
	})
	return out
}

type authored struct {
	Comment
	author string
}

// comments are the session's comments, each where tuicr says it is.
func (s session) comments() []authored {
	var out []authored
	add := func(loc string, c comment) {
		if c.ID == "" {
			return
		}
		t := c.Type
		if strings.EqualFold(t, "none") {
			t = ""
		}
		out = append(out, authored{Comment{ID: c.ID, Location: loc, Type: t, Text: c.Content, Created: c.Created},
			cmp.Or(c.Author, "user")})
	}
	for _, c := range s.Review {
		add("review", c)
	}
	for path, f := range s.Files {
		for _, c := range f.File {
			add(path, c)
		}
		for line, cs := range f.Lines {
			n, _ := strconv.Atoi(line)
			for _, c := range cs {
				start, end := n, n
				if c.Range != nil {
					start, end = c.Range.Start, c.Range.End
				}
				loc := fmt.Sprintf("%s:%d", path, start)
				if end != start {
					loc += fmt.Sprintf("-%d", end)
				}
				if c.Side == "old" {
					loc += " [old]"
				}
				add(loc, c)
			}
		}
	}
	return out
}

// authors are the names your comments carry: tuicr's default, and the
// username your tuicr settings give (its config.toml's top-level
// `username`, in $XDG_CONFIG_HOME/tuicr or ~/.config/tuicr).
func authors() []string {
	me := []string{"user"}
	dir := cmp.Or(os.Getenv("XDG_CONFIG_HOME"), os.Getenv("HOME")+"/.config")
	f, err := os.Open(dir + "/tuicr/config.toml")
	if err != nil {
		return me
	}
	defer func() { _ = f.Close() }() // read only: nothing to lose
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(l, "[") {
			break // a table: top-level keys come first
		}
		k, v, ok := strings.Cut(l, "=")
		if !ok || strings.TrimSpace(k) != "username" {
			continue
		}
		if u, err := strconv.Unquote(strings.TrimSpace(v)); err == nil && u != "" {
			me = append(me, u)
		}
	}
	return me
}

// unread are the comments of cs that the record given (one id a line)
// does not hold: not yet given to the thread's agent.
func unread(cs []Comment, given string) []Comment {
	if len(cs) == 0 {
		return nil
	}
	had := map[string]bool{}
	if b, err := os.ReadFile(given); err == nil {
		for _, id := range strings.Split(string(b), "\n") {
			had[id] = true
		}
	}
	var out []Comment
	for _, c := range cs {
		if !had[c.ID] {
			out = append(out, c)
		}
	}
	return out
}

// give records cs as given, in the record given.
func give(given string, cs []Comment) error {
	if len(cs) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(given), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(given, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, c := range cs {
		b.WriteString(c.ID + "\n")
	}
	_, err = f.WriteString(b.String())
	return cmp.Or(err, f.Close())
}

// givenFile is thread th's record of the comments its agent has been
// given, under weaver's state directory stateDir.
func givenFile(stateDir string, th store.Thread) string {
	return th.Dir(stateDir) + "/review-comments"
}

// ThreadUnread are your comments that thread th's agent has not been
// given yet: on its worktree, or root, the one its agent works in now (its
// review panel's), since the thread was made. What a prompt of yours
// carries next (`wvr hook`), and what its card counts.
func ThreadUnread(stateDir string, th store.Thread, root string) []Comment {
	return unread(Comments(Dir(), th.Created, th.Worktree, root), givenFile(stateDir, th))
}

// ThreadGive records cs as given to thread th's agent: ThreadUnread holds
// them no more.
func ThreadGive(stateDir string, th store.Thread, cs []Comment) error {
	return give(givenFile(stateDir, th), cs)
}
