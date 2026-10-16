package wvr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/session"
)

// A view is ready once it is signed in (gh installed, for GITHUB), asked at
// most once a minute; till then the strip shows it with a dot, and its
// setup screen offers the fix.
func TestViewNeeds(t *testing.T) {
	bin := t.TempDir()
	// No Keychain item: security finds none.
	for name, body := range map[string]string{"brew": "", "security": "exit 44"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	t.Setenv("LINEAR_API_KEY", "")
	e := &Env{&session.Env{StateDir: t.TempDir()}}
	if n := e.viewNeeds("linear"); n.state != viewSignedOut || strings.Join(n.fix, " ") != "wvr linear login" {
		t.Errorf("no key: %+v", n)
	}
	t.Setenv("LINEAR_API_KEY", "lin_api_x")
	if e.viewNeeds("linear").state == viewReady {
		t.Error("asked again within the minute")
	}
	old := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(e.StateDir+"/linear-auth", old, old); err != nil {
		t.Fatal(err)
	}
	if n := e.viewNeeds("linear"); n.state != viewReady {
		t.Errorf("a key: %+v", n)
	}
	if n := e.viewNeeds("github"); n.state != viewMissing || strings.Join(n.fix, " ") != "brew install gh" {
		t.Errorf("no gh: %+v", n)
	}
	t.Setenv("PATH", t.TempDir())
	if n := e.viewNeeds("github"); n.state != viewMissing || n.fix != nil {
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
