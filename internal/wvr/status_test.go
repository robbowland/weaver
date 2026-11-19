package wvr

import (
	"encoding/json"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/jsonx"
	"github.com/robbowland/weaver/internal/rex"
	"github.com/robbowland/weaver/internal/rex/rextest"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
)

// outside is wvr in a terminal outside weaver's session (a shell in no Rex
// session weaver made), while weaver's, demo ($WEAVER_SESSION), runs on a
// throwaway Rex server: the Env, and demo's home.
func outside(t *testing.T) (*Env, rex.Session) {
	t.Helper()
	rextest.Server(t, rex.Bin())
	dir := t.TempDir()
	se := &session.Env{StateDir: dir + "/state", LostDir: dir + "/lost", SockDir: dir + "/sock",
		ClaudeDir: dir + "/claude", Self: "/usr/bin/true"}
	if err := os.MkdirAll(se.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	home, err := rex.Start(se.RexDir(), "demo", "/", []string{"/bin/sleep", "600"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = home.Quit() })
	t.Setenv("WEAVER_SESSION", "demo")
	return &Env{se}, home
}

// stdout is what f prints.
func stdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	was := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = was }()
	got := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		got <- string(b)
	}()
	f()
	_ = w.Close()
	return <-got
}

// wvr status from a terminal outside weaver's session prints weaver's
// counts (for a menu bar or a prompt); with weaver's not running, it says
// so.
func TestStatusOutsideTheSession(t *testing.T) {
	e, home := outside(t)
	ps, err := home.Panes()
	if err != nil || len(ps) == 0 {
		t.Fatalf("home's panes: %v %v", ps, err)
	}
	// An agent that needs you, in home's pane.
	n, _ := strconv.Atoi(string(ps[0].ID))
	if err := store.Write(e.StateDir+"/"+rex.Key("demo")+"_"+string(ps[0].ID)+".json", agent.Record{Pane: jsonx.Int(n), Agent: agent.Claude,
		State: agent.StateNeedsYou, TS: time.Now().Unix(), Pid: json.Number(strconv.Itoa(os.Getpid())),
		Where: agent.Where{Server: home.Life().Server, Session: home.ID}}); err != nil {
		t.Fatal(err)
	}
	var serr error
	out := stdout(t, func() { serr = e.Status(nil) })
	if want := agent.CellNeedsYou + " 1\n"; serr != nil || out != want {
		t.Errorf("wvr status outside: %q (%v), want %q, weaver's counts", out, serr, want)
	}
	if err := home.Quit(); err != nil {
		t.Fatal(err)
	}
	e = &Env{&session.Env{StateDir: e.StateDir, SockDir: e.SockDir, ClaudeDir: e.ClaudeDir, Self: e.Self}}
	if err := e.Status(nil); err == nil || !strings.Contains(err.Error(), "no Rex session demo of weaver's is running") {
		t.Errorf("wvr status with weaver's not running: %v, want it said", err)
	}
}
