package session

import (
	"net"
	"os"
	"testing"
	"time"
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
	dead := listen("tmux-s_1.sock")
	_ = dead.Close()
	_ = os.Chtimes(dir+"/tmux-s_1.sock", old, old)
	live := listen("tmux-s_2.sock")
	defer func() { _ = live.Close() }()
	_ = os.Chtimes(dir+"/tmux-s_2.sock", old, old)
	young := listen("tmux-s_3.sock")
	_ = young.Close()

	e.sweepSockets(24 * time.Hour)
	for name, want := range map[string]bool{"tmux-s_1.sock": false, "tmux-s_2.sock": true, "tmux-s_3.sock": true} {
		if _, err := os.Lstat(dir + "/" + name); (err == nil) != want {
			t.Errorf("%s there: %v, want %v", name, err == nil, want)
		}
	}
}
