package wvr

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestMain runs the package's tests; run as nvim, the name
// TestUntilRewrittenWaitsForNvim gives a copy of this binary, it is an nvim
// that runs for its argument's time instead. A process is named by its
// executable's file, not a link's, and macOS kills a copy of one of its own
// programs (/bin/sleep) as it starts.
func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == "nvim" {
		d, err := time.ParseDuration(os.Args[1])
		if err != nil {
			panic(err)
		}
		time.Sleep(d)
		os.Exit(0)
	}
	os.Exit(m.Run())
}
