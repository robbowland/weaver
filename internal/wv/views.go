package wv

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"

	weaver "github.com/robbowland/weaver"
)

// Views: the weaver session's windows beside home that show work from
// elsewhere through the best tool for it: GITHUB (gh-dash) and LINEAR
// (linear-tui). Out of the thread numbering, at viewBase and on, each where
// its tool is installed (or as $WEAVER_VIEWS says). Alt y steps from a
// thread to home, on through the views, and back to the thread; in a view,
// Alt N starts a thread from the item under the cursor and Alt R a
// read-only review of it, through the new-thread form.

const viewBase = 900

type view struct {
	name, label string
	argv        []string
	env         []string
	dir         string
}

// ghDash is how gh-dash runs: its own binary, else as gh's extension.
func ghDash() []string {
	if p, err := exec.LookPath("gh-dash"); err == nil {
		return []string{p}
	}
	if p, err := exec.LookPath("gh"); err == nil {
		d := os.Getenv("XDG_DATA_HOME")
		if d == "" {
			d = os.Getenv("HOME") + "/.local/share"
		}
		if exists(d + "/gh/extensions/gh-dash") {
			return []string{p, "dash"}
		}
	}
	return nil
}

// viewNames are the session's views, in order: $WEAVER_VIEWS (none, or
// names), else every one whose tool is installed.
func viewNames() []string {
	all := []string{"github", "linear"}
	want, set := os.LookupEnv("WEAVER_VIEWS")
	if set {
		var ns []string
		for _, n := range strings.Fields(strings.ReplaceAll(want, ",", " ")) {
			for _, a := range all {
				if n == a {
					ns = append(ns, n)
				}
			}
		}
		return ns
	}
	var ns []string
	if ghDash() != nil {
		ns = append(ns, "github")
	}
	if _, err := exec.LookPath("linear-tui"); err == nil {
		ns = append(ns, "linear")
	}
	return ns
}

// viewOf is view name as it runs in this session.
func (e *Env) viewOf(name string) (view, error) {
	home := os.Getenv("HOME")
	switch name {
	case "github":
		argv := ghDash()
		if argv == nil {
			return view{}, fmt.Errorf("gh-dash is not installed (brew install gh-dash, or gh extension install dlvhdr/gh-dash)")
		}
		cfg, err := e.ghDashConfig()
		if err != nil {
			return view{}, err
		}
		// From home, not a repository: gh-dash would narrow itself to one.
		return view{name, "GITHUB", argv, []string{"GH_DASH_CONFIG=" + cfg}, home}, nil
	case "linear":
		p, err := exec.LookPath("linear-tui")
		if err != nil {
			return view{}, fmt.Errorf("linear-tui is not installed (see github.com/k1-c/linear-tui)")
		}
		// linear-tui keeps a view per repository, and a thread started
		// from it works there: the repository used last.
		dir := home
		if l, err := firstLine(e.StateDir + "/repos"); err == nil && l != "" && exists(l) {
			dir = l
		}
		return view{name, "LINEAR", []string{p}, nil, dir}, nil
	}
	return view{}, fmt.Errorf("no view %q (github or linear)", name)
}

// ghDashConfig writes gh-dash's config for the view: yours (gh-dash's
// global one), with weaver's keybindings added and, unless
// $WEAVER_VIEW_THEME is 0, weaver's theme. Its path.
func (e *Env) ghDashConfig() (string, error) {
	add, err := weaver.Files.ReadFile("config/gh-dash.yml")
	if err != nil {
		return "", err
	}
	add = []byte(strings.ReplaceAll(string(add), "WEAVER ", Quote(e.Self)+" "))
	var mine yaml.Node
	f := os.Getenv("XDG_CONFIG_HOME")
	if f == "" {
		f = os.Getenv("HOME") + "/.config"
	}
	f += "/gh-dash/config.yml"
	if b, err := os.ReadFile(f); err == nil {
		if err := yaml.Unmarshal(b, &mine); err != nil {
			return "", fmt.Errorf("cannot read %s: %v", f, err)
		}
	}
	var ours yaml.Node
	if err := yaml.Unmarshal(add, &ours); err != nil {
		return "", err
	}
	out := mergeGhDash(&mine, &ours, os.Getenv("WEAVER_VIEW_THEME") != "0")
	b, err := yaml.Marshal(out)
	if err != nil {
		return "", err
	}
	os.MkdirAll(e.StateDir, 0o755)
	p := e.StateDir + "/gh-dash.yml"
	tmp := p + "." + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, append([]byte("# Made by weaver from your gh-dash config, for its GITHUB view: edit yours.\n"), b...), 0o644); err != nil {
		return "", err
	}
	return p, os.Rename(tmp, p)
}

