package wvr

import (
	"fmt"
	"io"
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
// what it found, and what fixes it.
type check struct {
	name   string
	weight weight
	ok     bool
	found  string // its version, or what is wrong
	fix    string // the command that fixes it
	brew   string // the formula that installs it
}

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

// tool checks a command: installed, and at least version least when given.
func tool(name, cmd, formula string, w weight, versionArg string, least ...int) check {
	c := check{name: name, weight: w, brew: formula, fix: "brew install " + formula}
	if !onPath(cmd) {
		c.found = "not installed"
		return c
	}
	if versionArg == "" {
		c.ok = true
		return c
	}
	v, ran := version(cmd, versionArg)
	c.found = v
	if !ran {
		c.found = "does not run"
		return c
	}
	if len(least) > 0 && !atLeast(v, least...) {
		parts := make([]string, len(least))
		for i, m := range least {
			parts[i] = strconv.Itoa(m)
		}
		c.found = v + ", weaver needs " + strings.Join(parts, ".") + " or newer"
		c.fix = "brew upgrade " + formula
		return c
	}
	c.ok = true
	return c
}

// Doctor is `wvr doctor`: what weaver works with, whether each is there
// and set up, and how to fix what is not. It fails when weaver cannot work.
func (e *Env) Doctor(out io.Writer) error {
	cs := []check{
		tool("tmux", "tmux", "tmux", needed, "-V", 3, 3),
		tool("git", "git", "git", needed, "--version"),
		tool("Neovim", "nvim", "neovim", needed, "--version"),
		tool("fzf", "fzf", "fzf", needed, "--version", 0, 62),
		tool("Worktrunk", "wt", "worktrunk", optional, ""),
	}
	// The agents: one at least, its hooks running weaver.
	bin := e.Self
	if p, err := exec.LookPath("wvr"); err == nil {
		bin = p
	}
	agents := 0
	for _, a := range []struct{ name, cmd, label string }{{"Claude Code", "claude", "claude"}, {"Codex", "codex", "codex"}} {
		c := check{name: a.name, weight: optional}
		if !onPath(a.cmd) {
			c.found = "not installed"
			cs = append(cs, c)
			continue
		}
		agents++
		c.weight, c.found, c.fix = needed, "its hooks do not run weaver", "wvr setup"
		for _, cf := range setup.Configs() {
			if cf.Label != a.label {
				continue
			}
			if ch, err := setup.Plan(cf, bin); err != nil {
				c.found = err.Error()
			} else if !ch.Changed() {
				c.ok, c.found, c.fix = true, "hooks in "+tildify(cf.File), ""
			}
		}
		cs = append(cs, c)
	}
	if agents == 0 {
		cs = append(cs, check{name: "an agent", weight: needed, found: "neither Claude Code nor Codex is installed"})
	}
	// The views, and what work items need.
	gh := tool("gh", "gh", "gh", optional, "")
	if gh.ok && !e.signedIn("gh") {
		gh.ok, gh.found, gh.fix = false, "not signed in, for the GITHUB view and --pr/--issue", "gh auth login"
	}
	cs = append(cs, gh)
	dash := check{name: "gh-dash", weight: optional, brew: "robbowland/kegs/gh-dash", fix: "brew install robbowland/kegs/gh-dash", found: "not installed, for the GITHUB view"}
	if ghDash() != nil {
		dash.ok, dash.found, dash.fix, dash.brew = true, "", "", ""
	}
	cs = append(cs, dash)
	lin := tool("linear-tui", "linear-tui", "robbowland/kegs/linear-tui", optional, "")
	if lin.ok && !e.signedIn("linear-tui") {
		lin.ok, lin.found, lin.fix = false, "not signed in, for the LINEAR view", "linear-tui auth login"
	}
	cs = append(cs, lin)
	plug := check{name: "diffview-plus.nvim", weight: optional, ok: setup.PluginsCurrent(), fix: "wvr _plugins"}
	if !plug.ok {
		plug.found = "not fetched, for reviews"
	}
	cs = append(cs, plug)
	// weaver itself: its tmux and the hooks run wvr from PATH.
	self := check{name: "wvr", weight: needed, ok: true, found: tildify(bin)}
	switch {
	case !onPath("wvr"):
		self.ok, self.found, self.fix = false, "not on your PATH, which weaver's tmux runs it from", "add "+tildify(filepath.Dir(e.Self))+" to PATH"
	case !e.installed():
		self.found += " (another build than this one)"
	}
	cs = append(cs, self)

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
	failed := false
	var brew []string
	for _, c := range cs {
		cell := paintIf(GDone, CGreen)
		switch {
		case !c.ok && c.weight == needed:
			cell, failed = paintIf(GNeed, CRed), true
		case !c.ok:
			cell = paintIf(GIdle, CGrey)
		}
		line := fmt.Sprintf("%s %-19s %s", cell, c.name, dim(c.found))
		if !c.ok && c.fix != "" {
			line += dim(" · ") + c.fix
		}
		fmt.Fprintln(out, strings.TrimRight(line, " "))
		if !c.ok && c.brew != "" && strings.HasPrefix(c.fix, "brew install") {
			brew = append(brew, c.brew)
		}
	}
	if len(brew) > 0 {
		fmt.Fprintln(out, "\n"+dim("all that is missing:")+" brew install "+strings.Join(brew, " "))
	}
	if failed {
		return cli.ErrSaid
	}
	return nil
}
