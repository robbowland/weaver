package setup

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// mergeHooks takes out every weaver hook, whatever its form,
// puts the current ones in run by bin, touches nobody else's, keeps the
// settings' order, and changes nothing the second time.
func TestMergeHooks(t *testing.T) {
	settings := `{
  "model": "opus",
  "hooks": {
    "Stop": [{ "hooks": [{ "type": "command", "command": "wvr hook" }, { "type": "command", "command": "say done" }] }],
    "PostToolUse": [
      { "matcher": "Edit", "hooks": [{ "type": "command", "command": "/old/bin/wvr", "args": ["hook"] }] },
      { "matcher": "Bash", "hooks": [{ "type": "command", "command": "my-linter" }] }
    ],
    "SessionStart": [{ "hooks": [{ "type": "command", "command": "/old/bin/wvr hook" }] }]
  },
  "statusLine": { "type": "command", "command": "ünïcode ✓" }
}`
	add := `{"hooks": {
    "Stop": [{ "hooks": [{ "type": "command", "command": "wvr", "args": ["hook"] }] }],
    "Notification": [{ "hooks": [{ "type": "command", "command": "wvr hook --async" }] }]
  }}`
	out, err := mergeHooks([]byte(settings), []byte(add), "/new/bin/wvr")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, gone := range []string{`"wvr hook"`, `/old/bin/wvr`, `"SessionStart"`} {
		if strings.Contains(s, gone) {
			t.Errorf("%s left in:\n%s", gone, s)
		}
	}
	for _, kept := range []string{`"say done"`, `"my-linter"`, `"ünïcode ✓"`, `"/new/bin/wvr hook --async"`} {
		if !strings.Contains(s, kept) {
			t.Errorf("%s missing from:\n%s", kept, s)
		}
	}
	var keys []string
	d := json.NewDecoder(strings.NewReader(s))
	if _, err := d.Token(); err != nil {
		t.Fatal(err)
	}
	for d.More() {
		k, _ := d.Token()
		keys = append(keys, k.(string))
		var v json.RawMessage
		if err := d.Decode(&v); err != nil {
			t.Fatal(err)
		}
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
	if strings.Join(evs, " ") != "Stop PostToolUse Notification" {
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

// What weaver brings to tuicr goes where there is none, and where weaver's
// own is out of date; a file of yours (no mark: one you wrote, or took over
// by removing the line) is left as it is.
func TestBring(t *testing.T) {
	conf := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", conf)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	bs := BroughtFiles()
	if len(bs) != 3 {
		t.Fatalf("%d files brought, want tuicr's settings, theme and syntax theme", len(bs))
	}
	theme, settings := conf+"/tuicr/themes/micrographics.toml", conf+"/tuicr/config.toml"
	if err := os.MkdirAll(conf+"/tuicr/themes", 0o755); err != nil {
		t.Fatal(err)
	}
	mine := "theme = \"mine\"\n"
	if err := os.WriteFile(settings, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	old := "# weaver: this file is weaver's (an older one)\npanel_bg = \"#111111\"\n"
	if err := os.WriteFile(theme, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	wrote, err := Bring(bs)
	if err != nil || len(wrote) != 2 {
		t.Fatalf("Bring wrote %d (%v), want the theme and syntax theme", len(wrote), err)
	}
	if b, _ := os.ReadFile(settings); string(b) != mine {
		t.Errorf("your settings: %q, want them left alone", b)
	}
	for _, f := range bs {
		b, err := os.ReadFile(f.Path)
		if f.Path != settings && (err != nil || !bytes.Equal(b, f.Text)) {
			t.Errorf("%s: not weaver's text (%v)", f.Path, err)
		}
	}
	if wrote, _ := Bring(BroughtFiles()); len(wrote) != 0 {
		t.Errorf("again: wrote %d, want none", len(wrote))
	}
	if !bytes.Contains(bs[1].Text, []byte(`syntax_theme = "Micrographics.tmTheme"`)) {
		t.Error("the theme does not take its syntax theme from beside it")
	}
}

// The weaver skill goes where Claude Code reads skills, when you have it
// (its config directory is there); one you made yours (no mark) stays.
func TestBringSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	skill := home + "/.claude/skills/weaver/SKILL.md"
	brought := func() bool {
		for _, f := range BroughtFiles() {
			if f.Path == skill {
				return true
			}
		}
		return false
	}
	if brought() {
		t.Error("no Claude Code here, yet its skill is brought")
	}
	if err := os.MkdirAll(home+"/.claude", 0o755); err != nil {
		t.Fatal(err)
	}
	if !brought() {
		t.Fatal("Claude Code here, and no skill brought")
	}
	if _, err := Bring(BroughtFiles()); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(skill); err != nil || !bytes.HasPrefix(b, []byte("---\nname: weaver\n")) || !bytes.Contains(b, mark) {
		t.Errorf("the skill: %q (%v)", b, err)
	}
	mine := "---\nname: weaver\n---\nmine\n"
	if err := os.WriteFile(skill, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Bring(BroughtFiles()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(skill); string(b) != mine {
		t.Errorf("your skill: %q, want it left alone", b)
	}
}
