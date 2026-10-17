package wvr

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/robbowland/weaver/internal/cli"
	"github.com/robbowland/weaver/internal/setup"
)

// weight is how much weaver needs what a check checks.
type weight int

const (
	needed   weight = iota // weaver does not work without it
	optional               // a part of weaver does not
)

// check is one thing `wvr doctor` looks at: whether it is there and right,
// what it found, and what fixes it when it is not.
type check struct {
	name   string
	weight weight
	ok     bool
	found  string // its version, or what is wrong
	remedy remedy // nil when it is ok
}

// remedy is what fixes a check: Homebrew installing or upgrading a
// formula, a sign-in you take part in, a fix weaver makes itself, or one
// only you can make.
//
//sumtype:decl
type remedy interface{ text() string }

type (
	brewInstall struct{ formula string }
	brewUpgrade struct{ formula string }
	signIn      struct {
		view string // whose sign-in
		argv []string
	}
	selfFix struct {
		command string // how you would do it yourself
		do      func() error
	}
	byHand struct{ what string }
)

func (r brewInstall) text() string { return "brew install " + r.formula }
func (r brewUpgrade) text() string { return "brew upgrade " + r.formula }
func (r signIn) text() string      { return strings.Join(r.argv, " ") }
func (r selfFix) text() string     { return r.command }
func (r byHand) text() string      { return r.what }

var reVersion = regexp.MustCompile(`\d+(\.\d+)+`)

// version is the first version number cmd prints, and whether it ran.
func version(cmd ...string) (string, bool) {
	out, err := exec.Command(cmd[0], cmd[1:]...).Output()
	if err != nil {
		return "", false
	}
	return reVersion.FindString(string(out)), true
}

// atLeast is whether version v is at least the version least.
func atLeast(v string, least ...int) bool {
	ps := strings.Split(v, ".")
	for i, m := range least {
		n := 0
		if i < len(ps) {
			n, _ = strconv.Atoi(ps[i])
		}
		if n != m {
			return n > m
		}
	}
	return true
}

// tool checks a command Homebrew installs as formula: installed, and at
// least version least when given (asked with versionArg).
func tool(name, cmd, formula string, w weight, versionArg string, least ...int) check {
	c := check{name: name, weight: w}
	if !onPath(cmd) {
		c.found, c.remedy = "not installed", brewInstall{formula}
		return c
	}
	if versionArg == "" {
		c.ok = true
		return c
	}
	v, ran := version(cmd, versionArg)
	c.found = v
	if !ran {
		c.found, c.remedy = "does not run", brewUpgrade{formula}
		return c
	}
	if len(least) > 0 && !atLeast(v, least...) {
		parts := make([]string, len(least))
		for i, m := range least {
			parts[i] = strconv.Itoa(m)
		}
		c.found, c.remedy = v+", weaver needs "+strings.Join(parts, ".")+" or newer", brewUpgrade{formula}
		return c
	}
	c.ok = true
	return c
}

// checks are what weaver works with, each checked.
func (e *Env) checks() []check {
	cs := []check{
		tool("tmux", "tmux", "tmux", needed, "-V", 3, 3),
		tool("git", "git", "git", needed, "--version"),
		tool("Neovim", "nvim", "neovim", needed, "--version"),
		tool("fzf", "fzf", "fzf", needed, "--version", 0, 62),
		tool("Worktrunk", "wt", "worktrunk", optional, ""),
	}
	// The agents: one at least, its hooks running weaver.
	bin := e.hookBin()
	agents := 0
	for _, a := range []struct{ name, cmd string }{{"Claude Code", "claude"}, {"Codex", "codex"}} {
		c := check{name: a.name, weight: optional}
		if !onPath(a.cmd) {
			c.found = "not installed"
			cs = append(cs, c)
			continue
		}
		agents++
		c.weight, c.found = needed, "its hooks do not run weaver"
		for _, cf := range setup.Configs() {
			if cf.Label != a.cmd {
				continue
			}
			ch, err := setup.Plan(cf, bin)
			switch {
			case err != nil:
				c.found, c.remedy = err.Error(), byHand{"fix " + tildify(cf.File) + ", then wvr setup"}
			case ch.Changed():
				c.remedy = selfFix{"wvr setup", func() error { return applyHooks(ch, cf) }}
			default:
				c.ok, c.found = true, "hooks in "+tildify(cf.File)
			}
		}
		cs = append(cs, c)
	}
	if agents == 0 {
		cs = append(cs, check{name: "an agent", weight: needed, found: "neither Claude Code nor Codex is installed",
			remedy: byHand{"install Claude Code (github.com/anthropics/claude-code) or Codex (github.com/openai/codex)"}})
	}
	// The views, and what work items need.
	gh := tool("gh", "gh", "gh", optional, "")
	if gh.ok && !e.signedIn("github") {
		gh.ok, gh.found, gh.remedy = false, "not signed in, for the GITHUB view and --pr/--issue", signIn{"github", []string{"gh", "auth", "login"}}
	}
	cs = append(cs, gh)
	lin := check{name: "Linear", weight: optional, ok: e.signedIn("linear"), found: "signed in"}
	if !lin.ok {
		lin.found, lin.remedy = "no API key, for the LINEAR view and Linear issues", signIn{"linear", []string{"wvr", "linear", "login"}}
	}
	cs = append(cs, lin)
	plug := check{name: "diffview-plus.nvim", weight: optional, ok: setup.PluginsCurrent()}
	if !plug.ok {
		plug.found, plug.remedy = "not fetched, for reviews", selfFix{"wvr _plugins", setup.Plugins}
	}
	cs = append(cs, plug)
	// weaver itself: its tmux and the hooks run wvr from PATH.
	self := check{name: "wvr", weight: needed, ok: true, found: tildify(bin)}
	switch {
	case !onPath("wvr"):
		self.ok, self.found = false, "not on your PATH, which weaver's tmux runs it from"
		self.remedy = byHand{"add " + tildify(filepath.Dir(e.Self)) + " to PATH"}
	case !e.installed():
		self.found += " (another build than this one)"
	}
	return append(cs, self)
}

