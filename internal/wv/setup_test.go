package wv

import (
	"encoding/json"
	"strings"
	"testing"
)

// mergeHooks takes out every weaver (and agentnav) hook, whatever its form,
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
    "Stop": [{ "hooks": [{ "type": "command", "command": "weaver", "args": ["hook"] }] }],
    "Interrupt": [{ "hooks": [{ "type": "command", "command": "weaver adapt codex --bell" }] }]
  }}`
	out, err := mergeHooks([]byte(settings), []byte(add), "/new/bin/weaver")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, gone := range []string{`"weaver hook"`, `/old/bin/weaver`, `agentnav`, `"SessionStart"`} {
		if strings.Contains(s, gone) {
			t.Errorf("%s left in:\n%s", gone, s)
		}
	}
	for _, kept := range []string{`"say done"`, `"my-linter"`, `"ünïcode ✓"`, `"/new/bin/weaver adapt codex --bell"`} {
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
	again, err := mergeHooks(out, []byte(add), "/new/bin/weaver")
	if err != nil || string(again) != s {
		t.Errorf("not idempotent:\n%s", again)
	}
	if _, err := mergeHooks([]byte(`[1]`), []byte(add), "x"); err == nil {
		t.Error("a settings file that is not an object: no error")
	}
}
