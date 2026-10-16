package wvr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/template"
	"time"

	weaver "github.com/robbowland/weaver"
	"github.com/robbowland/weaver/internal/cli"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/git"
	"github.com/robbowland/weaver/internal/items"
	"github.com/robbowland/weaver/internal/store"
	"github.com/robbowland/weaver/internal/tmux"
)

// Views: the weaver session's windows beside home that show work from
// elsewhere through the best tool for it: GITHUB (gh-dash) and LINEAR
// (linear-tui). Out of the thread numbering, at viewBase and on; one whose
// tool is not installed or signed in yet shows how to set it up. Alt y steps from a
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
		if files.Exists(d + "/gh/extensions/gh-dash") {
			return []string{p, "dash"}
		}
	}
	return nil
}

// viewNames are the session's views, in order: $WEAVER_VIEWS (none, or
// names), else every view weaver has but those you hid. One not ready yet
// shows faint in the strip, and going there sets it up.
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
	hidden := e.hiddenViews()
	var ns []string
	for _, n := range all {
		if !hidden[n] {
			ns = append(ns, n)
		}
	}
	return ns
}

// hiddenViews are the views you hid from their setup screen, kept in
// $STATE_DIR/hidden-views; `wvr view NAME` brings one back.
func (e *Env) hiddenViews() map[string]bool {
	m := map[string]bool{}
	b, _ := os.ReadFile(e.StateDir + "/hidden-views")
	for _, n := range strings.Fields(string(b)) {
		m[n] = true
	}
	return m
}

func (e *Env) setHidden(name string, hide bool) {
	m := e.hiddenViews()
	if m[name] == hide {
		return
	}
	m[name] = hide
	var ns []string
	for n, h := range m {
		if h {
			ns = append(ns, n)
		}
	}
	sort.Strings(ns)
	bestEffort(store.WriteAtomic(e.StateDir+"/hidden-views", []byte(strings.Join(ns, "\n")+"\n")))
}

// viewState is how ready a view is to show.
type viewState int

const (
	viewReady     viewState = iota
	viewMissing             // a tool it needs is not installed
	viewSignedOut           // its tool is not signed in
)

// viewNeed is what a view needs before it shows: how ready it is, what is
// missing, and the command that fixes it (none when weaver knows none).
type viewNeed struct {
	state viewState
	what  string
	fix   []string
}

// brewFix is installing formula with Homebrew, when there is Homebrew.
func brewFix(formula string) []string {
	if !onPath("brew") {
		return nil
	}
	return []string{"brew", "install", formula}
}

// viewNeeds is what view name needs before it shows.
func (e *Env) viewNeeds(name string) viewNeed {
	switch name {
	case "github":
		switch {
		case !onPath("gh"):
			return viewNeed{viewMissing, "gh is not installed", brewFix("gh")}
		case ghDash() == nil:
			return viewNeed{viewMissing, "gh-dash is not installed", brewFix("robbowland/kegs/gh-dash")}
		case !e.signedIn("gh"):
			return viewNeed{viewSignedOut, "gh is not signed in to GitHub", []string{"gh", "auth", "login"}}
		}
	case "linear":
		switch {
		case !onPath("linear-tui"):
			return viewNeed{viewMissing, "linear-tui is not installed", brewFix("robbowland/kegs/linear-tui")}
		case !e.signedIn("linear-tui"):
			return viewNeed{viewSignedOut, "linear-tui is not signed in to Linear", []string{"linear-tui", "auth", "login"}}
		}
	}
	return viewNeed{state: viewReady}
}

func onPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// signedIn: is tool (gh, linear-tui) signed in, as its `auth status` says?
// Asked at most once a minute, the answer kept in $STATE_DIR (linear-auth,
// gh-auth): Alt y asks on every press.
func (e *Env) signedIn(tool string) bool {
	f := e.authFile(tool)
	if st, err := os.Stat(f); err == nil && time.Since(st.ModTime()) < time.Minute {
		b, _ := os.ReadFile(f)
		return strings.TrimSpace(string(b)) == "yes"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, tool, "auth", "status")
	c.WaitDelay = time.Second
	var out bytes.Buffer
	c.Stdout = &out
	ok := c.Run() == nil && !strings.Contains(out.String(), "Not authenticated")
	ans := "no"
	if ok {
		ans = "yes"
	}
	bestEffort(store.WriteAtomic(f, []byte(ans+"\n")))
	return ok
}

