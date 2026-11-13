package session

import (
	"net"
	"os"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/jsonx"
	"github.com/robbowland/weaver/internal/store"
)

// A socket nothing listens on goes once it is old; a live one, or a new
// one, stays.
func TestSweepSockets(t *testing.T) {
	dir := t.TempDir()
	e := &Env{SockDir: dir}
	listen := func(name string) *net.UnixListener {
		t.Helper()
		l, err := net.ListenUnix("unix", &net.UnixAddr{Name: dir + "/" + name, Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		l.SetUnlinkOnClose(false)
		return l
	}
	old := time.Now().Add(-48 * time.Hour)
	dead := listen("rex-s_1.sock")
	_ = dead.Close()
	_ = os.Chtimes(dir+"/rex-s_1.sock", old, old)
	live := listen("rex-s_2.sock")
	defer func() { _ = live.Close() }()
	_ = os.Chtimes(dir+"/rex-s_2.sock", old, old)
	young := listen("rex-s_3.sock")
	_ = young.Close()

	e.sweepSockets(24 * time.Hour)
	for name, want := range map[string]bool{"rex-s_1.sock": false, "rex-s_2.sock": true, "rex-s_3.sock": true} {
		if _, err := os.Lstat(dir + "/" + name); (err == nil) != want {
			t.Errorf("%s there: %v, want %v", name, err == nil, want)
		}
	}
}

// A thread lost with its session is pruned a month on; one you put on
// hold waits until you resume it, however long.
func TestGCKeepsHeldThreads(t *testing.T) {
	dir := t.TempDir()
	e := &Env{StateDir: dir + "/state", LostDir: dir + "/lost", SockDir: dir + "/sock"}
	for _, d := range []string{e.StateDir, e.LostDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-40 * 24 * time.Hour)
	for name, rec := range map[string]store.Lost{
		"rex-s_lost.json": {Lost: jsonx.Int(old.Unix()), SKey: "rex-s"},
		"rex-s_held.json": {Lost: jsonx.Int(old.Unix()), SKey: "rex-s", Held: jsonx.Int(old.Unix()), Note: "next: the docs"},
		"rex-s_new.json":  {Lost: jsonx.Int(time.Now().Unix()), SKey: "rex-s"},
	} {
		f := e.LostDir + "/" + name
		if err := store.Write(f, rec); err != nil {
			t.Fatal(err)
		}
		if name != "rex-s_new.json" {
			_ = os.Chtimes(f, old, old)
		}
	}
	e.gcFiles()
	for name, want := range map[string]bool{"rex-s_lost.json": false, "rex-s_held.json": true, "rex-s_new.json": true} {
		if _, err := os.Stat(e.LostDir + "/" + name); (err == nil) != want {
			t.Errorf("%s kept: %v, want %v", name, err == nil, want)
		}
	}
}
