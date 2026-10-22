package page

import (
	"errors"
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
