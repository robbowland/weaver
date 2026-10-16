package wvr

import "testing"

func TestAtLeast(t *testing.T) {
	for _, c := range []struct {
		v     string
		least []int
		want  bool
	}{{"3.5", []int{3, 3}, true}, {"3.2", []int{3, 3}, false}, {"0.74.5", []int{0, 62}, true}, {"0.61.9", []int{0, 62}, false}, {"4", []int{3, 3}, true}, {"3.3", []int{3, 3}, true}} {
		if got := atLeast(c.v, c.least...); got != c.want {
			t.Errorf("atLeast(%s, %v) = %v", c.v, c.least, got)
		}
	}
}
