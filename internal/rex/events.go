package rex

import (
	"bufio"
	"encoding/json"
	"errors"
)

// WatchActive follows the session's events (`rex events --session ID
// --json`) and calls changed each time its active window changes: the app's
// own tab changes too, as the app writes what it shows back to the server.
// Rex has no focus hook; this is what stands for one. It
// returns when the stream ends (the session or the server gone), with what
// ended it.
func (s Session) WatchActive(changed func()) error {
	c := rexCmd("events", "--session", s.ID, "--json")
	out, err := c.StdoutPipe()
	if err != nil {
		return err
	}
	if err := c.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		if isActiveChange(sc.Bytes()) {
			changed()
		}
	}
	return errors.Join(sc.Err(), c.Wait())
}

// isActiveChange: is line (one of `rex events --json`'s) a session's
// active_window_changed?
func isActiveChange(line []byte) bool {
	var ev struct {
		Type    string `json:"type"`
		Payload struct {
			Event struct {
				Type string `json:"type"`
			} `json:"event"`
		} `json:"payload"`
	}
	return json.Unmarshal(line, &ev) == nil && ev.Type == "session_event" &&
		ev.Payload.Event.Type == "active_window_changed"
}