// hookBin is the wvr the agents' hooks run: the one on PATH, else this.
func (e *Env) hookBin() string {
	if p, err := exec.LookPath("wvr"); err == nil {
		return p
	}
	return e.Self
}

// applyHooks writes the hooks change ch to the agent's file cf.
func applyHooks(ch setup.Change, cf setup.Config) error {
	backup, err := ch.Apply(setup.Timestamp())
	if err != nil {
		return err
	}
	msg := "wvr: " + tildify(cf.File) + " runs weaver's hooks now"
	if backup != "" {
		msg += " (the old one is " + tildify(backup) + ")"
	}
	fmt.Println(msg)
	return nil
}

// Doctor is `wvr doctor`: what weaver works with, whether each is there
// and set up, and what fixes what is not (`wvr doctor --fix` does it). It
// fails when weaver cannot work.
func (e *Env) Doctor(out io.Writer) error {
	return report(out, e.checks())
}

// Fix is `wvr doctor --fix`: what can be fixed, fixed. Homebrew installs
// and upgrades what is missing or too old, weaver sets up its hooks and
// fetches its plugin, and, at a terminal, each sign-in is offered. Then
// what is left.
func (e *Env) Fix(out io.Writer) error {
	cs := e.checks()
	var install, upgrade []string
	for _, c := range cs {
		switch r := c.remedy.(type) {
		case brewInstall:
			install = append(install, r.formula)
		case brewUpgrade:
			upgrade = append(upgrade, r.formula)
		case signIn, selfFix, byHand, nil:
		}
	}
	brewed := false
	for _, b := range []struct {
		verb     string
		formulae []string
	}{{"install", install}, {"upgrade", upgrade}} {
		if len(b.formulae) == 0 {
			continue
		}
		if !onPath("brew") {
			fmt.Fprintf(out, "wvr: Homebrew (https://brew.sh) would %s %s\n", b.verb, strings.Join(b.formulae, " "))
			continue
		}
		fmt.Fprintf(out, "wvr: brew %s %s\n", b.verb, strings.Join(b.formulae, " "))
		c := exec.Command("brew", append([]string{b.verb}, b.formulae...)...)
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
		if err := c.Run(); err != nil {
			fmt.Fprintf(out, "wvr: brew %s: %v\n", b.verb, err)
		}
		brewed = true
	}
	if brewed {
		// What Homebrew brought may want fixing too.
		cs = e.checks()
	}
	for _, c := range cs {
		if r, ok := c.remedy.(selfFix); ok {
			if err := r.do(); err != nil {
				fmt.Fprintf(out, "wvr: %s: %v\n", c.name, err)
			}
		}
	}
	if cli.IsTTY(0) && cli.IsTTY(1) {
		for _, c := range cs {
			r, ok := c.remedy.(signIn)
			if !ok {
				continue
			}
			fmt.Fprintf(out, "\n%s is %s. Sign in now (%s)?\n", c.name, c.found, r.text())
			t, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
			if err != nil {
				continue
			}
			yes := choose(t, "", []choice{{"y", "sign in"}, {"n", "not now"}}, 0) == 0
			_ = t.Close()
			if !yes {
				continue
			}
			argv := append([]string{}, r.argv...)
			if argv[0] == "wvr" {
				argv[0] = e.Self
			}
			s := exec.Command(argv[0], argv[1:]...)
			s.Stdin, s.Stdout, s.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := s.Run(); err != nil {
				fmt.Fprintf(out, "wvr: %s: %v\n", r.text(), err)
			}
			os.Remove(e.authFile(r.view))
		}
	}
	fmt.Fprintln(out)
	return e.Doctor(out)
}

// report prints the checks, and fails when weaver cannot work.
func report(out io.Writer, cs []check) error {
	colour := cli.IsTTY(1)
	paintIf := func(cell, c string) string {
		if !colour {
			return cell
		}
		return paint(cell, c, CDim, COff)
	}
	dim := func(s string) string {
		if !colour || s == "" {
			return s
		}
		return CGrey + s + COff
	}
	failed, fixable := false, false
	for _, c := range cs {
		cell := paintIf(GDone, CGreen)
		switch {
		case !c.ok && c.weight == needed:
			cell, failed = paintIf(GNeed, CRed), true
		case !c.ok:
			cell = paintIf(GIdle, CGrey)
		}
		line := fmt.Sprintf("%s %-19s %s", cell, c.name, dim(c.found))
		if !c.ok && c.remedy != nil {
			line += dim(" · ") + c.remedy.text()
			switch c.remedy.(type) {
			case brewInstall, brewUpgrade, selfFix, signIn:
				fixable = true
			case byHand:
			}
		}
		fmt.Fprintln(out, strings.TrimRight(line, " "))
	}
	if fixable {
		fmt.Fprintln(out, "\n"+dim("wvr doctor --fix does what it can of that"))
	}
	if failed {
		return cli.ErrSaid
	}
	return nil
}
