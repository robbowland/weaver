package rex

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/robbowland/weaver/internal/files"
)

// ids is weaver's numbers for Rex's blocks and windows. Weaver's files and
// records name a pane by a number, and Rex names its
// blocks and windows by UUIDs, so each one weaver meets gets the next
// number, kept in dir:
//
//	n/<num>        the block or window (block:..., window:...) num names
//	u/<id>         num, for the block or window id (its ':' as '_')
//	cmd/<num>      the argv weaver started in block num, space-separated
//	want/<num>     the place weaver gave window num (home 0, a thread its
//	               number, a view 900 and on)
//	view/<num>     the view window num shows
//	split/<num>    the ratio weaver last gave the split beside block num
//	               (SetWidth), for Moved to tell from one you moved
//	in/<num>       the session the block or window num is in, as Rex
//	               calls it (session:...); none for one numbered before
//	               weaver kept it, all in its home session
//	sessions/<id>  a session weaver made: its name, when, its server and,
//	               for a thread's, the session it is a thread of (its home)
//	said/<sid>     the block of the message last shown in session sid (Say)
//	remote-off     when weaver last said the app takes no actions
//	lock, start    held while a number is given or moved, and while wvr
//	               asks whether weaver's session runs and makes it (Ensure)
//	say            held while a message replaces the last (Say)
//
// A number is never given twice, and follows its block when weaver starts
// another in its place (Respawn); the block it leaves gets none again
// (its u/<id> holds retired).
type ids struct{ dir string }

// retired is u/<id> of a block another took the number of (Respawn): it
// is ending, and what it says as it does (an agent's last hooks) is no
// pane's, neither the number's new block's nor one of its own.
const retired = "-"

var errRetired = errors.New("rex: a block replaced")

func (r ids) path(kind, name string) string { return filepath.Join(r.dir, kind, name) }

func fileName(id string) string { return strings.ReplaceAll(id, ":", "_") }

