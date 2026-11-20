package rex

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestMain runs the package's tests; run as fzf, the name
// TestKeysShiftEnter gives copies of this binary, it is the program that
// test needs by that name instead (fakeFzf). A process is named by its
// executable's file, not a link's, and macOS kills a copy of one of its
// own programs (/bin/dd, /bin/bash) as it starts.
func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == "fzf" {
		os.Exit(fakeFzf(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// fakeFzf is a program by fzf's name: with -c SCRIPT ARGS, a shell that
// runs SCRIPT, as fzf runs the form's editor (what reads keys is its
// child); else dd's bs=1 count=N, copying the N bytes typed at it to its
// output, as fzf would read them.
func fakeFzf(args []string) int {
	if len(args) > 0 && args[0] == "-c" {
		c := exec.Command("/bin/sh", args...)
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		if c.Run() != nil {
			return 1
		}
		return 0
	}
	n := 1
	for _, a := range args {
		if v, ok := strings.CutPrefix(a, "count="); ok {
			n, _ = strconv.Atoi(v)
		}
	}
	b := make([]byte, n)
	k, _ := io.ReadFull(os.Stdin, b)
	_, _ = os.Stdout.Write(b[:k])
	return 0
}