// mapGet is the value of key k in mapping node m, or nil.
func mapGet(m *yaml.Node, k string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == k {
			return m.Content[i+1]
		}
	}
	return nil
}

func mapSet(m *yaml.Node, k string, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == k {
			m.Content[i+1] = v
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}, v)
}

// mergeGhDash is your gh-dash config (a document node, maybe empty) with
// ours put in: our keybindings added to each section, replacing any of
// yours on the same key, and our theme when theme is true.
func mergeGhDash(mine, ours *yaml.Node, theme bool) *yaml.Node {
	top := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	if mine.Kind == yaml.DocumentNode && len(mine.Content) == 1 && mine.Content[0].Kind == yaml.MappingNode {
		top = mine.Content[0]
	}
	o := ours.Content[0]
	if kb := mapGet(o, "keybindings"); kb != nil {
		mk := mapGet(top, "keybindings")
		if mk == nil || mk.Kind != yaml.MappingNode {
			mk = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			mapSet(top, "keybindings", mk)
		}
		for i := 0; i+1 < len(kb.Content); i += 2 {
			sec, add := kb.Content[i].Value, kb.Content[i+1]
			list := mapGet(mk, sec)
			if list == nil || list.Kind != yaml.SequenceNode {
				list = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
				mapSet(mk, sec, list)
			}
			taken := map[string]bool{}
			for _, b := range add.Content {
				if k := mapGet(b, "key"); k != nil {
					taken[k.Value] = true
				}
			}
			var keep []*yaml.Node
			for _, b := range list.Content {
				if k := mapGet(b, "key"); k == nil || !taken[k.Value] {
					keep = append(keep, b)
				}
			}
			list.Content = append(keep, add.Content...)
		}
	}
	if t := mapGet(o, "theme"); theme && t != nil {
		mapSet(top, "theme", t)
	}
	return top
}

// viewWindows are the session's view windows, by view name: window ids.
func (e *Env) viewWindows() map[string]string {
	out, err := e.Tmux("list-windows", "-t", "="+e.Session, "-F", "#{window_id} #{@weaver-view}")
	ws := map[string]string{}
	if err != nil {
		return ws
	}
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		id, v, _ := strings.Cut(l, " ")
		if v != "" {
			ws[v] = strings.TrimPrefix(id, "@")
		}
	}
	return ws
}

// OpenView shows view name (started first if it is not running); with
// quiet, starts it without going there. Its window id.
func (e *Env) OpenView(name string, quiet bool) (string, error) {
	if id := e.viewWindows()[name]; id != "" {
		if !quiet {
			e.Tmux("select-window", "-t", "@"+id)
		}
		return id, nil
	}
	v, err := e.viewOf(name)
	if err != nil {
		return "", err
	}
	idx := viewBase
	for i, n := range viewNames() {
		if n == name {
			idx = viewBase + i
		}
	}
	args := []string{"new-window", "-P", "-F", "#{window_id}"}
	if quiet {
		args = append(args, "-d")
	}
	args = append(args, "-t", "="+e.Session+":"+strconv.Itoa(idx), "-n", v.label, "-c", v.dir, "-e", "WEAVER_TMUX_SESSION="+e.Session)
	for _, x := range v.env {
		args = append(args, "-e", x)
	}
	out, err := e.Tmux(append(args, v.argv...)...)
	if err != nil {
		return "", fmt.Errorf("tmux could not open the %s view", v.label)
	}
	id := strings.TrimPrefix(strings.TrimSpace(string(out)), "@")
	e.Tmux("set-option", "-w", "-t", "@"+id, "@weaver-view", name)
	// A session started before its views (or an older weaver) has no strip.
	e.SetStrip()
	return id, nil
}

// SetStrip sets the session's view strip, the status line's left on home
// and in a view: THREADS and each view, the one you are on in ink.
func (e *Env) SetStrip() {
	strip := "#{?#{==:#{window_index},0},#[fg=#ffffff],#[fg=#616161]}THREADS"
	for _, n := range viewNames() {
		strip += "   #{?#{==:#{@weaver-view}," + n + "},#[fg=#ffffff],#[fg=#616161]}" + strings.ToUpper(n)
	}
	e.Tmux("set-option", "-t", "="+e.Session+":", "@weaver-strip", strip)
}