func (e *Env) authFile(tool string) string {
	if tool == "linear-tui" {
		return e.StateDir + "/linear-auth"
	}
	return e.StateDir + "/" + tool + "-auth"
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
		if l, err := files.FirstLine(e.StateDir + "/repos"); err == nil && l != "" && files.Exists(l) {
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
func (e *Env) viewWindows() map[string]tmux.WindowID {
	out, err := e.Tmux("list-windows", "-t", "="+e.Session, "-F", "#{window_id} #{@weaver-view}")
	ws := map[string]tmux.WindowID{}
	if err != nil {
		return ws
	}
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		id, v, _ := strings.Cut(l, " ")
		if w, ok := tmux.ParseWindow(id); ok && v != "" {
			ws[v] = w
		}
	}
	return ws
}

// OpenView shows view name, started first if it is not running. Its
// window id.
func (e *Env) OpenView(name string) (tmux.WindowID, error) {
	return e.view(name, true)
}

// startView starts view name, if it is not running, without going there.
func (e *Env) startView(name string) (tmux.WindowID, error) {
	return e.view(name, false)
}

func (e *Env) view(name string, show bool) (tmux.WindowID, error) {
	if id := e.viewWindows()[name]; id != "" {
		if show {
			e.Tmux("select-window", "-t", id.Target())
		}
		return id, nil
	}
	v, err := e.viewOf(name)
	if e.viewNeeds(name).state != viewReady {
		// Not ready: its window shows what it needs, and becomes the view
		// once it has it.
		v, err = view{name, strings.ToUpper(name), []string{e.Self, "_viewsetup", name}, nil, os.Getenv("HOME")}, nil
	}
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
	id, _ := tmux.ParseWindow(string(out))
	e.Tmux("set-option", "-w", "-t", id.Target(), "@weaver-view", name)
	// A session started before its views (or an older weaver) has no strip.
	e.SetStrip()
	return id, nil
}

// SetStrip sets the session's view strip, the status line's left on home
// and in a view: THREADS and each view, the one you are on in ink, one not
// set up yet with a faint dot.
func (e *Env) SetStrip() {
	strip := "#{?#{==:#{window_index},0},#[fg=#ffffff],#[fg=#616161]}THREADS"
	for _, n := range e.viewNames() {
		strip += "   #{?#{==:#{@weaver-view}," + n + "},#[fg=#ffffff],#[fg=#616161]}" + strings.ToUpper(n)
		if e.viewNeeds(n).state != viewReady {
			strip += " #[fg=#616161]·"
		}
	}
	e.Tmux("set-option", "-t", "="+e.Session+":", "@weaver-strip", strip)
}

// StartViews starts the session's views in the background, so the first
// Alt y to one finds it ready.
func (e *Env) StartViews() {
	for _, n := range e.viewNames() {
		if e.viewNeeds(n).state == viewReady {
			_, err := e.startView(n)
			bestEffort(err)
		}
	}
}

// View is `wvr view [NAME]`: go to view NAME (the first, without one),
// shown again if you hid it.
func (e *Env) View(args []string) error {
	ns := e.viewNames()
	name := ""
	if len(args) > 0 {
		name = args[0]
		e.setHidden(name, false)
	} else if len(ns) > 0 {
		name = ns[0]
	}
	if name == "" {
		return errors.New("no views (WEAVER_VIEWS names none)")
	}
	e.SetStrip()
	if _, err := e.OpenView(name); err != nil {
		return err
	}
	return nil
}

// From is `wvr _from github pr|issue REPO NUM [--review]` (gh-dash's
// Alt N and Alt R) and `wvr _from linear [--review]` (the LINEAR view's):
// the new-thread form for that item, in a popup, while the view goes on.
func (e *Env) From(args []string) error {
	review := false
	var rest []string
	for _, a := range args {
		if a == "--review" {
			review = true
		} else {
			rest = append(rest, a)
		}
	}
	// In a view, said on tmux's message line.
	say := func(m string) error {
		e.Tmux("display-message", "-d", "2500", "weaver: "+m)
		return cli.ErrSaid
	}
	var nw []string
	dir := os.Getenv("HOME")
	switch {
	case len(rest) == 4 && rest[0] == "github" && (rest[1] == "pr" || rest[1] == "issue"):
		nw = []string{"--" + rest[1], rest[2] + "#" + rest[3]}
	case len(rest) >= 1 && rest[0] == "linear":
		d := ""
		if id := e.viewWindows()["linear"]; id != "" {
			if out, err := e.Tmux("display-message", "-p", "-t", id.Target(), "#{pane_current_path}"); err == nil {
				d = strings.TrimSpace(string(out))
			}
		}
		if d == "" {
			d = dir
		}
		id, err := items.LinearSelected(d)
		if err != nil {
			return say(err.Error())
		}
		dir = d
		nw = []string{"--issue", id}
		if _, _, ok := git.RepoOf(d); ok {
			nw = append(nw, "--repo", d)
		}
	default:
		return &cli.Exit{Code: 2, Err: errors.New("usage: wvr _from github pr|issue REPO NUM [--review] | linear [--review]")}
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
	return nil
}
