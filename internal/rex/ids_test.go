package rex

import (
	"errors"
	"testing"
	"time"
)

// A session is found by its files' key, though the key turned its name's /
// into _: the latest weaver made of that name.
func TestKeyed(t *testing.T) {
	r := ids{t.TempDir()}
	for _, s := range []struct {
		id, name string
		made     int64
	}{{"session:old", "work/api", 1}, {"session:new", "work/api", 2}, {"session:other", "work_api2", 3}} {
		if err := r.made(s.id, s.name, time.Unix(s.made, 0), 0); err != nil {
			t.Fatal(err)
		}
	}
	if id, name, ok := r.keyed(Key("work/api")); !ok || id != "session:new" || name != "work/api" {
		t.Errorf("keyed: %q %q %v", id, name, ok)
	}
	if _, _, ok := r.keyed("rex-nothing"); ok {
		t.Error("a key no session has was found")
	}
}

// A block started in another's place (Respawn) takes its number, and the
// block it replaces, still ending, gets none: neither the number, nor a
// new one, for its program's last words (an agent's late hooks) or a look
// at the session before it has closed.
func TestReplaceRetires(t *testing.T) {
	r := ids{t.TempDir()}
	n, err := r.num("block:old")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.replace(n, func() (string, error) { return "block:new", nil }, func(string) { t.Error("dropped") }); err != nil {
		t.Fatal(err)
	}
	if got, err := r.num("block:new"); err != nil || got != n {
		t.Errorf("the new block: %q %v, want %q", got, err, n)
	}
	if id, _ := r.id(n); id != "block:new" {
		t.Errorf("%s names %q, want block:new", n, id)
	}
	if got, err := r.num("block:old"); !errors.Is(err, errRetired) {
		t.Errorf("the old block: %q %v, want none (retired)", got, err)
	}
	if _, ok := r.known("block:old"); ok {
		t.Error("the old block is known")
	}
	if next, _ := r.num("block:other"); next == n {
		t.Errorf("a new block took %q again", next)
	}
	// It did not close after all: it is a pane again, of a number of its
	// own; the number it gave up stays the new block's.
	r.unretire("block:old")
	if got, err := r.num("block:old"); err != nil || got == "" || got == n {
		t.Errorf("unretired: %q %v, want a number of its own", got, err)
	}
	if got, _ := r.num("block:new"); got != n {
		t.Errorf("the new block: %q, want %q", got, n)
	}
}

// Of two sessions of a name made within one second, the later is found;
// a record from before weaver kept milliseconds still reads.
func TestLatestWithinASecond(t *testing.T) {
	r := ids{t.TempDir()}
	at := time.Unix(1700000000, 0)
	if err := r.made("session:first", "w", at.Add(100*time.Millisecond), 0); err != nil {
		t.Fatal(err)
	}
	if err := r.made("session:second", "w", at.Add(900*time.Millisecond), 0); err != nil {
		t.Fatal(err)
	}
	if id, ok := r.named("w"); !ok || id != "session:second" {
		t.Errorf("named: %q %v, want session:second", id, ok)
	}
	if err := r.write("sessions", fileName("session:old"), "o\n1700000000"); err != nil {
		t.Fatal(err)
	}
	if m, ok := r.session("session:old"); !ok || m.name != "o" || m.at != 1700000000 || m.server != 0 {
		t.Errorf("an older record: %+v %v", m, ok)
	}
}
