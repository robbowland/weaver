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
// works, a helper works or it waits on you, or an agent's background
// tasks or subagents run on after its turn (Esc stopped the turn, not
// them), it asks in its card (a popup, which a server with no app
// attached cannot show: the session runs on).
func TestCtrlQAsksOnlyMidTurn(t *testing.T) {
	for _, c := range []struct {
		name          string
		agent, helper agent.State
		bg, subs      int
		asks          bool
	}{
		{"all idle", agent.StateIdle, "", 0, 0, false},
		{"its agent works", agent.StateWorking, "", 0, 0, true},
		{"its agent waits on you", agent.StateNeedsYou, "", 0, 0, true},
		{"a helper works", agent.StateIdle, agent.StateWorking, 0, 0, true},
		{"its background tasks run on", agent.StateInterrupted, "", 2, 0, true},
		{"its subagents run on", agent.StateInterrupted, "", 0, 1, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, p, sid, _ := closing(t)
			store.UpdateState(e.StateFile(p), func(r *agent.Record) {
				r.State, r.Background = c.agent, c.bg
				for i := range c.subs {
					r.Subagents = append(r.Subagents, agent.Subagent{ID: "sub" + strconv.Itoa(i)})
				}
			})
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
