// Package rextest is Rex for tests outside package rex: none at all, or a
// server of a test's own, never the user's (as rex_test.go's hermetic and
// server are inside it).
package rextest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/rex"
)

// NoRex clears what weaver, Rex and tmux read from the environment (all
// but where rex is, REX_BIN_DIR) and points rex at a server that is not
// there, in a runtime directory of the test's own: rex never falls back
// to the user's server, and no Rex answers. Its directory, under /tmp (a
// Unix socket's path has room for about 100 bytes: not $TMPDIR's).
func NoRex(t testing.TB) string {
	t.Helper()
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k == "TMUX" || k == "TMUX_PANE" || strings.HasPrefix(k, "WEAVER_") ||
			strings.HasPrefix(k, "REX_") && k != "REX_BIN_DIR" {
			t.Setenv(k, "")
			_ = os.Unsetenv(k)
		}
	}
	d, err := os.MkdirTemp("/tmp", "wvr-rex")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	t.Setenv("REX_RUNTIME_DIR", filepath.Join(d, "run"))
	t.Setenv("REX_SERVER", "unix://"+filepath.Join(d, "s.sock"))
	return d
}

// stopWait is how long a server has to stop once told.
var stopWait = 10 * time.Second

// Server starts a Rex server of the test's own (the test is skipped
// without rex), as NoRex points rex. It stops as the test ends, or when
// stop is called (no Rex answers from then on): SIGTERM, then waited for,
// so that Rex removes its runtime file; one left is an error, and so is a
// server still running 10 s on, which is then killed.
func Server(t testing.TB) (stop func()) {
	t.Helper()
	bin := rex.Bin()
	if _, err := exec.LookPath(bin); err != nil {
		t.Skip("rex is not installed")
	}
	d := NoRex(t)
	conf := filepath.Join(d, "init.lua")
	if err := os.WriteFile(conf, []byte("-- nothing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := os.Getenv("REX_RUNTIME_DIR")
	// Its blocks have the server's environment, REX_SERVER and all.
	srv := exec.Command(bin, "server", "run", "--listen", os.Getenv("REX_SERVER"), "--state-dir", filepath.Join(d, "state"), "--config", conf)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	stop = func() {
		once.Do(func() {
			done := make(chan error, 1)
			go func() { done <- srv.Wait() }()
			_ = srv.Process.Signal(syscall.SIGTERM)
			select {
			case <-done:
			case <-time.After(stopWait):
				t.Errorf("the Rex server did not stop in %s", stopWait)
				_ = srv.Process.Kill()
				<-done
			}
			if left, _ := filepath.Glob(filepath.Join(run, "servers", "*", "runtime.json")); len(left) > 0 {
				t.Errorf("the Rex server left %q", left)
			}
		})
	}
	t.Cleanup(stop)
	for i := 0; ; i++ {
		if exec.Command(bin, "--autostart=false", "api", "call", "server.status", "{}").Run() == nil {
			return stop
		}
		if i == 50 {
			t.Fatal("the Rex server did not start")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
