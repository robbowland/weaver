package wv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mergeHooks takes out every weaver hook (wvr, weaver or agentnav), whatever its form,
// puts the current ones in run by bin, touches nobody else's, keeps the
// settings' order, and changes nothing the second time.
func TestMergeHooks(t *testing.T) {
	settings := `{
  "model": "opus",
  "hooks": {
    "Stop": [{ "hooks": [{ "type": "command", "command": "weaver hook" }, { "type": "command", "command": "say done" }] }],
    "PostToolUse": [
      { "matcher": "Edit", "hooks": [{ "type": "command", "command": "/old/bin/weaver", "args": ["hook"] }] },
      { "matcher": "Bash", "hooks": [{ "type": "command", "command": "my-linter" }] }
    ],
    "SessionStart": [{ "hooks": [{ "type": "command", "command": "agentnav hook" }] }]
  },
  "statusLine": { "type": "command", "command": "ünïcode ✓" }
}`
	add := `{"hooks": {
    "Stop": [{ "hooks": [{ "type": "command", "command": "wvr", "args": ["hook"] }] }],
    "Interrupt": [{ "hooks": [{ "type": "command", "command": "wvr adapt codex --bell" }] }]
  }}`
	out, err := mergeHooks([]byte(settings), []byte(add), "/new/bin/wvr")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, gone := range []string{`"weaver hook"`, `/old/bin/weaver`, `agentnav`, `"SessionStart"`} {
		if strings.Contains(s, gone) {
			t.Errorf("%s left in:\n%s", gone, s)
		}
	}
	for _, kept := range []string{`"say done"`, `"my-linter"`, `"ünïcode ✓"`, `"/new/bin/wvr adapt codex --bell"`} {
		if !strings.Contains(s, kept) {
			t.Errorf("%s missing from:\n%s", kept, s)
		}
	}
	var keys []string
	d := json.NewDecoder(strings.NewReader(s))
	d.Token()
	for d.More() {
		k, _ := d.Token()
		keys = append(keys, k.(string))
		var v json.RawMessage
		d.Decode(&v)
	}
	if strings.Join(keys, " ") != "model hooks statusLine" {
		t.Errorf("order: %v", keys)
	}
	o, _ := parseOobj(out)
	hr, _ := o.get("hooks")
	h, _ := parseOobj(hr)
	var evs []string
	for _, m := range h {
		evs = append(evs, m.k)
	}
	if strings.Join(evs, " ") != "Stop PostToolUse Interrupt" {
		t.Errorf("events (kept in place, new ones last): %v", evs)
	}
	again, err := mergeHooks(out, []byte(add), "/new/bin/wvr")
	if err != nil || string(again) != s {
		t.Errorf("not idempotent:\n%s", again)
	}
	if _, err := mergeHooks([]byte(`[1]`), []byte(add), "x"); err == nil {
		t.Error("a settings file that is not an object: no error")
	}
}

// wvr sets itself up as it starts: only when it is the wvr on PATH, the
// hooks of an agent you have, and nothing the second time.
func TestSelfSetup(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	os.MkdirAll(bin, 0o755)
	self := filepath.Join(bin, "wvr")
	os.WriteFile(self, []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("WEAVER_SELF_SETUP", "")
	// The plugins are there already: nothing to fetch.
	os.MkdirAll(PluginsDir()+"/diffview-plus.nvim", 0o755)
	os.WriteFile(PluginsDir()+"/diffview-plus.nvim/.weaver-rev", []byte(diffviewRev+"\n"), 0o644)
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
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
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
