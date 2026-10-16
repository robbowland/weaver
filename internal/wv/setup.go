package wv

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	weaver "github.com/robbowland/weaver"
)

// reOurs is a hook command of weaver's (wvr, or weaver and agentnav, its
// old names): its name alone, a path to it, either with arguments.
var reOurs = regexp.MustCompile(`^(\S*/)?(wvr|weaver|agentnav)( |$)`)

// Setup is `wvr setup [--dry-run] [--no-codex] [--bin PATH]`: weaver's
// hooks into Claude Code's settings (and Codex's hooks, when ~/.codex
// exists), run by this binary's path (or PATH), each file backed up before
// it changes; its files unpacked.
func Setup(args []string, self string, out io.Writer) int {
	dry, codex, bin := false, true, self
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dry-run":
			dry = true
		case "--no-codex":
			codex = false
		case "--bin":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "weaver: --bin needs a path")
				return 2
			}
			i++
			bin = args[i]
		default:
			fmt.Fprintln(os.Stderr, "weaver: usage: wvr setup [--dry-run] [--no-codex] [--bin PATH]")
			return 2
		}
	}
	if !dry {
		if _, err := Runtime(); err != nil {
			fmt.Fprintln(os.Stderr, "weaver: "+err.Error())
			return 1
		}
	}
	home := os.Getenv("HOME")
	warnZellij(home)
	claude := os.Getenv("CLAUDE_CONFIG_DIR")
	if claude == "" {
		claude = home + "/.claude"
	}
	ts := time.Now().Format("20060102-150405")
	rc := 0
	if !configureHooks(out, "claude", claude+"/settings.json", "config/claude-hooks.json", bin, dry, ts) {
		rc = 1
	}
	codexDone := false
	cdir := os.Getenv("CODEX_HOME")
	if cdir == "" {
		cdir = home + "/.codex"
	}
	if st, err := os.Stat(cdir); codex && err == nil && st.IsDir() {
		if !configureHooks(out, "codex", cdir+"/hooks.json", "config/codex-hooks.json", bin, dry, ts) {
			rc = 1
		}
		codexDone = true
	}
	if dry || rc != 0 {
		return rc
	}
	fmt.Fprint(out, `
Done. To pick it up:
  - a running weaver tmux session reads weaver's tmux config again the next time
    `+"`wvr`"+` attaches to it
  - restart any Claude Code sessions that are already running: they read hooks at start
`)
	if codexDone {
		fmt.Fprint(out, `  - Codex: trust weaver's hooks in /hooks (Codex asks before running new hooks), then
    restart running Codex sessions
`)
	}
	return 0
}

// warnZellij: weaver ran on zellij too, once; a zellij config that still has
// its keybindings runs weaver commands it no longer has.
func warnZellij(home string) {
	f := os.Getenv("ZELLIJ_CONFIG_FILE")
	if f == "" {
		c := os.Getenv("XDG_CONFIG_HOME")
		if c == "" {
			c = home + "/.config"
		}
		f = c + "/zellij/config.kdl"
	}
	if b, err := os.ReadFile(f); err == nil && regexp.MustCompile(`Run "(weaver|agentnav)"`).Match(b) {
		fmt.Fprintf(os.Stderr, "warning: zellij: %s still has weaver's keybindings; weaver runs on tmux only now, so remove its '// weaver:' block\n", f)
	}
}

// configureHooks puts weaver's hooks (the embedded file hooks) into the
// agent's JSON hooks file f, showing what changes: false when it could not.
func configureHooks(out io.Writer, label, f, hooks, bin string, dry bool, ts string) bool {
	add, err := weaver.Files.ReadFile(hooks)
	if err != nil {
		fmt.Fprintln(os.Stderr, "weaver: "+err.Error())
		return false
	}
	old, err := os.ReadFile(f)
	had := err == nil
	if !had {
		old = []byte("{}")
	}
	nw, err := mergeHooks(old, add, bin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "weaver: cannot parse %s: %v\n", f, err)
		return false
	}
	var a, b bytes.Buffer
	if had && json.Compact(&a, old) == nil && json.Compact(&b, nw) == nil && a.String() == b.String() {
		fmt.Fprintf(out, "%s: %s already runs weaver on every event\n", label, f)
		return true
	}
	fmt.Fprintf(out, "--- %s: %s\n", label, f)
	if tmp, err := os.CreateTemp("", "weaver-setup."); err == nil {
		tmp.Write(nw)
		tmp.Close()
		from := f
		if !had {
			from = "/dev/null"
		}
		d := exec.Command("diff", "-u", from, tmp.Name())
		d.Stdout = out
		d.Run()
		os.Remove(tmp.Name())
	}
	if dry {
		return true
	}
	if had {
		if err := copyFile(f, f+".bak."+ts); err != nil {
			fmt.Fprintf(os.Stderr, "weaver: cannot back up %s; not changing it\n", f)
			return false
		}
		fmt.Fprintf(out, "backed up to %s\n", f+".bak."+ts)
	} else if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "weaver: cannot create %s\n", filepath.Dir(f))
		return false
	}
	// Through the file, so a linked settings file stays a link.
	if err := os.WriteFile(f, nw, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "weaver: cannot write %s (backup: %s.bak.%s)\n", f, f, ts)
		return false
	}
	return true
}

