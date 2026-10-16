package wvr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robbowland/weaver/internal/setup"
)

// wvr sets itself up as it starts: only when it is the wvr on PATH, the
// hooks of an agent you have, and nothing the second time.
func TestSelfSetup(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	self := filepath.Join(bin, "wvr")
	if err := os.WriteFile(self, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("WEAVER_SELF_SETUP", "")
	// The plugins are there already: nothing to fetch.
	if err := os.MkdirAll(setup.PluginsDir()+"/diffview-plus.nvim", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(setup.PluginsDir()+"/diffview-plus.nvim/.weaver-rev", []byte(setup.DiffviewRev+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(home, ".claude", "settings.json")

	t.Setenv("PATH", "/usr/bin:/bin")
	(&Env{Self: self}).SelfSetup()
	if exists(settings) || exists(filepath.Join(home, ".claude")) {
		t.Fatal("not the wvr on PATH, yet set up")
	}
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	(&Env{Self: self}).SelfSetup()
	if exists(filepath.Join(home, ".claude")) {
		t.Fatal("no Claude Code here, yet its settings were made")
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	(&Env{Self: self}).SelfSetup()
	b, err := os.ReadFile(settings)
	if err != nil || !strings.Contains(string(b), `"`+self+`"`) {
		t.Fatalf("hooks not set up: %s", b)
	}
	(&Env{Self: self}).SelfSetup()
	if b2, _ := os.ReadFile(settings); string(b2) != string(b) {
		t.Error("changed again")
	}
	if bk, _ := filepath.Glob(settings + ".bak.*"); len(bk) != 0 {
		t.Errorf("backups of a file that was not there: %v", bk)
	}
	if exists(filepath.Join(home, ".codex")) {
		t.Error("Codex's hooks made without ~/.codex")
	}
}
