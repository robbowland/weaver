package session

import (
	"testing"

	"github.com/robbowland/weaver/internal/agent"
)

// A helper (no thread, its pane in a thread's session) is its thread's,
// and the helper that has needed you longest rolls into its thread as
// every list shows it: a thread done or exited needs you, saying which
// helper waits on what. A helper merely done rolls nothing in.
func TestRollUp(t *testing.T) {
	rows := []Row{
		{Pane: "1", Session: "session:t", Tnum: 1, State: agent.StateDone, Title: "greet"},
		{Pane: "2", Session: "session:t", WindowName: "lint", State: agent.StateNeedsYou, Waiting: "Bash: lint", Since: 9},
		{Pane: "3", Session: "session:t", WindowName: "tests", State: agent.StateNeedsYou, Waiting: "Bash: go test", Since: 5},
		{Pane: "4", Session: "session:u", Tnum: 2, State: agent.StateIdle, Exited: true},
		{Pane: "5", Session: "session:u", State: agent.StateNeedsYou, Since: 3},
		{Pane: "6", Session: "session:v", Tnum: 3, State: agent.StateWorking},
		{Pane: "7", Session: "session:v", State: agent.StateDone},
		{Pane: "8", Session: "session:home", State: agent.StateNeedsYou},
	}
	rollUp(rows)
	for i, want := range []int{1, 1, 1, 2, 2, 3, 3, 0} {
		if rows[i].Thread != want || rows[i].Helper() != (rows[i].Tnum == 0 && want > 0) {
			t.Errorf("row %s: thread %d (helper %v), want %d", rows[i].Pane, rows[i].Thread, rows[i].Helper(), want)
		}
	}
	if s := rows[0].Shown(); s.State != agent.StateNeedsYou || s.Waiting != "tests: Bash: go test" || s.Since != 5 || rows[0].Via.Pane != "3" {
		t.Errorf("thread 1, two helpers waiting: %+v, want the longest-waiting's", s)
	}
	if s := rows[3].Shown(); s.State != agent.StateNeedsYou || s.Exited || s.Since != 3 {
		t.Errorf("thread 2, exited, its helper waiting: %+v", s)
	}
	if s := rows[5].Shown(); s.State != agent.StateWorking || rows[5].Via != nil {
		t.Errorf("thread 3, its helper done: %+v", s)
	}
	if rows[0].State != agent.StateDone {
		t.Errorf("the thread's own row changed: %+v", rows[0])
	}
}
