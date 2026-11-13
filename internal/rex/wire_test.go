package rex

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/rex/rextest"
)

// weaver speaks to the server over its control connection: a call answers
// as the rex command's would, a Rex error with its code; the connection
// outlives its server's restart (one kept from before is found closed and
// opened again), opens again once closed, and leaves no block it made
// owned by it (a terminal takes its size from its owner, which asks none).
func TestWire(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "wvr-w")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	stop := rextest.ServerAt(t, Bin(), dir)
	if !wireReady() {
		t.Fatal("no control connection to a server on a Unix socket")
	}
	pid := serverPID()
	var st struct {
		Pid int `json:"pid"`
	}
	if err := wireCall("server.status", map[string]any{}, &st); err != nil || st.Pid != pid {
		t.Fatalf("server.status: %+v %v, want pid %d", st, err, pid)
	}
	err = wireCall("session.view", map[string]any{"session_id": "session:01a1d270-d4c2-7743-ab7a-377e27f88198"}, nil)
	if err == nil || !strings.Contains(err.Error(), "code=session_not_found") || !strings.HasPrefix(err.Error(), "session.attach: rex: ") {
		t.Errorf("a session that is not: %v, want Rex's error with its code", err)
	}
	s, err := Start(dir, "demo", "/", []string{"/bin/sleep", "600"})
	if err != nil {
		t.Fatal(err)
	}
	_, p, _, err := s.OpenWindow(mux.NewWindow{Spawn: mux.Spawn{Argv: []string{"/bin/sleep", "600"}}, Label: "w", At: 1, Background: true})
	if err != nil {
		t.Fatal(err)
	}
	var sz struct {
		Owner string `json:"owner"`
	}
	if err := s.blockMethod(p, "size", nil, &sz); err != nil || sz.Owner != "" {
		t.Errorf("a block weaver made: owner %q (%v), want none", sz.Owner, err)
	}

	stop()
	if wireCall("server.status", map[string]any{}, nil) == nil {
		t.Fatal("a call with the server gone")
	}
	rextest.ServerAt(t, Bin(), dir)
	if err := wireCall("server.status", map[string]any{}, &st); err != nil || st.Pid == pid || st.Pid == 0 {
		t.Errorf("after the server started again: %+v %v", st, err)
	}
	Close()
	if err := wireCall("server.status", map[string]any{}, nil); err != nil {
		t.Errorf("after Close: %v", err)
	}
	// Idle, it is closed (the server forgets its client at once), and the
	// next call opens another.
	defer func(d time.Duration) { wireIdle = d }(wireIdle)
	wireIdle = 100 * time.Millisecond
	if err := wireCall("server.status", map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	wireMu.Lock()
	idle := wireNow
	wireMu.Unlock()
	if idle != nil {
		t.Error("an idle connection kept open")
	}
	if err := wireCall("server.status", map[string]any{}, nil); err != nil {
		t.Errorf("after an idle close: %v", err)
	}
	t.Setenv("WEAVER_REX_WIRE", "0")
	if wireReady() {
		t.Error("WEAVER_REX_WIRE=0: a control connection all the same")
	}
}
