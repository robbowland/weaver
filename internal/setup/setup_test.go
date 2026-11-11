package setup

import (
	"bytes"
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

// What weaver brings to tuicr goes where there is none, and where weaver's
// own is out of date; a file of yours (no mark: one you wrote, or took over
// by removing the line) is left as it is.
func TestBring(t *testing.T) {
	conf := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", conf)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
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

// The weaver skill goes where each agent you have reads skills (Claude
// Code's config directory, and for Codex ~/.agents/skills), and none where
// you have neither; one you made yours (no mark) stays.
func TestBringSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	skills := func() []string {
		var out []string
		for _, f := range BroughtFiles() {
			if strings.HasSuffix(f.Path, "/SKILL.md") {
				out = append(out, strings.TrimPrefix(f.Path, home))
			}
		}
		return out
	}
	if got := skills(); len(got) != 0 {
		t.Errorf("no agent here: skills %q", got)
	}
	for _, d := range []string{"/.claude", "/.codex"} {
		if err := os.MkdirAll(home+d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(skills(), " "); got != "/.claude/skills/weaver/SKILL.md /.agents/skills/weaver/SKILL.md" {
		t.Errorf("skills %q", got)
	}
	mine := home + "/.agents/skills/weaver/SKILL.md"
	if err := os.MkdirAll(filepath.Dir(mine), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mine, []byte("---\nname: weaver\n---\nmine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Bring(BroughtFiles()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(home + "/.claude/skills/weaver/SKILL.md")
	if err != nil || !bytes.HasPrefix(b, []byte("---\nname: weaver\n")) || !bytes.Contains(b, mark) {
		t.Errorf("Claude Code's skill: %q (%v)", b, err)
	}
	if b, _ := os.ReadFile(mine); string(b) != "---\nname: weaver\n---\nmine\n" {
		t.Errorf("your skill: %q, want it left alone", b)
	}
}
