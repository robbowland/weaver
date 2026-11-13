// Package rex is weaver's sessions on Rex (Fleet), through Rex's own
// command, rex: its API (rex api call) and its Lua (rex do).
package rex

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Bin is the rex command: the one Rex's terminals say is theirs
// ($REX_BIN_DIR: the running server's own, whatever a login shell puts
// first on PATH), else on PATH, else the one beside the server the Rex app
// runs (an app's environment, where Rex's server runs weaver's keys, has
// neither).
func Bin() string {
	if d := os.Getenv("REX_BIN_DIR"); d != "" {
		if p := filepath.Join(d, "rex"); isExec(p) {
			return p
		}
	}
	if p, err := exec.LookPath("rex"); err == nil {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil {
		if p := filepath.Join(home, appServerRex); isExec(p) {
			return p
		}
	}
	return "rex"
}

// appServerRex is where the Rex app keeps its server's rex, under $HOME.
const appServerRex = "Library/Application Support/com.superlogical.rex/RexServer/Versions/Current/Rex Server.app/Contents/MacOS/rex"

// BinDir is the directory of the rex Bin finds; "" when it finds none.
func BinDir() string {
	if p := Bin(); filepath.IsAbs(p) {
		return filepath.Dir(p)
	}
	return ""
}

func isExec(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

// KeysLoaded: does the Rex server's configuration bind weaver's keys (one
// of weaver.lua's bindings in its keymap)? A server that read init.lua
// before weaver.lua was there has none: init.lua's require of it failed,
// and Rex reads its configuration again only when told (ReloadConfig).
// False when Rex does not say.
func KeysLoaded() bool {
	var k struct {
		Bindings []struct {
			Source string `json:"source"`
		} `json:"bindings"`
	}
	if call("client.keymap", map[string]any{}, &k) != nil {
		return false
	}
	for _, b := range k.Bindings {
		if strings.HasPrefix(b.Source, "weaver.lua:") {
			return true
		}
	}
	return false
}

// ReloadConfig has the Rex server read its configuration again, and every
// client fetch its keys again.
func ReloadConfig() error {
	if err := wireCall("server.config_reload", map[string]any{}, nil); !errors.Is(err, errNoWire) {
		return err
	}
	_, err := run("config", "reload")
	return err
}

// rexCmd is rex with args, on the server $REX_SERVER names (else Rex's
// own), never starting one.
func rexCmd(args ...string) *exec.Cmd {
	return exec.Command(Bin(), append([]string{"--autostart=false"}, args...)...)
}

// run runs rex with args: its output, or an error carrying what it said.
func run(args ...string) ([]byte, error) {
	c := rexCmd(args...)
	var stderr bytes.Buffer
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return out, errors.New("rex: " + msg)
	}
	return out, nil
}

// call calls API method with in, its result into out (when not nil): over
// the control connection (wireCall), else through the rex command.
func call(method string, in map[string]any, out any) error {
	if err := wireCall(method, in, out); !errors.Is(err, errNoWire) {
		return err
	}
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	o, err := run("api", "call", method, string(b))
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(o, out)
}

// do runs Lua script against session sid with args (rex.args), its result
// into out.
func do(sid, script string, args map[string]any, out any) error {
	a, err := json.Marshal(args)
	if err != nil {
		return err
	}
	o, err := run("do", "-s", sid, "-e", script, "--args", string(a))
	if err != nil {
		return err
	}
	return json.Unmarshal(o, out)
}