// num is id's number, given it now if it has none, in session in; none
// for a block replaced (errRetired).
func (r ids) num(id, in string) (string, error) {
	if id == "" {
		return "", errors.New("rex: no id")
	}
	if n, ok, err := r.has(id); ok {
		return n, err
	}
	unlock, err := r.lock()
	if err != nil {
		return "", err
	}
	defer unlock()
	if n, ok, err := r.has(id); ok {
		return n, err
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
	if err := r.note("in", n, in); err != nil {
		return "", err
	}
	return n, r.write("", "next", strconv.Itoa(next+1))
}

// has is what u/<id> says: id's number, or errRetired; false when it
// says nothing.
func (r ids) has(id string) (string, bool, error) {
	n, err := files.FirstLine(r.path("u", fileName(id)))
	switch {
	case err != nil || n == "":
		return "", false, nil
	case n == retired:
		return "", true, errRetired
	}
	return n, true, nil
}

// known is id's number when it has one; it gives none.
func (r ids) known(id string) (string, bool) {
	n, ok, err := r.has(id)
	return n, ok && err == nil
}

// id is what number n names.
func (r ids) id(n string) (string, bool) {
	if n == "" || strings.ContainsAny(n, "/.") {
		return "", false
	}
	id, err := files.FirstLine(r.path("n", n))
	return id, err == nil && id != ""
}

// replace has number n name the block start makes (its id) from now on: a
// block started in the place of n's. The lock is held from before start
// till n names the block, so its program, asking its number however soon
// (num, which waits for the lock to give one), gets n, never one of its
// own; the old block is retired. When n cannot be kept for the block, drop
// closes it, and n names the old block still: a failed replace leaves one
// block, the old.
func (r ids) replace(n string, start func() (string, error), drop func(id string)) error {
	unlock, err := r.lock()
	if err != nil {
		return err
	}
	defer unlock()
	id, err := start()
	if err != nil {
		return err
	}
	old, had := r.id(n)
	if err := r.write("u", fileName(id), n); err != nil {
		drop(id)
		return err
	}
	if err := r.write("n", n, id); err != nil {
		os.Remove(r.path("u", fileName(id)))
		drop(id)
		return err
	}
	if had && old != id {
		_ = r.write("u", fileName(old), retired)
	}
	return nil
}

// unretire forgets that block id was retired (replace), when it was: a
// block that has not gone after all gets a number of its own when next
// asked.
func (r ids) unretire(id string) {
	if n, err := files.FirstLine(r.path("u", fileName(id))); err == nil && n == retired {
		os.Remove(r.path("u", fileName(id)))
	}
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

// madeSession is weaver's record of a Rex session it made
// (sessions/<id>, made): the name it gave it, when (epoch seconds to the
// millisecond; whole seconds in older records), the pid of the server
// it made it on (0 in older records), and the session it is a thread of
// (none for a home session). A thread's session has its home's name:
// weaver's files are keyed by the home, whichever of its sessions an
// agent runs in.
type madeSession struct {
	name   string
	at     float64
	server int
	home   string
}

// session is weaver's record of Rex session id, when weaver made it.
func (r ids) session(id string) (madeSession, bool) {
	b, err := os.ReadFile(r.path("sessions", fileName(id)))
	if err != nil {
		return madeSession{}, false
	}
	ls := strings.Split(strings.TrimSpace(string(b)), "\n")
	m := madeSession{name: ls[0]}
	if len(ls) > 1 {
		m.at, _ = strconv.ParseFloat(ls[1], 64)
	}
	if len(ls) > 2 {
		m.server, _ = strconv.Atoi(ls[2])
	}
	if len(ls) > 3 {
		m.home = ls[3]
	}
	return m, m.name != ""
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

// latest is the home session weaver made last of a name that matches,
// and its name.
func (r ids) latest(match func(name string) bool) (string, string, bool) {
	ents, _ := os.ReadDir(filepath.Join(r.dir, "sessions"))
	var best, bestName string
	at := -1.0
	for _, d := range ents {
		id := strings.Replace(d.Name(), "_", ":", 1)
		if m, ok := r.session(id); ok && m.home == "" && match(m.name) && m.at > at {
			best, bestName, at = id, m.name, m.at
		}
	}
	return best, bestName, best != ""
}

// made records session id as weaver's, named name, made at, on the server
// whose pid is server, a thread of session home ("" for a home session)
// (sessions/<id>: the name, the time in epoch seconds to the millisecond,
// so the later of two made in one second is told, the pid and the home).
func (r ids) made(id, name string, at time.Time, server int, home string) error {
	t := strconv.FormatFloat(float64(at.UnixMilli())/1000, 'f', 3, 64)
	v := name + "\n" + t + "\n" + strconv.Itoa(server)
	if home != "" {
		v += "\n" + home
	}
	return r.write("sessions", fileName(id), v)
}

// threads are the sessions weaver made as threads of session home.
func (r ids) threads(home string) []string {
	var out []string
	for _, id := range r.allThreads() {
		if m, _ := r.session(id); m.home == home && home != "" {
			out = append(out, id)
		}
	}
	return out
}

// allThreads are the sessions weaver made as threads, of any home.
func (r ids) allThreads() []string {
	ents, _ := os.ReadDir(filepath.Join(r.dir, "sessions"))
	var out []string
	for _, d := range ents {
		id := strings.Replace(d.Name(), "_", ":", 1)
		if m, ok := r.session(id); ok && m.home != "" {
			out = append(out, id)
		}
	}
	return out
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
func (r ids) lock() (func(), error) { return r.lockOn("lock") }

// lockOn holds file name in dir locked (flock) till the func it gives is
// called: one holder at a time, across weaver's processes.
func (r ids) lockOn(name string) (func(), error) {
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(r.dir, name), os.O_CREATE|os.O_RDWR, 0o644)
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
