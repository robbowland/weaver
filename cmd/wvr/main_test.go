package main

import (
	"os"
	"slices"
	"testing"
)

// wvr --rex and wvr start --rex start weaver on Rex, as WEAVER_MUX=rex does,
// and --tmux on tmux; every other command is left as it was.
func TestMuxFlag(t *testing.T) {
	for _, c := range []struct {
		cmd      string
		args     []string
		wantCmd  string
		wantArgs []string
		mux      string
	}{
		{"--rex", nil, "start", nil, "rex"},
		{"--rex", []string{"work"}, "start", []string{"work"}, "rex"},
		{"start", []string{"--rex", "work"}, "start", []string{"work"}, "rex"},
		{"--tmux", nil, "start", nil, "tmux"},
		{"start", []string{"work"}, "start", []string{"work"}, ""},
		{"new", []string{"--rex"}, "new", []string{"--rex"}, ""},
		{"", nil, "", nil, ""},
	} {
		t.Setenv("WEAVER_MUX", "")
		cmd, args := muxFlag(c.cmd, c.args)
		if cmd != c.wantCmd || !slices.Equal(args, c.wantArgs) || os.Getenv("WEAVER_MUX") != c.mux {
			t.Errorf("muxFlag(%q, %q) = %q %q, WEAVER_MUX %q; want %q %q, %q",
				c.cmd, c.args, cmd, args, os.Getenv("WEAVER_MUX"), c.wantCmd, c.wantArgs, c.mux)
		}
	}
}
