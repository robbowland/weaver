package wvr

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
	// errSaid ends a command that has said why it failed.
	errSaid = &Exit{Code: 1}
	// errCancelled: you cancelled it (Esc in a form).
	errCancelled = &Exit{Code: 130}
)
