package agent

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestParseWhere(t *testing.T) {
	for in, want := range map[string]Where{
		"/private/tmp/tmux-501/weaver,84824,3": {Server: 84824, Session: "$3"},
		"/tmp/tmux-501/default,1,0":            {Server: 1, Session: "$0"},
		"":                                     {},
		"/tmp/x,abc,1":                         {},
		"/tmp/x,12":                            {},
	} {
		if got := ParseWhere(in); got != want {
			t.Errorf("ParseWhere(%q) = %+v, want %+v", in, got, want)
		}
	}
}

// What a resumed agent starts from: every field of a record is either the
// conversation's (carried on) or its process's and pane's (gone), and a new
// field must say which here, so none rides along by accident (as server
// once did, sending a resumed thread back to lost when its pane closed).
func TestResumed(t *testing.T) {
	carried := map[string]bool{"Agent": true, "SessionID": true, "Title": true, "Turn": true, "TurnEnded": true,
		"PromptID": true, "Transcript": true, "Cwd": true, "Root": true, "StartCwd": true, "ProjectRoot": true,
		"File": true, "Prompt": true, "LastMessage": true, "Log": true, "Tasks": true, "TurnRoots": true, "Files": true}
	renewed := map[string]bool{"State": true, "TS": true, "Since": true, "Seen": true, "Where": true, "Subagents": true}
	gone := map[string]bool{"Pane": true, "Pid": true, "ProcStart": true, "Waiting": true, "Pending": true,
		"MarkedRoot": true, "Marker": true, "Background": true, "BackgroundTasks": true, "Doing": true,
		"DoingID": true, "DoneIDs": true, "NvimPane": true, "EndReason": true}
	nv := json.Number("4")
	full := Record{Pane: 3, Agent: "claude", State: StateWorking, TS: 1, Since: "1", Seen: false, SessionID: "s",
		Pid: "9", ProcStart: "p", Title: "t", Turn: 4, TurnEnded: 3, PromptID: "q", Transcript: "tr", Cwd: "c",
		Root: "r", StartCwd: "sc", ProjectRoot: "pr", File: "f", Prompt: "pp", Waiting: "w", Pending: "pe",
		LastMessage: "lm", MarkedRoot: "mr", Marker: "m", Background: 2, BackgroundTasks: "b", Doing: "d",
		DoingID: "di", DoneIDs: "dd", NvimPane: &nv, Where: Where{Server: 1, Session: "$1"}, EndReason: "e", Log: "l",
		Subagents: []Subagent{{ID: "a"}}, Tasks: []Task{{ID: "t"}}, TurnRoots: []string{"r"}, Files: []string{"f"}}
	w := Where{Server: 7, Session: "$2"}
	got := full.Resumed(100, w)
	rt, gv, fv := reflect.TypeOf(got), reflect.ValueOf(got), reflect.ValueOf(full)
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		switch {
		case carried[name]:
			if !reflect.DeepEqual(gv.Field(i).Interface(), fv.Field(i).Interface()) {
				t.Errorf("%s not carried on: %v", name, gv.Field(i).Interface())
			}
		case gone[name]:
			if !gv.Field(i).IsZero() {
				t.Errorf("%s carried from the old process: %v", name, gv.Field(i).Interface())
			}
		case !renewed[name]:
			t.Errorf("field %s: carried on a resume, or gone? Say so in Resumed and here", name)
		}
	}
	if got.State != StateIdle || got.TS != 100 || got.Since != "100" || !got.Seen || got.Where != w || len(got.Subagents) != 0 {
		t.Errorf("renewed: %+v", got)
	}
}
