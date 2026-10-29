// Package rex is weaver's session on Rex (mux.Mux), through Rex's own
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

// Bin is the rex command: on PATH, else where Rex's terminals say it is.
func Bin() string {
	if p, err := exec.LookPath("rex"); err == nil {
		return p
	}
	if d := os.Getenv("REX_BIN_DIR"); d != "" {
		return filepath.Join(d, "rex")
	}
	return "rex"
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

// call calls API method with in, its result into out (when not nil).
func call(method string, in map[string]any, out any) error {
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
