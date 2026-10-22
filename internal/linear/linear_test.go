package linear

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// noKey is a Keychain with no key of weaver's, and none in the
// environment.
func noKey(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "security"), []byte("#!/bin/sh\nexit 44\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("LINEAR_API_KEY", "")
}

// Without a key, every section fails for that reason, and says so itself.
func TestFetchNoKey(t *testing.T) {
	noKey(t)
	got := Fetch()
	if len(got) != len(Sections) {
		t.Fatalf("%d sections", len(got))
	}
	for i, r := range got {
		if r.Section != Sections[i] || r.Err != ErrNoKey.Error() || !r.Fetched.IsZero() || len(r.Items) != 0 {
			t.Errorf("%+v", r)
		}
	}
	if _, err := Key(); !errors.Is(err, ErrNoKey) {
		t.Errorf("a key: %v", err)
	}
}
