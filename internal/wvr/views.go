package wvr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"text/template"
	"time"

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
// names), else every one whose tool is installed and ready (linear-tui
// signed in).
func (e *Env) viewNames() []string {
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
	if e.linearReady() {
		ns = append(ns, "linear")
	}
	return ns
}

// linearReady: is linear-tui installed and signed in? Asked of linear-tui
// (`auth status`) at most once a minute, the answer kept in
// $STATE_DIR/linear-auth: Alt y asks every press.
func (e *Env) linearReady() bool {
	p, err := exec.LookPath("linear-tui")
	if err != nil {
		return false
	}
	f := e.StateDir + "/linear-auth"
	if st, err := os.Stat(f); err == nil && time.Since(st.ModTime()) < time.Minute {
		b, _ := os.ReadFile(f)
		return strings.TrimSpace(string(b)) == "yes"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, p, "auth", "status")
	c.WaitDelay = time.Second
	var out bytes.Buffer
	c.Stdout = &out
	ok := c.Run() == nil && !strings.Contains(out.String(), "Not authenticated")
	ans := "no"
	if ok {
		ans = "yes"
	}
	os.MkdirAll(e.StateDir, 0o755)
	os.WriteFile(f, []byte(ans+"\n"), 0o644)
	return ok
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
		env, err := e.ghDashConfig(argv)
		if err != nil {
			return view{}, err
		}
		// From home, not a repository: gh-dash would narrow itself to one.
		return view{name, "GITHUB", argv, env, home}, nil
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

// ghDashConfig writes the whole of gh-dash's config for the view (from
// config/gh-dash.yml) under its own XDG_CONFIG_HOME: gh-dash reads its
// global config first and GH_DASH_CONFIG on top, so neither may be yours.
// The environment gh-dash runs with: that config home, gh's own config
// where gh keeps it (its sign-in), and no GH_DASH_CONFIG.
func (e *Env) ghDashConfig(argv []string) ([]string, error) {
	src, err := weaver.Files.ReadFile("config/gh-dash.yml")
	if err != nil {
		return nil, err
	}
	t, err := template.New("gh-dash").Parse(string(src))
	if err != nil {
		return nil, err
	}
	// The command gh-dash runs through a shell, written into YAML as it is:
	// this binary's path when it needs no quoting, else wvr from PATH.
	wvr := e.Self
	if !rePlainPath.MatchString(wvr) {
		wvr = "wvr"
	}
	// The fork has weaver's extras (only some views, line-by-line preview
	// scrolling); another gh-dash would refuse them.
	out, _ := exec.Command(argv[0], append(argv[1:], "--version")...).Output()
	var b strings.Builder
	if err := t.Execute(&b, struct {
		Wvr  string
		Fork bool
	}{wvr, reFork.Match(out)}); err != nil {
		return nil, err
	}
	home := e.StateDir + "/gh-dash"
	if err := os.MkdirAll(home+"/gh-dash", 0o755); err != nil {
		return nil, err
	}
	p := home + "/gh-dash/config.yml"
	tmp := p + "." + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, p); err != nil {
		return nil, err
	}
	gh := os.Getenv("GH_CONFIG_DIR")
	if gh == "" {
		if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
			gh = x + "/gh"
		} else {
			gh = os.Getenv("HOME") + "/.config/gh"
		}
	}
	return []string{"XDG_CONFIG_HOME=" + home, "GH_CONFIG_DIR=" + gh, "GH_DASH_CONFIG="}, nil
}

var (
	rePlainPath = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)
	reFork      = regexp.MustCompile(`-rb[0-9]`)
)

// viewWindows are the session's view windows, by view name: window ids.
func (e *Env) viewWindows() map[string]WindowID {
	out, err := e.Tmux("list-windows", "-t", "="+e.Session, "-F", "#{window_id} #{@weaver-view}")
	ws := map[string]WindowID{}
	if err != nil {
		return ws
	}
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		id, v, _ := strings.Cut(l, " ")
		if w, ok := parseWindow(id); ok && v != "" {
			ws[v] = w
		}
	}
	return ws
}

// OpenView shows view name, started first if it is not running. Its
// window id.
func (e *Env) OpenView(name string) (WindowID, error) {
	return e.view(name, true)
}

// startView starts view name, if it is not running, without going there.
func (e *Env) startView(name string) (WindowID, error) {
	return e.view(name, false)
}

func (e *Env) view(name string, show bool) (WindowID, error) {
	if id := e.viewWindows()[name]; id != "" {
		if show {
			e.Tmux("select-window", "-t", id.target())
		}
		return id, nil
	}
	v, err := e.viewOf(name)
	if err != nil {
		return "", err
	}
	// Each view has its own index, shown or not: GITHUB 900, LINEAR 901.
	idx := viewBase
	if name == "linear" {
		idx++
	}
	args := []string{"new-window", "-P", "-F", "#{window_id}"}
	if !show {
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
	id, _ := parseWindow(string(out))
	e.Tmux("set-option", "-w", "-t", id.target(), "@weaver-view", name)
	// A session started before its views (or an older weaver) has no strip.
	e.SetStrip()
	return id, nil
}

// SetStrip sets the session's view strip, the status line's left on home
// and in a view: THREADS and each view, the one you are on in ink.
func (e *Env) SetStrip() {
	strip := "#{?#{==:#{window_index},0},#[fg=#ffffff],#[fg=#616161]}THREADS"
	for _, n := range e.viewNames() {
		strip += "   #{?#{==:#{@weaver-view}," + n + "},#[fg=#ffffff],#[fg=#616161]}" + strings.ToUpper(n)
	}
	e.Tmux("set-option", "-t", "="+e.Session+":", "@weaver-strip", strip)
}

// StartViews starts the session's views in the background, so the first
// Alt y to one finds it ready.
func (e *Env) StartViews() {
	for _, n := range e.viewNames() {
		_, err := e.startView(n)
		bestEffort(err)
	}
}

// View is `wvr view [NAME]`: go to view NAME (the first, without one).
func (e *Env) View(args []string) int {
	ns := e.viewNames()
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
	if _, err := e.OpenView(name); err != nil {
		fmt.Fprintln(os.Stderr, "weaver: "+err.Error())
		return 1
	}
	return 0
}

// From is `wvr _from github pr|issue REPO NUM [--review]` (gh-dash's
// Alt N and Alt R) and `wvr _from linear [--review]` (the LINEAR view's):
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
			if out, err := e.Tmux("display-message", "-p", "-t", id.target(), "#{pane_current_path}"); err == nil {
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
		fmt.Fprintln(os.Stderr, "weaver: usage: wvr _from github pr|issue REPO NUM [--review] | linear [--review]")
		return 2
	}
	if review {
		nw = append(nw, "--review")
	}
	// The form in a popup, from a process of its own: whatever ran this
	// (gh-dash, waiting on its command) goes on at once.
	// On the client looking at this session: what ran this may have none
	// (tmux's run-shell).
	argv := []string{"display-popup"}
	if out, err := e.Tmux("list-clients", "-t", "="+e.Session, "-F", "#{client_activity} #{client_name}"); err == nil {
		best, at := "", ""
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if t, c, ok := strings.Cut(l, " "); ok && t >= at {
				best, at = c, t
			}
		}
		if best != "" {
			argv = append(argv, "-c", best)
		}
	}
	argv = append(argv, "-B", "-E", "-w", "96", "-h", "20", "-d", dir, "--", e.Self, "new")
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
