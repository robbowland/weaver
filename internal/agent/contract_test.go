package agent

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// states are the states a state file can hold, in the order the design
// doc lists them (StateStarting is the board's own).
var states = []State{StateWorking, StateNeedsYou, StateDone, StateIdle, StateInterrupted, StateEnded}

// The states an agent can be in, as the Go half has them (states) and
// docs/design.md's state file table says them. A list drifting from the
// other fails here.
func TestStateContract(t *testing.T) {
	b, err := os.ReadFile("../../docs/design.md")
	if err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile("(?m)^\\| `state` \\| string \\|(.*)$").FindStringSubmatch(string(b))
	if row == nil {
		t.Fatal("no state row in docs/design.md's state file table")
	}
	var doc []string
	for _, k := range regexp.MustCompile("`([a-z-]+)`").FindAllStringSubmatch(row[1], -1) {
		doc = append(doc, k[1])
	}
	sort.Strings(doc)
	var goStates []string
	for _, s := range states {
		goStates = append(goStates, string(s))
	}
	sort.Strings(goStates)
	if strings.Join(goStates, " ") != strings.Join(doc, " ") {
		t.Errorf("the Go half's states %v are not the design doc's %v", goStates, doc)
	}
	if StateStarting.Cell() != CellWorking {
		t.Errorf("a starting thread shows as %q, not working's %q", StateStarting.Cell(), CellWorking)
	}
}
