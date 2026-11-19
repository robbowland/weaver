package wvr

import (
	"strconv"
	"testing"
	"time"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/jsonx"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/store"
)

// Ctrl q quits at once when quitting would cost nothing; while an agent
// works, a helper works or it waits on you, it asks in its card (a popup,
// which a server with no app attached cannot show: the session runs on).
func TestCtrlQAsksOnlyMidTurn(t *testing.T) {
	for _, c := range []struct {
		name          string
		agent, helper agent.State
		asks          bool
	}{
		{"all idle", agent.StateIdle, "", false},
		{"its agent works", agent.StateWorking, "", true},
		{"its agent waits on you", agent.StateNeedsYou, "", true},
		{"a helper works", agent.StateIdle, agent.StateWorking, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, p, sid, _ := closing(t)
			store.UpdateState(e.StateFile(p), func(r *agent.Record) { r.State = c.agent })
			if c.helper != "" {
				h, err := e.Mux().SplitLeft(p, mux.NewSplit{Spawn: mux.Spawn{Argv: []string{"/bin/sleep", "600"}, Dir: "/"}, Background: true})
				if err != nil {
					t.Fatal(err)
				}
				n, _ := strconv.Atoi(string(h))
				if err := store.Write(e.StateFile(h), agent.Record{Pane: jsonx.Int(n), Agent: agent.Claude, State: c.helper, TS: time.Now().Unix(),
					SessionID: "helper", Where: agent.Where{Server: e.Mux().Home.Life().Server, Session: sid}}); err != nil {
					t.Fatal(err)
				}
			}
			err := e.Popup([]string{"quit"})
			if running := e.Mux().Running(); running != c.asks {
				t.Errorf("Popup quit returned %v, the session running %v; want it running %v", err, running, c.asks)
			}
		})
	}
}
