// Package nvim asks weaver's Neovims something over their RPC sockets,
// bounded in time: an nvim busy with a dialog must not hold up a hook.
package nvim

import (
	"context"
	"time"

	client "github.com/neovim/go-client/nvim"
)

// Reply is how an nvim took a request.
type Reply int

const (
	// Answered: it evaluated the expression.
	Answered Reply = iota
	// Absent: nothing listens on the socket, or the request failed.
	Absent
	// Stalled: it did not answer within the time given.
	Stalled
)

func dial(ctx context.Context, sock string) (*client.Nvim, error) {
	return client.Dial(sock, client.DialContext(ctx), client.DialLogf(func(string, ...any) {}))
}

// Eval evaluates expr in the nvim at sock, waiting at most d.
func Eval(sock, expr string, d time.Duration) Reply {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	v, err := dial(ctx, sock)
	if err != nil {
		if ctx.Err() != nil {
			return Stalled
		}
		return Absent
	}
	defer func() { _ = v.Close() }() // the answer, if any, is in
	done := make(chan error, 1)
	go func() {
		var r any
		done <- v.Eval(expr, &r)
	}()
	select {
	case err := <-done:
		if err != nil {
			return Absent
		}
		return Answered
	case <-ctx.Done():
		return Stalled
	}
}

// EvalInt is expr's value in the nvim at sock as a number, waiting at most
// d; false when it gave none.
func EvalInt(sock, expr string, d time.Duration) (int, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	v, err := dial(ctx, sock)
	if err != nil {
		return 0, false
	}
	defer func() { _ = v.Close() }() // the answer, if any, is in
	done := make(chan int, 1)
	go func() {
		var r int
		if v.Eval(expr, &r) != nil {
			r = -1
		}
		done <- r
	}()
	select {
	case r := <-done:
		return r, r >= 0
	case <-ctx.Done():
		return 0, false
	}
}
