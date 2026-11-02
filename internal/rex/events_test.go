package rex

import "testing"

// What `rex events --session S --json` writes (Rex build 1696): a session's
// own event inside session_event; every API call's client comes and goes.
func TestIsActiveChange(t *testing.T) {
	for _, c := range []struct {
		line string
		want bool
	}{
		{`{"type":"session_event","payload":{"session_id":"session:1","event":{"type":"active_window_changed","payload":{"window_id":"window:2"}}}}`, true},
		{`{"type":"session_event","payload":{"session_id":"session:1","event":{"type":"client_connected","payload":{"client_id":"client:3"}}}}`, false},
		{`{"type":"session_event","payload":{"session_id":"session:1","event":{"type":"session_view_changed","payload":{"revision":63}}}}`, false},
		{`{"type":"active_window_changed"}`, false},
		{`not json`, false},
	} {
		if got := isActiveChange([]byte(c.line)); got != c.want {
			t.Errorf("isActiveChange(%s) = %v, want %v", c.line, got, c.want)
		}
	}
}
