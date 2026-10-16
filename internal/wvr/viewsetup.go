package wvr

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// viewAbout is what a view shows, for its setup screen.
var viewAbout = map[string]string{
	"github": "your pull requests and issues, to start threads from",
	"linear": "your Linear issues, to start threads from",
}

// ViewSetup is `wvr _viewsetup NAME`, a view's window while the view is not
// ready: what it needs, and a key that does it (an install, a sign-in); once
// it is ready, the window becomes the view.
func (e *Env) ViewSetup(name string) error {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return errors.New("the view's setup needs a terminal")
	}
	defer func() { _ = tty.Close() }()
	said := ""
	for {
		need := e.viewNeeds(name)
		if need.state == viewReady {
			return e.becomeView(name)
		}
		pad := placeCard(tty, 8)
		fmt.Fprint(tty, cardRow(pad, "view", CBold+strings.ToUpper(name)+COff+" "+CDim+"·"+COff+" "+CGrey+viewAbout[name]+COff))
		fmt.Fprint(tty, cardRow(pad, "needs", need.what))
		keys := []string{}
		if need.fix != nil {
			doing := "install"
			if need.state == viewSignedOut {
				doing = "sign in"
			}
			fmt.Fprint(tty, cardRow(pad, "runs", CGrey+strings.Join(need.fix, " ")+COff))
			keys = append(keys, "⏎", doing)
		} else {
			fmt.Fprint(tty, cardRow(pad, "", CGrey+"install it, then come back here"+COff))
		}
		if said != "" {
			fmt.Fprint(tty, cardRow(pad, "", CRed+said+COff))
		}
		keys = append(keys, "h", "hide this view", "esc", "home")
		fmt.Fprint(tty, "\n"+cardKeys(pad, keys...))
		said = ""
		switch k := readKey(); {
		case (k == '\r' || k == '\n') && need.fix != nil:
			fmt.Fprint(tty, "\033[H\033[2J")
			argv := append([]string{}, need.fix...)
			if argv[0] == "wvr" {
				argv[0] = e.Self
			}
			c := exec.Command(argv[0], argv[1:]...)
			c.Stdin, c.Stdout, c.Stderr = tty, tty, tty
			if err := c.Run(); err != nil {
				said = strings.Join(need.fix, " ") + ": " + err.Error()
			}
			// Asked again now, not in a minute.
			os.Remove(e.authFile(name))
			e.SetStrip()
		case k == 'h' || k == 'H':
			e.setHidden(name, true)
			e.SetStrip()
			e.Tmux("select-window", "-t", "="+e.Session+":0")
			return nil
		case k == 0x1b || k == 'q':
			e.Tmux("select-window", "-t", "="+e.Session+":0")
		}
	}
}

// becomeView runs view name in this window, in place of its setup.
func (e *Env) becomeView(name string) error {
	v, err := e.viewOf(name)
	if err != nil {
		return err
	}
	e.SetStrip()
	bestEffort(os.Chdir(v.dir))
	p, err := exec.LookPath(v.argv[0])
	if err != nil {
		return err
	}
	return fmt.Errorf("%s: %w", v.argv[0], syscall.Exec(p, v.argv, append(os.Environ(), v.env...)))
}
