// Package page is a list of work items as far as weaver has read it: its
// items, and how asking for them last went. Each of the views' sections is
// one (a search of internal/github's, an issue list of internal/linear's),
// kept in the view's cache from one ask to the next.
package page

import "time"

// List is a list's items as far as they have been read.
type List[I any] struct {
	// Total is how many there are in all, as the service counts them; 0
	// when it does not count them (Linear's lists).
	Total int `json:"total"`
	Items []I `json:"items"`
	// Fetched is when the items were read (zero: never); Err, why the last
	// ask failed, the items, if any, being from before it.
	Fetched time.Time `json:"fetched,omitzero"`
	Err     string    `json:"err,omitempty"`
}

// Failed is the answer to an ask that failed with err.
func Failed[I any](err error) List[I] { return List[I]{Err: err.Error()} }

// Since is l, a new answer, over before, the list as it was: l when asking
// succeeded; else before's items, as of when they were read, with l's
// error. A failure never empties a list, nor makes its items look new.
func (l List[I]) Since(before List[I]) List[I] {
	if l.Err == "" {
		return l
	}
	before.Err = l.Err
	return before
}
