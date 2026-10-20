package session

import (
	"encoding/binary"
	"reflect"
	"testing"
)

func TestParseProcArgs(t *testing.T) {
	b := binary.LittleEndian.AppendUint32(nil, 2)
	b = append(b, "/usr/bin/uv\x00\x00\x00\x00uv\x00run\x00TMUX_PANE=%1\x00PWD=/w\x00\x00junk"...)
	argv, env, ok := parseProcArgs(b)
	if !ok || !reflect.DeepEqual(argv, []string{"uv", "run"}) || !reflect.DeepEqual(env, map[string]string{"TMUX_PANE": "%1", "PWD": "/w"}) {
		t.Fatalf("got %q %q %v", argv, env, ok)
	}
	if _, _, ok := parseProcArgs(binary.LittleEndian.AppendUint32(nil, 3)); ok {
		t.Fatal("a truncated block parsed")
	}
}

// What an agent left running is a leftover only when it runs from the
// worktree it was started in: a venv's python (a link elsewhere, taken as
// run), a script there, a relative path from there; not a daemon whose
// program lives elsewhere, nor a bare command name.
func TestRunsFrom(t *testing.T) {
	pwd := map[string]string{"PWD": "/w/locus"}
	for _, c := range []struct {
		argv []string
		want bool
	}{
		{[]string{"/w/locus/.venv/bin/python", "-m", "app"}, true},
		{[]string{"node", "/w/node_modules/vite/bin/vite.js"}, true},
		{[]string{"./bin/srv"}, true},
		{[]string{"/home/u/.codex/bin/codex", "app-server"}, false},
		{[]string{"uv", "run", "/w/x.py"}, false},
		{[]string{"/wx/bin/srv"}, false},
	} {
		tree := []proc{{argv: []string{"uv", "run"}, env: pwd}, {argv: c.argv, env: pwd}}
		if got := runsFrom(tree, "/w"); got != c.want {
			t.Errorf("%q: got %v, want %v", c.argv, got, c.want)
		}
	}
	if runsFrom([]proc{{argv: []string{"/bin/sleep"}}}, "/") {
		t.Error("everything runs from /")
	}
}
