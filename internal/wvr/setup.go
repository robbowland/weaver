package wvr

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
// it changes; its files unpacked and its Neovim plugins fetched. `wvr`
// does the same as it starts (SelfSetup); this shows the changes.
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
	warnZellij(os.Getenv("HOME"))
	ts := timestamp()
	rc := 0
	codexDone := false
	for _, c := range hookConfigs() {
		if c.label == "codex" && !codex {
			continue
		}
		codexDone = codexDone || c.label == "codex"
		ch, err := planHooks(c, bin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "weaver: "+err.Error())
			rc = 1
			continue
		}
		ch.show(out)
		if dry || !ch.changed() {
			continue
		}
		backup, err := ch.apply(ts)
		if err != nil {
			fmt.Fprintln(os.Stderr, "weaver: "+err.Error())
			rc = 1
			continue
		}
		if backup != "" {
			fmt.Fprintf(out, "backed up to %s\n", backup)
		}
	}
	if !dry && rc == 0 {
		if err := Plugins(); err != nil {
			fmt.Fprintln(os.Stderr, "weaver: "+err.Error())
		}
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

// hookChange is what weaver's hooks make of an agent's hooks file: the
// file as it is (existed false: not there yet) and as it would be.
type hookChange struct {
	label, file string
	old, new    []byte
	existed     bool
}

// planHooks is what putting weaver's hooks (the embedded file hooks), run by
// bin, into the agent's hooks file f would change.
func planHooks(c hookConfig, bin string) (hookChange, error) {
	add, err := weaver.Files.ReadFile(c.hooks)
	if err != nil {
		return hookChange{}, err
	}
	ch := hookChange{label: c.label, file: c.file}
	ch.old, err = os.ReadFile(c.file)
	ch.existed = err == nil
	if !ch.existed {
		ch.old = []byte("{}")
	}
	if ch.new, err = mergeHooks(ch.old, add, bin); err != nil {
		return hookChange{}, fmt.Errorf("cannot parse %s: %v", c.file, err)
	}
	return ch, nil
}

// changed: does the file need writing (the same JSON, laid out differently,
// does not)?
func (ch hookChange) changed() bool {
	var a, b bytes.Buffer
	return !ch.existed || json.Compact(&a, ch.old) != nil || json.Compact(&b, ch.new) != nil || a.String() != b.String()
}

// show says what the change is: a diff, or that there is none.
func (ch hookChange) show(out io.Writer) {
	if !ch.changed() {
		fmt.Fprintf(out, "%s: %s already runs weaver on every event\n", ch.label, ch.file)
		return
	}
	fmt.Fprintf(out, "--- %s: %s\n", ch.label, ch.file)
	tmp, err := os.CreateTemp("", "weaver-setup.")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(ch.new); err != nil {
		return
	}
	if tmp.Close() != nil {
		return
	}
	from := ch.file
	if !ch.existed {
		from = "/dev/null"
	}
	d := exec.Command("diff", "-u", from, tmp.Name())
	d.Stdout = out
	bestEffort(d.Run()) // diff exits 1 when the files differ, which is the point
}

// apply writes the change, the file backed up first (to its name and
// .bak.ts) when it was there; the backup's name, or "".
func (ch hookChange) apply(ts string) (string, error) {
	backup := ""
	if ch.existed {
		backup = ch.file + ".bak." + ts
		if err := copyFile(ch.file, backup); err != nil {
			return "", fmt.Errorf("cannot back up %s; not changing it", ch.file)
		}
	} else if err := os.MkdirAll(filepath.Dir(ch.file), 0o755); err != nil {
		return "", fmt.Errorf("cannot create %s", filepath.Dir(ch.file))
	}
	// Through the file, so a linked settings file stays a link.
	if err := os.WriteFile(ch.file, ch.new, 0o644); err != nil {
		return backup, fmt.Errorf("cannot write %s (backup: %s)", ch.file, backup)
	}
	return backup, nil
}

func timestamp() string { return time.Now().Format("20060102-150405") }

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