// StartViews starts the session's views in the background, so the first
// Alt y to one finds it ready.
func (e *Env) StartViews() {
	for _, n := range viewNames() {
		e.OpenView(n, true)
	}
}

// View is `weaver view [NAME]`: go to view NAME (the first, without one).
func (e *Env) View(args []string) int {
	ns := viewNames()
	name := ""
	if len(args) > 0 {
		name = args[0]
	} else if len(ns) > 0 {
		name = ns[0]
	}
	if name == "" {
		fmt.Fprintln(os.Stderr, "weaver: no views: install gh-dash or linear-tui (or set WEAVER_VIEWS)")
		return 1
	}
	e.SetStrip()
	if _, err := e.OpenView(name, false); err != nil {
		fmt.Fprintln(os.Stderr, "weaver: "+err.Error())
		return 1
	}
	return 0
}

// From is `weaver _from github pr|issue REPO NUM [--review]` (gh-dash's
// Alt N and Alt R) and `weaver _from linear [--review]` (the LINEAR view's):
// the new-thread form for that item, in a popup, while the view goes on.
func (e *Env) From(args []string) int {
	review := false
	var rest []string
	for _, a := range args {
		if a == "--review" {
			review = true
		} else {
			rest = append(rest, a)
		}
	}
	say := func(m string) int {
		e.Tmux("display-message", "-d", "2500", "weaver: "+m)
		return 1
	}
	var nw []string
	dir := os.Getenv("HOME")
	switch {
	case len(rest) == 4 && rest[0] == "github" && (rest[1] == "pr" || rest[1] == "issue"):
		nw = []string{"--" + rest[1], rest[2] + "#" + rest[3]}
	case len(rest) >= 1 && rest[0] == "linear":
		d := ""
		if id := e.viewWindows()["linear"]; id != "" {
			if out, err := e.Tmux("display-message", "-p", "-t", "@"+id, "#{pane_current_path}"); err == nil {
				d = strings.TrimSpace(string(out))
			}
		}
		if d == "" {
			d = dir
		}
		id, err := linearSelected(d)
		if err != nil {
			return say(err.Error())
		}
		dir = d
		nw = []string{"--issue", id}
		if _, _, ok := repoOf(d); ok {
			nw = append(nw, "--repo", d)
		}
	default:
		fmt.Fprintln(os.Stderr, "weaver: usage: weaver _from github pr|issue REPO NUM [--review] | linear [--review]")
		return 2
	}
	if review {
		nw = append(nw, "--review")
	}
	// The form in a popup, from a process of its own: whatever ran this
	// (gh-dash, waiting on its command) goes on at once.
	argv := []string{"display-popup", "-B", "-E", "-w", "96", "-h", "20", "-d", dir, "--", e.Self, "new"}
	argv = append(argv, nw...)
	if !e.InTmux {
		argv = append(append([]string{}, e.TSock...), argv...)
	}
	c := exec.Command("tmux", argv...)
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil {
		return say("could not open the form")
	}
	return 0
}

// linearSelected is the Linear issue under linear-tui's cursor in dir (or
// the one it has open), from its view snapshot.
func linearSelected(dir string) (string, error) {
	out, err := runJSON("linear-tui", "context", "--json", "--workspace", dir)
	if err != nil {
		return "", err
	}
	var c struct {
		Snapshot *struct {
			Version     int    `json:"version"`
			Screen      string `json:"screen"`
			Issue       *struct{ Identifier string }
			Rows        []struct{ Kind, Identifier string }
			SelectedRow *int `json:"selected_row"`
		} `json:"snapshot"`
	}
	if err := json.Unmarshal(out, &c); err != nil {
		return "", fmt.Errorf("linear-tui context: %v", err)
	}
	s := c.Snapshot
	switch {
	case s == nil:
		return "", fmt.Errorf("linear-tui has no view open in %s", filepath.Base(dir))
	case s.Version != 1:
		return "", fmt.Errorf("linear-tui's view is version %d, which this weaver does not know", s.Version)
	case s.Screen == "issue_detail" && s.Issue != nil && s.Issue.Identifier != "":
		return s.Issue.Identifier, nil
	case s.SelectedRow != nil && *s.SelectedRow >= 0 && *s.SelectedRow < len(s.Rows):
		if r := s.Rows[*s.SelectedRow]; r.Kind == "issue" && r.Identifier != "" {
			return r.Identifier, nil
		}
	}
	return "", fmt.Errorf("no issue under linear-tui's cursor")
}
