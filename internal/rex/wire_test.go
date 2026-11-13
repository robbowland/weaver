package rex

import (
	"encoding/json"
	"os"
	"strconv"
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

// fakeApp is a client of kind app on the server at socket path: it
// attaches to session sid, and answers each action asked of it (what it
// was asked goes to asked), refusing them as the Rex app does without
// Remote Control when refuse.
func fakeApp(t *testing.T, path, sid string, refuse bool, asked chan<- map[string]any) {
	t.Helper()
	w, err := wireDialAs(path, "app")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.close)
	if err := w.send(map[string]any{"type": "request", "id": "a", "method": "session.attach", "payload": map[string]any{"session_id": sid}}); err != nil {
		t.Fatal(err)
	}
	go func() {
		for i := 0; ; i++ {
			b, err := w.message()
			if err != nil {
				return
			}
			var m struct {
				Type    string `json:"type"`
				Payload struct {
					Type    string         `json:"type"`
					Payload map[string]any `json:"payload"`
				} `json:"payload"`
			}
			if json.Unmarshal(b, &m) != nil || m.Type != "event" || m.Payload.Type != "client_perform_requested" {
				continue
			}
			p := m.Payload.Payload
			asked <- p
			reply := map[string]any{"call_id": p["call_id"], "result": nil}
			if refuse {
				reply = map[string]any{"call_id": p["call_id"], "error": map[string]any{"code": "refused",
					"message": "Rex.app is not accepting actions. Turn on Remote Control in Rex's settings."}}
			}
			_ = w.send(map[string]any{"type": "request", "id": "r" + strconv.Itoa(i), "method": "client.reply", "payload": reply})
		}
	}()
	// Attached, before the test asks it anything.
	time.Sleep(100 * time.Millisecond)
}

// A window is shown in the app over the control connection as rex do asks
// it (client.perform of session.select with the session and window): the
// one app connected; with two, each attached to the session; refused for
// want of Remote Control, said so.
func TestShowInAppOverWire(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "wvr-a")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	rextest.ServerAt(t, Bin(), dir)
	s, err := Start(dir, "demo", "/", []string{"/bin/sleep", "600"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.showInApp("window:w"); err == nil || remoteOff(err) {
		t.Errorf("no app: %v, want an error, not Remote Control's", err)
	}
	path := wireEndpoint()
	asked := make(chan map[string]any, 4)
	fakeApp(t, path, s.ID, false, asked)
	if err := s.showInApp("window:w"); err != nil {
		t.Fatalf("one app: %v", err)
	}
	p := <-asked
	a, _ := p["args"].(map[string]any)
	if p["action"] != "session.select" || a["session_id"] != s.ID || a["window_id"] != "window:w" || p["origin"] != "cli" {
		t.Errorf("the app was asked %+v, want session.select of the session and window, as rex do asks", p)
	}
	// Two apps, one refusing: shown by the other.
	fakeApp(t, path, s.ID, true, asked)
	if err := s.showInApp(""); err != nil {
		t.Errorf("two apps, one showing it: %v", err)
	}
	for range 2 {
		<-asked
	}
	// Both refusing: why.
	Close()
	rextest.ServerAt(t, Bin(), "")
	s2, err := Start(t.TempDir(), "demo", "/", []string{"/bin/sleep", "600"})
	if err != nil {
		t.Fatal(err)
	}
	fakeApp(t, wireEndpoint(), s2.ID, true, asked)
	if err := s2.showInApp(""); !remoteOff(err) {
		t.Errorf("the app refusing: %v, want Remote Control's refusal", err)
	}
}

// A message said a moment after another (alt+1 pressed twice) replaces it
// without waiting on Rex: Rex closes a terminal still starting its program
// only after a second.
func TestSayQuicklyAgain(t *testing.T) {
	server(t)
	s, err := Start(t.TempDir(), "demo", "/", []string{"/bin/sleep", "600"})
	if err != nil {
		t.Fatal(err)
	}
	for _, gap := range []time.Duration{0, 10, 20, 30, 40} {
		s.Say(2000, "the first")
		time.Sleep(gap * time.Millisecond)
		start := time.Now()
		s.Say(2000, "the second")
		if took := time.Since(start); took > 600*time.Millisecond {
			t.Errorf("the second message, %d ms after the first: took %v", gap, took.Round(10*time.Millisecond))
		}
	}
}