func copyFile(from, to string) error {
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	st, err := os.Stat(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, b, st.Mode().Perm())
}

// mergeHooks is settings with every weaver hook (whatever its form: "weaver
// hook", a path to weaver with args, or agentnav, its old name) removed and
// add's hooks put in, their "wvr" run as bin: so a changed matcher or
// event never leaves a stale entry behind. Hooks of anything else are never
// touched; a matcher group left empty, or an event left with none, goes.
// Members keep their order; new events go last. Two-space indented, as jq
// writes it.
func mergeHooks(settings, add []byte, bin string) ([]byte, error) {
	top, ok := parseOobj(settings)
	if !ok {
		return nil, fmt.Errorf("not a JSON object")
	}
	var hooks oobj
	if r, ok := top.get("hooks"); ok {
		if hooks, ok = parseOobj(r); !ok {
			return nil, fmt.Errorf(".hooks is not an object")
		}
	}
	var kept oobj
	for _, ev := range hooks {
		groups, ok := rawArray(ev.v)
		if !ok {
			return nil, fmt.Errorf(".hooks.%s is not an array", ev.k)
		}
		var gs []json.RawMessage
		for _, g := range groups {
			grp, ok := parseOobj(g)
			hr, has := grp.get("hooks")
			if !ok || !has {
				gs = append(gs, g)
				continue
			}
			hs, ok := rawArray(hr)
			if !ok {
				return nil, fmt.Errorf(".hooks.%s has hooks that are not an array", ev.k)
			}
			var keep []json.RawMessage
			for _, h := range hs {
				var c struct {
					Command any `json:"command"`
				}
				json.Unmarshal(h, &c)
				if s, ok := c.Command.(string); ok && reOurs.MatchString(s) {
					continue
				}
				keep = append(keep, h)
			}
			if len(keep) == 0 {
				continue
			}
			gs = append(gs, json.RawMessage(grp.set("hooks", joinRaw(keep)).String()))
		}
		if len(gs) > 0 {
			kept = append(kept, kv{ev.k, json.RawMessage(joinRaw(gs))})
		}
	}
	at, _ := parseOobj(add)
	ar, _ := at.get("hooks")
	adds, _ := parseOobj(ar)
	for _, ev := range adds {
		groups, _ := rawArray(ev.v)
		var gs []json.RawMessage
		if r, ok := kept.get(ev.k); ok {
			gs, _ = rawArray(r)
		}
		for _, g := range groups {
			grp, _ := parseOobj(g)
			hr, _ := grp.get("hooks")
			hs, _ := rawArray(hr)
			for i, h := range hs {
				ho, _ := parseOobj(h)
				if r, ok := ho.get("command"); ok {
					var c string
					if json.Unmarshal(r, &c) == nil {
						switch {
						case c == "wvr":
							c = bin
						case strings.HasPrefix(c, "wvr "):
							c = bin + c[3:]
						}
						hs[i] = json.RawMessage(ho.set("command", jqStr(c)).String())
					}
				}
			}
			gs = append(gs, json.RawMessage(grp.set("hooks", joinRaw(hs)).String()))
		}
		kept = kept.set(ev.k, joinRaw(gs))
	}
	if kept == nil {
		kept = oobj{}
	}
	top = top.set("hooks", kept.String())
	var b bytes.Buffer
	if err := json.Indent(&b, []byte(top.String()), "", "  "); err != nil {
		return nil, err
	}
	b.WriteByte('\n')
	return b.Bytes(), nil
}
