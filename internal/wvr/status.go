package wvr

import "fmt"

// Status is `wvr status [--push]`: the counts of weaver's session
// (StatusText), from inside it or from anywhere else (toWeaverSession: a
// menu bar's, a prompt's); --push brings its boards up to date instead.
func (e *Env) Status(args []string) error {
	if err := e.toWeaverSession(); err != nil {
		return err
	}
	if arg(args, 0) == "--push" {
		e.PushStatus()
		return nil
	}
	fmt.Println(e.StatusText())
	return nil
}
