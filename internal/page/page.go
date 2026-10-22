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
	// Next is where the page after these starts (the service's cursor);
	// "" when there is none.
	Next string `json:"next,omitempty"`
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

// More is l with p, the page read after cursor after: its items l has not
// got (id names one), in their order, and where the page after it starts;
// l's total and age stay its first page's. A page that failed leaves the
// items as they were, with its error; one read after another cursor than
// l's next is not l's (a refresh came between), and leaves l as it was.
func (l List[I]) More(after string, p List[I], id func(I) string) List[I] {
	if after == "" || after != l.Next {
		return l
	}
	if p.Err != "" {
		l.Err = p.Err
		return l
	}
	seen := make(map[string]bool, len(l.Items))
	for _, it := range l.Items {
		seen[id(it)] = true
	}
	for _, it := range p.Items {
		if k := id(it); !seen[k] {
			seen[k] = true
			l.Items = append(l.Items, it)
		}
	}
	l.Next, l.Err = p.Next, ""
	if l.Total > 0 {
		l.Total = max(l.Total, len(l.Items))
	}
	return l
}
