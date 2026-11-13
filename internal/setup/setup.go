package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	weaver "github.com/robbowland/weaver"
	"github.com/robbowland/weaver/internal/cli"
)

// reOurs is a hook of weaver's written as one command line (wvr by name or
// path, run as a hook): "wvr hook --async", "/some path/bin/wvr hook".
var reOurs = regexp.MustCompile(`^(.*/)?wvr hook( |$)`)

// ourNames are weaver's names.
var ourNames = map[string]bool{"wvr": true}

// Run is `wvr setup [--dry-run] [--bin PATH]`: weaver's hooks into Claude
// Code's settings, run by this binary's path (or PATH), each file backed up before
// it changes; the files it brings to other programs written (BroughtFiles);
// its files unpacked. `wvr` does the same as it starts (SelfSetup); this
// shows the changes.
func Run(args []string, self string, out io.Writer) error {
	dry, bin := false, self
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dry-run":
			dry = true
		case "--bin":
			if i+1 >= len(args) {
				return &cli.Exit{Code: 2, Err: errors.New("--bin needs a path")}
			}
			i++
			bin = args[i]
		default:
			return &cli.Exit{Code: 2, Err: errors.New("usage: wvr setup [--dry-run] [--bin PATH]")}
		}
	}
	if !dry {
		if _, err := Runtime(); err != nil {
			return err
		}
	}
	ts := Timestamp()
	// Each configuration's problem is said as it comes; the others go on.
	var failed error
	for _, c := range Configs() {
		ch, err := Plan(c, bin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "weaver: "+err.Error())
			failed = cli.ErrSaid
			continue
		}
		ch.Show(out)
		if dry || !ch.Changed() {
			continue
		}
		backup, err := ch.Apply(ts)
		if err != nil {
			fmt.Fprintln(os.Stderr, "weaver: "+err.Error())
			failed = cli.ErrSaid
			continue
		}
		if backup != "" {
			fmt.Fprintf(out, "backed up to %s\n", backup)
		}
	}
	for _, f := range BroughtFiles() {
		switch f.Fate() {
		case Current:
			fmt.Fprintf(out, "%s: %s is weaver's, as it writes it\n", f.Label, f.Path)
		case Yours:
			fmt.Fprintf(out, "%s: %s is yours, left alone\n", f.Label, f.Path)
		case ToWrite:
			if dry {
				fmt.Fprintf(out, "%s: would write %s\n", f.Label, f.Path)
			} else if err := f.Write(); err != nil {
				fmt.Fprintln(os.Stderr, "weaver: "+err.Error())
				failed = cli.ErrSaid
			} else {
				fmt.Fprintf(out, "%s: wrote %s\n", f.Label, f.Path)
			}
		}
	}
	if dry || failed != nil {
		return failed
	}
	fmt.Fprint(out, `
Done. To pick it up:
  - restart any Claude Code sessions that are already running: they read hooks at start
`)
	return nil
}

// Change is what weaver's hooks make of an agent's hooks file: the
// file as it is (existed false: not there yet) and as it would be.
type Change struct {
	label, file string
	old, new    []byte
	existed     bool
}

// Plan is what putting weaver's hooks (the embedded file hooks), run by
// bin, into the agent's hooks file f would change.
func Plan(c Config, bin string) (Change, error) {
	add, err := weaver.Files.ReadFile(c.Hooks)
	if err != nil {
		return Change{}, err
	}
	ch := Change{label: c.Label, file: c.File}
	ch.old, err = os.ReadFile(c.File)
	ch.existed = err == nil
	if !ch.existed {
		ch.old = []byte("{}")
	}
	if ch.new, err = mergeHooks(ch.old, add, bin); err != nil {
		return Change{}, fmt.Errorf("cannot parse %s: %v", c.File, err)
	}
	return ch, nil
}

// changed: does the file need writing (the same JSON, laid out differently,
// does not)?
func (ch Change) Changed() bool {
	var a, b bytes.Buffer
	return !ch.existed || json.Compact(&a, ch.old) != nil || json.Compact(&b, ch.new) != nil || a.String() != b.String()
}

// Show says what the change is: a diff, or that there is none.
func (ch Change) Show(out io.Writer) {
	if !ch.Changed() {
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
	_ = d.Run() // diff exits 1 when the files differ, which is the point
}

// Apply writes the change, the file backed up first (to its name and
// .bak.ts) when it was there; the backup's name, or "".
func (ch Change) Apply(ts string) (string, error) {
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

func Timestamp() string { return time.Now().Format("20060102-150405") }

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

// mergeHooks is settings with every weaver hook (whatever its form: "wvr
// hook", or a path to wvr with args) removed and
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
					Args    any `json:"args"`
				}
				// A hook that is not an object is not weaver's: kept.
				if json.Unmarshal(h, &c) == nil && isOurs(c.Command, c.Args) {
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
						hs[i] = json.RawMessage(ho.set("command", quote(c)).String())
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

// isOurs is whether a hook runs weaver as a hook: its command wvr (by name
// or path, spaces and all) with hook its first argument, or one command
// line saying that (reOurs). Another program named wvr, or wvr run
// otherwise, is yours.
func isOurs(command, args any) bool {
	s, ok := command.(string)
	if !ok {
		return false
	}
	if as, ok := args.([]any); ok && len(as) > 0 {
		first, _ := as[0].(string)
		return ourNames[filepath.Base(s)] && first == "hook"
	}
	return reOurs.MatchString(s)
}
