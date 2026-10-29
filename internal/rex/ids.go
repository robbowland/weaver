package rex

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/robbowland/weaver/internal/files"
)

// ids is weaver's numbers for Rex's blocks and windows. Weaver's files and
// records name a pane by a number (tmux's %3 is 3), and Rex names its
// blocks and windows by UUIDs, so each one weaver meets gets the next
// number, kept in dir:
//
//	n/<num>        the block or window (block:..., window:...) num names
//	u/<id>         num, for the block or window id (its ':' as '_')
//	cmd/<num>      the argv weaver started in block num, space-separated
//	want/<num>     the place weaver gave window num (home 0, a thread its
//	               number, a view 900 and on)
//	view/<num>     the view window num shows
//
// A number is never given twice, and follows its block when weaver starts
// another in its place (Respawn).
type ids struct{ dir string }

func (r ids) path(kind, name string) string { return filepath.Join(r.dir, kind, name) }

func fileName(id string) string { return strings.ReplaceAll(id, ":", "_") }

// num is id's number, given it now if it has none.
func (r ids) num(id string) (string, error) {
	if id == "" {
		return "", errors.New("rex: no id")
	}
	if n, err := files.FirstLine(r.path("u", fileName(id))); err == nil && n != "" {
		return n, nil
	}
	unlock, err := r.lock()
	if err != nil {
		return "", err
	}
	defer unlock()
	if n, err := files.FirstLine(r.path("u", fileName(id))); err == nil && n != "" {
		return n, nil
	}
	next := 1
	if l, err := files.FirstLine(r.path("", "next")); err == nil {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			next = v
		}
	}
	for files.Exists(r.path("n", strconv.Itoa(next))) {
		next++
	}
	n := strconv.Itoa(next)
	if err := r.write("n", n, id); err != nil {
		return "", err
	}
	if err := r.write("u", fileName(id), n); err != nil {
		return "", err
	}
	return n, r.write("", "next", strconv.Itoa(next+1))
}

// known is id's number when it has one; it gives none.
func (r ids) known(id string) (string, bool) {
	n, err := files.FirstLine(r.path("u", fileName(id)))
	return n, err == nil && n != ""
}

// id is what number n names.
func (r ids) id(n string) (string, bool) {
	if n == "" || strings.ContainsAny(n, "/.") {
		return "", false
	}
	id, err := files.FirstLine(r.path("n", n))
	return id, err == nil && id != ""
}

// repoint has number n name id from now on: a block started in the place
// of n's.
func (r ids) repoint(n, id string) error {
	unlock, err := r.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if old, ok := r.id(n); ok {
		os.Remove(r.path("u", fileName(old)))
	}
	if err := r.write("n", n, id); err != nil {
		return err
	}
	return r.write("u", fileName(id), n)
}

// note keeps what weaver knows of number n under kind (cmd, want, view);
// empty forgets it.
func (r ids) note(kind, n, v string) error {
	if v == "" {
		os.Remove(r.path(kind, n))
		return nil
	}
	return r.write(kind, n, v)
}

func (r ids) noted(kind, n string) string {
	v, _ := os.ReadFile(r.path(kind, n))
	return strings.TrimSuffix(string(v), "\n")
}

// session is weaver's name for Rex session id, and when weaver made it,
// when weaver made it (sessions/<id>: the name, then the time).
func (r ids) session(id string) (string, int64, bool) {
	b, err := os.ReadFile(r.path("sessions", fileName(id)))
	if err != nil {
		return "", 0, false
	}
	name, made, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	t, _ := strconv.ParseInt(made, 10, 64)
	return name, t, name != ""
}

// named is the Rex session weaver made last for name.
func (r ids) named(name string) (string, bool) {
	id, _, ok := r.latest(func(n string) bool { return n == name })
	return id, ok
}

// keyed is the Rex session weaver made last whose name is keyed skey, and
// that name.
func (r ids) keyed(skey string) (string, string, bool) {
	return r.latest(func(n string) bool { return Key(n) == skey })
}

// latest is the session weaver made last of a name that matches, and its
// name.
func (r ids) latest(match func(name string) bool) (string, string, bool) {
	ents, _ := os.ReadDir(filepath.Join(r.dir, "sessions"))
	var best, bestName string
	var at int64 = -1
	for _, d := range ents {
		id := strings.Replace(d.Name(), "_", ":", 1)
		if n, made, ok := r.session(id); ok && match(n) && made > at {
			best, bestName, at = id, n, made
		}
	}
	return best, bestName, best != ""
}

func (r ids) made(id, name string, at int64) error {
	return r.write("sessions", fileName(id), name+"\n"+strconv.FormatInt(at, 10))
}

func (r ids) write(kind, name, v string) error {
	d := filepath.Join(r.dir, kind)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return err
	}
	f := filepath.Join(d, name)
	tmp := f + "." + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, []byte(v+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, f)
}

// lock is the registry's lock, held while a number is given or moved.
func (r ids) lock() (func(), error) {
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(r.dir, "lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
