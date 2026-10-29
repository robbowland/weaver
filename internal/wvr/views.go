package wvr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/robbowland/weaver/internal/cli"
	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/linear"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/store"
	"github.com/robbowland/weaver/internal/tmux"
)

// Views: the weaver session's windows beside home that show work from
// elsewhere: GITHUB (internal/github) and LINEAR (internal/linear), each
// through itemsview.go. Out of the thread numbering, at viewBase and on; one
// not signed in yet shows how to set it up. Alt y steps from a
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
		case !e.signedIn("github"):
			return viewNeed{viewSignedOut, "gh is not signed in to GitHub", []string{"gh", "auth", "login"}}
		}
	case "linear":
		if !e.signedIn("linear") {
			return viewNeed{viewSignedOut, "weaver has no Linear API key", []string{"wvr", "linear", "login"}}
		}
	}
	return viewNeed{state: viewReady}
}

func onPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// signedIn is whether view name's service is signed in: gh has a token, or
// weaver a Linear key. Asked at most once a minute, the answer kept in
// $STATE_DIR (github-auth, linear-auth): Alt y asks on every press.
func (e *Env) signedIn(name string) bool {
	f := e.authFile(name)
	if st, err := os.Stat(f); err == nil && time.Since(st.ModTime()) < time.Minute {
		b, _ := os.ReadFile(f)
		return strings.TrimSpace(string(b)) == "yes"
	}
	ok := false
	switch name {
	case "github":
		// gh's auth status checks its token over the network (2 s): whether
		// it has one is enough, and its token is never kept.
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		c := exec.CommandContext(ctx, "gh", "auth", "token")
		c.WaitDelay = time.Second
		ok = c.Run() == nil
	case "linear":
		_, err := linear.Key()
		ok = err == nil
	}
	ans := "no"
	if ok {
		ans = "yes"
	}
	bestEffort(store.WriteAtomic(f, []byte(ans+"\n")))
	return ok
}

func (e *Env) authFile(name string) string { return e.StateDir + "/" + name + "-auth" }

// viewOf is view name as it runs in this session.
func (e *Env) viewOf(name string) (view, error) {
	switch name {
	case "github", "linear":
		return view{name, strings.ToUpper(name), []string{e.Self, "_items", name}, nil, os.Getenv("HOME")}, nil
	}
	return view{}, fmt.Errorf("no view %q (github or linear)", name)
}

// viewWindows are the session's view windows, by view name: window ids.
func (e *Env) viewWindows() map[string]mux.WindowID {
	out, err := e.Tmux("list-windows", "-t", "="+e.Session, "-F", "#{window_id} #{@weaver-view}")
	ws := map[string]mux.WindowID{}
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
func (e *Env) OpenView(name string) (mux.WindowID, error) {
	return e.view(name, true)
}

// startView starts view name, if it is not running, without going there.
func (e *Env) startView(name string) (mux.WindowID, error) {
	return e.view(name, false)
}

func (e *Env) view(name string, show bool) (mux.WindowID, error) {
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
	args = append(args, "-t", "="+e.Session+":"+strconv.Itoa(idx), "-n", tmux.Text(v.label), "-c", tmux.Text(v.dir), "-e", "WEAVER_TMUX_SESSION="+e.Session)
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
// set up yet with a faint dot; each a button (a tmux user range, which
// `wvr _click status` reads).
func (e *Env) SetStrip() {
	strip := "#[range=user|home]#{?#{==:#{window_index},0},#[fg=#ffffff],#[fg=#616161]}THREADS#[norange]"
	for _, n := range e.viewNames() {
		strip += "   #[range=user|" + n + "]#{?#{==:#{@weaver-view}," + n + "},#[fg=#ffffff],#[fg=#616161]}" + strings.ToUpper(n) + "#[norange]"
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

// From is `wvr _from github pr|issue REPO NUM [--review]` and `wvr _from
// linear ID [--review]` (a view's enter and Alt R): the new-thread form for
// that item, in a popup, while the view goes on. A Linear issue's thread
// works in the repository used last.
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
	var nw []string
	dir := os.Getenv("HOME")
	switch {
	case len(rest) == 4 && rest[0] == "github" && (rest[1] == "pr" || rest[1] == "issue"):
		nw = []string{"--" + rest[1], rest[2] + "#" + rest[3]}
	case len(rest) == 2 && rest[0] == "linear":
		nw = []string{"--issue", rest[1]}
		if l, err := files.FirstLine(e.StateDir + "/repos"); err == nil && l != "" && files.Exists(l) {
			dir = l
			nw = append(nw, "--repo", l)
		}
	default:
		return &cli.Exit{Code: 2, Err: errors.New("usage: wvr _from github pr|issue REPO NUM [--review] | linear ID [--review]")}
	}
	if review {
		nw = append(nw, "--review")
	}
	// The form in a popup, as Alt N opens it, while the view goes on.
	return e.formPopup(dir, append([]string{"new"}, nw...)...)
}
