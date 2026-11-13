package rex

import (
	"encoding/json"
	"strings"
)

// Looks at sessions over the control connection (wire.go): what
// snapshotLua and fleetLua look at through the rex command, in a few
// batches of calls the server runs side by side.

// lookWire is a snapshot (as snapshotLua's) of each session of sids: their
// views, then their terminals' sizes, with full their directories and
// processes too. A terminal's method that fails (its block gone since the
// view) answers nothing, as in snapshotLua; a session gone since it was
// listed (session_not_found) is left out when gone says it may be (a
// thread's), else fails the look, as any other failed call does.
// errNoWire when there is no connection.
func lookWire(sids []string, full bool, gone func(sid string) bool) (map[string]snapshot, error) {
	reqs := make([]wireReq, len(sids))
	for i, sid := range sids {
		reqs[i] = wireReq{"session.view", map[string]any{"session_id": sid}}
	}
	res, errs, err := wireBatch(reqs)
	if err != nil {
		return nil, err
	}
	type slot struct{ sid, block, method string }
	var slots []slot
	var calls []wireReq
	out := map[string]snapshot{}
	for i, sid := range sids {
		if errs[i] != nil {
			if gone != nil && gone(sid) && strings.Contains(errs[i].Error(), "code=session_not_found") {
				continue
			}
			return nil, errs[i]
		}
		sn := snapshot{Blocks: map[string]info{}}
		if err := json.Unmarshal(res[i], &sn.View); err != nil {
			return nil, err
		}
		methods := []string{"size"}
		if full {
			methods = append(methods, "pwd", "process")
		}
		for _, w := range sn.View.Windows {
			for _, l := range w.Layers {
				if l.Kind != "tiled" {
					continue
				}
				for _, b := range l.Blocks {
					if b.Creator != terminal {
						continue
					}
					sn.Blocks[b.ID] = info{}
					for _, m := range methods {
						slots = append(slots, slot{sid, b.ID, m})
						calls = append(calls, wireReq{"block.method", map[string]any{"session_id": sid, "block_id": b.ID, "method": m, "args": map[string]any{}}})
					}
				}
			}
		}
		out[sid] = sn
	}
	if len(calls) == 0 {
		return out, nil
	}
	res, errs, err = wireBatch(calls)
	if err != nil {
		return nil, err
	}
	for j, s := range slots {
		if errs[j] != nil {
			continue
		}
		in := out[s.sid].Blocks[s.block]
		switch s.method {
		case "size":
			_ = json.Unmarshal(res[j], &in.Size)
		case "pwd":
			_ = json.Unmarshal(res[j], &in.Pwd)
		case "process":
			_ = json.Unmarshal(res[j], &in.Process)
		}
		out[s.sid].Blocks[s.block] = in
	}
	return out, nil
}

// wireReady: is there a control connection to call over (one opened now
// if need be)?
func wireReady() bool {
	wireMu.Lock()
	defer wireMu.Unlock()
	_, err := wireGet()
	return err == nil
}
