package rextest

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// recorder is a test whose errors are kept rather than failing it.
type recorder struct {
	testing.TB
	errs []string
}

func (r *recorder) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

// A server that does not stop once told is an error, and is killed: the
// run never hangs on it.
func TestServerStopBounded(t *testing.T) {
	dir := t.TempDir()
	// A rex whose server ignores SIGTERM, and whose calls all answer once
	// it does; it writes a runtime file as Rex's does.
	ready := dir + "/ready"
	fake := "#!/bin/sh\nif [ \"$1\" = server ]; then trap '' TERM; mkdir -p \"$REX_RUNTIME_DIR/servers/fake\"; " +
		": > \"$REX_RUNTIME_DIR/servers/fake/runtime.json\"; : > '" + ready + "'; exec /bin/sleep 600; fi\n" +
		"[ -e '" + ready + "' ]\n"
	if err := os.WriteFile(dir+"/rex", []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	was := stopWait
	stopWait = 200 * time.Millisecond
	t.Cleanup(func() { stopWait = was })
	r := &recorder{TB: t}
	stop := Server(r, dir+"/rex")
	done := make(chan struct{})
	go func() {
		stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stop hung on a server that ignores SIGTERM")
	}
	// Killed, it leaves its runtime file: said too.
	if len(r.errs) != 2 || !strings.Contains(r.errs[0], "did not stop") || !strings.Contains(r.errs[1], "left") {
		t.Errorf("errors: %q, want the server's not stopping, and the runtime file it left", r.errs)
	}
}
