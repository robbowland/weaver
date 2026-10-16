package wvr

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The contracts between weaver's halves, which no single test sees whole:
// the states an agent can be in and their cells, as the Go half
// (stateCell), Neovim (STATES in init.lua, GLYPH in status.lua) and
// docs/design.md have them. A copied list drifting from the first fails here.
func TestStateContract(t *testing.T) {
	read := func(p string) string {
		b, err := os.ReadFile("../../" + p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	key := regexp.MustCompile(`\[?'?([a-z-]+)'?\]?\s*=`)

	m := regexp.MustCompile(`(?m)^local STATES = \{ (.*) \}$`).FindStringSubmatch(read("nvim/lua/weaver/init.lua"))
	if m == nil {
		t.Fatal("no STATES in nvim/lua/weaver/init.lua")
	}
	var lua []string
	for _, k := range key.FindAllStringSubmatch(m[1], -1) {
		lua = append(lua, k[1])
	}
	sort.Strings(lua)

	row := regexp.MustCompile("(?m)^\\| `state` \\| string \\|(.*)$").FindStringSubmatch(read("docs/design.md"))
	if row == nil {
		t.Fatal("no state row in docs/design.md's state file table")
	}
	var doc []string
	for _, k := range regexp.MustCompile("`([a-z-]+)`").FindAllStringSubmatch(row[1], -1) {
		doc = append(doc, k[1])
	}
	sort.Strings(doc)
	if strings.Join(lua, " ") != strings.Join(doc, " ") {
		t.Errorf("Neovim's states %v are not the design doc's %v", lua, doc)
	}

	glyph := map[string]string{}
	status := read("nvim/lua/weaver/status.lua")
	block := regexp.MustCompile(`(?s)local GLYPH = \{\n(.*?)\n\}`).FindStringSubmatch(status)
	if block == nil {
		t.Fatal("no GLYPH in nvim/lua/weaver/status.lua")
	}
	for _, l := range strings.Split(block[1], "\n") {
		if g := regexp.MustCompile(`^\s*\[?'?([a-z-]+)'?\]?\s*=\s*\{\s*'(.)'`).FindStringSubmatch(l); g != nil {
			glyph[g[1]] = g[2]
		}
	}
	for _, s := range lua {
		if c := stateCell(s); c[1:2] != glyph[s] {
			t.Errorf("state %s: the Go half's cell %q, Neovim's mark %q", s, c, glyph[s])
		}
	}
	if stateCell("starting") != GWork {
		t.Errorf("a starting thread shows as %q, not working's %q", stateCell("starting"), GWork)
	}
}
