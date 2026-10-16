package wvr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/session"
)

// A view is ready once its tool is installed and signed in, asked at most
// once a minute; till then the strip shows it with a dot, and its setup
// screen offers the fix.
func TestViewNeeds(t *testing.T) {
	bin := t.TempDir()
	out := filepath.Join(bin, "status")
	if err := os.WriteFile(filepath.Join(bin, "linear-tui"), []byte("#!/bin/sh\ncat '"+out+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "brew"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	e := &Env{&session.Env{StateDir: t.TempDir()}}
	if err := os.WriteFile(out, []byte("OAuth token  none\n\nNot authenticated. Run `linear-tui auth login` to sign in.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := e.viewNeeds("linear"); n.state != viewSignedOut || strings.Join(n.fix, " ") != "linear-tui auth login" {
		t.Errorf("signed out: %+v", n)
	}
	if err := os.WriteFile(out, []byte("OAuth token  acme (rob)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if e.viewNeeds("linear").state == viewReady {
		t.Error("asked again within the minute")
	}
	old := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(e.StateDir+"/linear-auth", old, old); err != nil {
		t.Fatal(err)
	}
	if n := e.viewNeeds("linear"); n.state != viewReady {
		t.Errorf("signed in: %+v", n)
	}
	if n := e.viewNeeds("github"); n.state != viewMissing || strings.Join(n.fix, " ") != "brew install gh" {
		t.Errorf("no gh: %+v", n)
	}
	t.Setenv("PATH", "/usr/bin:/bin")
	if n := e.viewNeeds("linear"); n.state != viewMissing || n.fix != nil {
		t.Errorf("not installed, no Homebrew: %+v", n)
	}
	// Hidden from its setup screen, a view leaves the strip; wvr view NAME
	// brings it back.
	t.Setenv("WEAVER_VIEWS", "")
	if err := os.Unsetenv("WEAVER_VIEWS"); err != nil {
		t.Fatal(err)
	}
	e.setHidden("linear", true)
	if ns := e.viewNames(); strings.Join(ns, " ") != "github" {
		t.Errorf("hidden: %v", ns)
	}
	e.setHidden("linear", false)
	if ns := e.viewNames(); strings.Join(ns, " ") != "github linear" {
		t.Errorf("shown again: %v", ns)
	}
}
