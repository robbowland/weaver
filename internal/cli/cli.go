// Package cli is how a weaver command ends: an Exit with its status, the
// usage asked for, or nothing.
package cli

import (
	"errors"
	"strconv"
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
