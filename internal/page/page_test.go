package page

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// A new answer replaces the list; a failed one keeps its items, as of when
// they were read, and says why.
func TestSince(t *testing.T) {
	then := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	before := List[string]{Total: 2, Items: []string{"a", "b"}, Fetched: then}
	now := List[string]{Total: 1, Items: []string{"c"}, Fetched: then.Add(time.Hour)}
	if got := now.Since(before); len(got.Items) != 1 || got.Items[0] != "c" || !got.Fetched.Equal(now.Fetched) || got.Err != "" {
		t.Errorf("an answer: %+v", got)
	}
	got := Failed[string](errors.New("gh: HTTP 502")).Since(before)
	if len(got.Items) != 2 || got.Total != 2 || !got.Fetched.Equal(then) || got.Err != "gh: HTTP 502" {
		t.Errorf("a failure: %+v", got)
	}
	if got := Failed[string](errors.New("no")).Since(List[string]{}); len(got.Items) != 0 || !got.Fetched.IsZero() || got.Err != "no" {
		t.Errorf("a failure, nothing before: %+v", got)
	}
}

// A page after a list adds what the list has not got (an item that moved
// up between the pages comes once), and says where the next starts; one
// that failed, or was read for another cursor (a refresh came between),
// adds nothing.
func TestMore(t *testing.T) {
	then := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	id := func(s string) string { return s }
	l := List[string]{Total: 5, Items: []string{"a", "b"}, Next: "c2", Fetched: then}
	got := l.More("c2", List[string]{Total: 6, Items: []string{"b", "c", "d"}, Next: "c4", Fetched: then.Add(time.Hour)}, id)
	if strings.Join(got.Items, " ") != "a b c d" || got.Next != "c4" || got.Total != 5 || !got.Fetched.Equal(then) {
		t.Errorf("a page: %+v", got)
	}
	if got := got.More("c4", List[string]{Items: []string{"e"}}, id); got.Next != "" || len(got.Items) != 5 {
		t.Errorf("the last page: %+v", got)
	}
	if got := l.More("c2", Failed[string](errors.New("gh: timeout")), id); len(got.Items) != 2 || got.Next != "c2" || got.Err != "gh: timeout" {
		t.Errorf("a page that failed: %+v", got)
	}
	if got := l.More("c9", List[string]{Items: []string{"x"}}, id); len(got.Items) != 2 || got.Next != "c2" {
		t.Errorf("a page of another cursor: %+v", got)
	}
	// A list of no count stays so.
	if got := (List[string]{Items: []string{"a"}, Next: "c"}).More("c", List[string]{Items: []string{"b"}}, id); got.Total != 0 {
		t.Errorf("counted: %+v", got)
	}
}
