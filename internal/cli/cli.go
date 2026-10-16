// Package cli is what weaver's commands share: their environment and
// terminal, and how they end (an Exit with its status, the usage asked
// for, or nothing).
package cli

import (
	"errors"
	"os"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

// Exit ends a command with status Code. Err, when set, is what main says,
// as of any other error; nil when the command has said why already, or
// there is nothing to say.
type Exit struct {
	Code int
	Err  error
}

func (x *Exit) Error() string {
	if x.Err == nil {
		return "exit status " + strconv.Itoa(x.Code)
	}
	return x.Err.Error()
}

func (x *Exit) Unwrap() error { return x.Err }

// ErrHelp asks for the usage: main prints it.
var ErrHelp = errors.New("help")

var (
	// ErrSaid ends a command that has said why it failed.
	ErrSaid = &Exit{Code: 1}
	// ErrCancelled: you cancelled it (Esc in a form).
	ErrCancelled = &Exit{Code: 130}
)

// IsTTY is whether fd is a terminal.
func IsTTY(fd int) bool {
	_, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	return err == nil
}

// EnvInt is $k as a whole number, if it is one.
func EnvInt(k string) (int, bool) {
	v := os.Getenv(k)
	if !digits(v) {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	return n, err == nil
}

// EnvSeconds is $k as a whole number of seconds, else d.
func EnvSeconds(k string, d time.Duration) time.Duration {
	if n, ok := EnvInt(k); ok {
		return time.Duration(n) * time.Second
	}
	return d
}

func digits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
