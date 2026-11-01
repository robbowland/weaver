package tmux

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// A pane broken out to the session's next free index stays in the
// session: from outside a client, a tmux command with no target acts on
// the newest session, another one.
func TestBreakOutNextFreeStaysInSession(t *testing.T) {
	bin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("no tmux")
	}
	t.Setenv("TMUX", "")
	// A socket's path has room for about 100 bytes: not t.TempDir's.
	dir, err := os.MkdirTemp("", "tmux")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	srv := []string{"-S", dir + "/s", "-f", "/dev/null"}
	tm := func(args ...string) string {
		t.Helper()
		out, err := exec.Command(bin, append(append([]string{}, srv...), args...)...).Output()
		if err != nil {
			t.Fatalf("tmux %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	tm("new-session", "-d", "-s", "a", "-x", "200", "-y", "50", "/bin/sleep 600")
	t.Cleanup(func() { _ = exec.Command(bin, append(append([]string{}, srv...), "kill-server")...).Run() })
	p, _ := ParsePane(tm("split-window", "-d", "-P", "-F", "#{pane_id}", "-t", "=a:", "/bin/sleep 600"))
	tm("new-session", "-d", "-s", "b", "/bin/sleep 600")

	w, err := Session{Name: "a", Sock: srv}.BreakOut(p, "out", -1, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := tm("display-message", "-p", "-t", WindowTarget(w), "#{session_name}"); got != "a" {
		t.Errorf("broken out to session %q, want a", got)
	}
}
