package rex

import (
	"errors"
	"testing"
)

// A session is found by its files' key, though the key turned its name's /
// into _: the latest weaver made of that name.
func TestKeyed(t *testing.T) {
	r := ids{t.TempDir()}
	for _, s := range []struct {
		id, name string
		made     int64
	}{{"session:old", "work/api", 1}, {"session:new", "work/api", 2}, {"session:other", "work_api2", 3}} {
		if err := r.made(s.id, s.name, s.made); err != nil {
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
}
