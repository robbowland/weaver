package wvr

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/tmux"
)

// An operation that looks up panes and windows fetches each once (on Rex a
// fetch is a snapshot of the session): a thread's width set back, a
// window fitted, an agent adopted in the window it has to itself.
func TestOneFetchPerOperation(t *testing.T) {
	tmuxBin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("no tmux")
	}
	dir := t.TempDir()
	sock := dir + "/tmux.sock"
	log := dir + "/tmux.log"
	// tmux as weaver runs it, each call logged.
	bin := dir + "/bin"
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	wrap := "#!/bin/sh\necho \"$*\" >> '" + log + "'\nexec '" + tmuxBin + "' \"$@\"\n"
	if err := os.WriteFile(bin+"/tmux", []byte(wrap), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	tm := func(args ...string) string {
		t.Helper()
		out, err := exec.Command(tmuxBin, append([]string{"-S", sock, "-f", "/dev/null"}, args...)...).Output()
		if err != nil {
			t.Fatalf("tmux %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	tm("new-session", "-d", "-s", "s", "-x", "200", "-y", "50", "/bin/sleep 600")
	t.Cleanup(func() { _ = exec.Command(tmuxBin, "-S", sock, "kill-server").Run() })
	pane := func(s string) mux.PaneID {
		t.Helper()
		p, ok := tmux.ParsePane(s)
		if !ok {
			t.Fatalf("pane %q", s)
		}
		return p
	}
	// A thread's window: its nvim and its agent.
	agent := pane(tm("new-window", "-d", "-P", "-F", "#{pane_id}", "/bin/sleep 600"))
	nvim := pane(tm("split-window", "-d", "-h", "-b", "-t", tmux.Target(agent), "-P", "-F", "#{pane_id}", "/bin/sleep 600"))
	win := tm("display-message", "-p", "-t", tmux.Target(agent), "#{window_id}")
	// An agent alone in a window of its own, to adopt.
	lone := pane(tm("new-window", "-d", "-P", "-F", "#{pane_id}", "/bin/sleep 600"))

	e := &Env{&session.Env{Session: "s", SKey: "tmux-s", StateDir: dir + "/state", SockDir: dir + "/sock",
		TSock: []string{"-S", sock}, Self: "/bin/sleep"}}
	if err := os.MkdirAll(e.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Both panes a thread's agent with its nvim bound (as two agents side by
	// side would be): Fit sets each one's width, from one look at the
	// windows.
	for _, p := range [][2]mux.PaneID{{agent, nvim}, {nvim, agent}} {
		if err := os.WriteFile(e.ThreadFile(p[0]), []byte(`{"num":1}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(e.PaneFile(p[0], "nvim"), []byte(string(p[1])+"\n\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fetches := func(what string, run func()) {
		t.Helper()
		os.Remove(log)
		run()
		b, _ := os.ReadFile(log)
		ls := strings.Split(string(b), "\n")
		count := func(cmd string) int {
			n := 0
			for _, l := range ls {
				if strings.Contains(" "+l+" ", " "+cmd+" ") {
					n++
				}
			}
			return n
		}
		if p, w := count("list-panes"), count("list-windows"); p != 1 || w != 1 {
			t.Errorf("%s: %d fetches of the panes and %d of the windows, want 1 and 1\n%s", what, p, w, b)
		}
	}
	fetches("threadWidth", func() { e.threadWidth(agent) })
	fetches("Fit", func() { e.Fit(win) })
	fetches("adoptWindow", func() {
		if e.adoptWindow(lone, 2, "02 demo", dir) == "" {
			t.Error("adoptWindow: no window")
		}
	})
	for i := 0; !strings.Contains(tm("list-windows", "-F", "#{window_name}"), "02 demo"); i++ {
		if i == 20 {
			t.Fatal("the adopted agent's window was not named")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(e.StateDir, "tmux-s_"+string(lone)+".nvim")); err != nil {
		t.Errorf("the adopted agent got no nvim: %v", err)
	}
}
