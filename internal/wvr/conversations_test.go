package wvr

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A transcript says where its conversation started (its first cwd), where
// it is now and what it is called (its last lines: your title over Claude
// Code's, over its first prompt), however long it is.
func TestReadConversation(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, lines ...string) string {
		t.Helper()
		f := filepath.Join(dir, name+".jsonl")
		if err := os.WriteFile(f, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return f
	}
	start := `{"type":"user","cwd":"/r","gitBranch":"main","message":{"role":"user","content":"<command-name>/model</command-name>"}}`
	meta := `{"type":"user","isMeta":true,"cwd":"/r","message":{"role":"user","content":"Caveat: messages below"}}`
	prompt := `{"type":"user","cwd":"/r","message":{"role":"user","content":[{"type":"text","text":"tidy the parser\nplease"}]}}`
	tool := `{"type":"user","cwd":"/r","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`
	pad := `{"type":"assistant","cwd":"/r","message":{"content":"` + strings.Repeat("x", transcriptHead) + `"}}`
	moved := `{"type":"assistant","cwd":"/r/wt","gitBranch":"fix","message":{"content":"ok"}}`
	ai := `{"type":"ai-title","aiTitle":"Parser tidy"}`
	custom := `{"type":"custom-title","customTitle":"parser"}`

	for i, c := range []struct {
		name  string
		lines []string
		want  conversation
	}{
		{"first prompt", []string{start, meta, tool, prompt},
			conversation{Start: "/r", Cwd: "/r", Branch: "main", Title: "tidy the parser please"}},
		{"Claude Code's title", []string{start, prompt, ai},
			conversation{Start: "/r", Cwd: "/r", Branch: "main", Title: "Parser tidy"}},
		{"yours over Claude Code's, read from the end of a long one", []string{start, prompt, custom, pad, pad, ai, moved},
			conversation{Start: "/r", Cwd: "/r/wt", Branch: "fix", Title: "parser"}},
	} {
		got, err := readConversation(write(strconv.Itoa(i), c.lines...))
		if err != nil {
			t.Fatal(err)
		}
		if got.Start != c.want.Start || got.Cwd != c.want.Cwd || got.Branch != c.want.Branch || got.Title != c.want.Title {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}
