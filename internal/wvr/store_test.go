package wvr

import (
	"os"
	"path/filepath"
	"testing"
)

// A member of an unexpected type reads as its zero value; the rest reads.
func TestReadStateTolerant(t *testing.T) {
	f := filepath.Join(t.TempDir(), "s.json")
	for _, c := range []struct {
		json   string
		ok     bool
		prompt string
	}{
		{`{"state":"done","turn":"3","prompt":"hi","tasks":{"x":1}}`, true, "hi"},
		{`{"state":"done","prompt":"hi"}` + "\n" + `junk`, true, "hi"},
		{`["state","done"]`, false, ""},
		{`"done"`, false, ""},
	} {
		if err := os.WriteFile(f, []byte(c.json), 0o644); err != nil {
			t.Fatal(err)
		}
		s, err := readState(f)
		if (err == nil) != c.ok || s.Prompt != c.prompt {
			t.Errorf("%s: err %v prompt %q, want ok %v prompt %q", c.json, err, s.Prompt, c.ok, c.prompt)
		}
		if c.ok && (s.State != StateDone || s.Turn != 0 || len(s.Tasks) != 0) {
			t.Errorf("%s: %+v", c.json, s)
		}
	}
}
