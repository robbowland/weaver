package rex

import "testing"

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
