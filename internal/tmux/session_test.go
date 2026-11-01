package tmux

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/mux"
)

// server is a throwaway tmux server with session a in it: its arguments
// for tmux (-S, -f), and tm, which runs tmux on it.
func server(t *testing.T) ([]string, func(args ...string) string) {
	t.Helper()
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
	t.Cleanup(func() {
		_ = exec.Command(bin, append(append([]string{}, srv...), "kill-server")...).Run()
		os.RemoveAll(dir)
	})
	return srv, tm
}

// A pane broken out to the session's next free index stays in the
// session: from outside a client, a tmux command with no target acts on
// the newest session, another one.
func TestBreakOutNextFreeStaysInSession(t *testing.T) {
	srv, tm := server(t)
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

// A pane spawned with Keep stays when its program exits, even at once,
// saying what happened, how it exited and what to do; started again, it is
// still kept. One without Keep goes with its program.
func TestKeepStaysWhenTheProgramExits(t *testing.T) {
	srv, tm := server(t)
	s := Session{Name: "a", Sock: srv}
	exit3 := []string{"/bin/sh", "-c", "echo said last; exit 3"}
	keep := &mux.Exit{What: "weaver: it exited", Hint: "R starts it again"}
	dead := func(p mux.PaneID, status string) string {
		var out string
		for i := 0; i < 50; i++ {
			if out = tm("display-message", "-p", "-t", Target(p), "#{pane_dead} #{pane_dead_status}"); out == "1 "+status {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		return out
	}

	_, p, _, err := s.OpenWindow(mux.NewWindow{Spawn: mux.Spawn{Argv: exit3, Keep: keep}, Label: "kept", At: -1, Background: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := dead(p, "3"); got != "1 3" {
		t.Fatalf("kept pane: dead and status %q, want 1 3", got)
	}
	screen := tm("capture-pane", "-p", "-S", "-", "-t", Target(p))
	for _, want := range []string{"said last", "weaver: it exited with status 3 · R starts it again"} {
		if !strings.Contains(screen, want) {
			t.Errorf("kept pane does not show %q:\n%s", want, screen)
		}
	}
	if err := s.Respawn(p, mux.Spawn{Argv: []string{"/bin/sh", "-c", "exit 4"}, Keep: keep}); err != nil {
		t.Fatal(err)
	}
	if got := dead(p, "4"); got != "1 4" {
		t.Errorf("respawned pane: dead and status %q, want 1 4", got)
	}

	if _, _, _, err := s.OpenWindow(mux.NewWindow{Spawn: mux.Spawn{Argv: exit3}, Label: "gone", At: -1, Background: true}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if names := tm("list-windows", "-t", "=a", "-F", "#{window_name}"); strings.Contains(names, "gone") {
		t.Errorf("a pane without Keep stayed: windows %q", names)
	}
}
