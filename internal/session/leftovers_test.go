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

// Of a tree launchd adopted, what a gone pane left goes: each process that
// runs from its checkout, the pane known from its own environment (or its
// parent's, where macOS hides it), with all under it. Never a process whose
// own environment says no pane, though another in the tree says one (CI's
// runner, whose test script runs from the checkout while a hook of a pane
// runs under it); never a daemon above, from elsewhere.
func TestCondemned(t *testing.T) {
	pane := map[string]string{"WEAVER_TMUX_SESSION": "weaver", "TMUX": "/s,1,0", "TMUX_PANE": "%7", "PWD": "/w"}
	tree := []proc{
		{pid: 2, argv: []string{"/runner/Runner.Listener"}, env: map[string]string{"HOME": "/h"}},
		{pid: 3, argv: []string{"/bin/bash", "test/run.sh"}, env: map[string]string{"PWD": "/w"}},
		{pid: 4, argv: []string{"/tmp/at/bin/wvr", "hook"}, env: pane},
		{pid: 5, argv: []string{"/usr/bin/git", "status"}, env: pane},
		// Codex's app server, started from pane 7: a gone thread's command
		// under it, through a shell whose environment macOS hides.
		{pid: 10, argv: []string{"/home/u/.codex/bin/codex", "app-server"}, env: pane},
		{pid: 11, argv: []string{"/bin/bash", "-lc", "pytest"}},
		{pid: 12, argv: []string{"/bin/bash", "-lc", "ls"}},
		{pid: 13, argv: []string{"/w/.venv/bin/python", "-m", "pytest"}, env: pane},
		{pid: 14, argv: []string{"/usr/bin/git", "status"}, env: pane},
	}
	kids := map[int][]int{2: {3}, 3: {4}, 4: {5}, 10: {11, 12}, 11: {13}, 13: {14}}
	gone := func(env map[string]string) bool { return env["TMUX_PANE"] == "%7" }
	var got []int
	for _, l := range condemned(tree[:5], kids, gone) {
		got = append(got, l.Procs...)
	}
	if len(got) != 0 {
		t.Errorf("the runner's tree: condemned %v, want none", got)
	}
	got = nil
	ls := condemned(tree[5:], kids, gone)
	for _, l := range ls {
		got = append(got, l.Procs...)
	}
	if !reflect.DeepEqual(got, []int{13, 14}) || ls[0].Pid != 13 || ls[0].Pane != "%7" {
		t.Errorf("under the daemon: condemned %v (%+v), want [13 14]", got, ls)
	}
	if ls := condemned(tree[5:], kids, func(map[string]string) bool { return false }); len(ls) != 0 {
		t.Errorf("its pane still there: %+v", ls)
	}
}
