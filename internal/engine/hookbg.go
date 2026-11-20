package engine

import (
	"encoding/json"
	"os"
	"os/exec"

	"github.com/robbowland/weaver/internal/session"
)

// hookBg is `wvr hook --bg`: everything a hook does besides writing the
// state, after it, so the agent never waits on Rex, git or Worktrunk.
func hookBg(e *session.Env) {
	h := &hk{}
	if json.Unmarshal([]byte(os.Getenv("WEAVER_BG")), h) != nil {
		return
	}
	bestEffort(os.Unsetenv("WEAVER_BG"))
	h.actOn(e)
	h.sf = e.StateFile(h.Pane)
	// A thread's label is its title (else its task or prompt) and branch;
	// a command may have relabelled its session (Rex's SessionStart hook
	// asks every agent to name the session it is in, and a helper's is its
	// thread's), which the labels then set back.
	switch {
	case h.Event == "UserPromptSubmit" || h.Event == "SessionStart" || h.Title != h.sess().Title || h.Root != h.Old.Root || h.ranCommand():
		e.PushStatusAndLabels()
	case h.State != h.Old.State || h.Doing != h.sess().Doing || h.Waiting != h.Old.Waiting:
		e.PushStatus()
	}
	if h.MarkDir != h.Old.MarkedRoot || h.MarkDir != "" && h.Marker != h.Old.Marker {
		if h.Old.MarkedRoot != "" && h.Old.MarkedRoot != h.MarkDir {
			wtMarker(h.Old.MarkedRoot, "")
		}
		if h.MarkDir != "" {
			wtMarker(h.MarkDir, h.Marker)
		}
	}
	if h.Event == "SessionStart" {
		e.GC()
	}
}

// ranCommand: is the event a Bash command's end?
func (h *hk) ranCommand() bool {
	return h.Tool == "Bash" && (h.Event == "PostToolUse" || h.Event == "PostToolUseFailure")
}

// actOn has e act on the session the hook acted on, not the one its
// environment names (a background run's may be another session's). A run
// started by an older weaver says none: its environment's, as then.
func (h *hk) actOn(e *session.Env) {
	if h.Session != "" {
		e.Session, e.SKey, e.RexID, e.RexHere = h.Session, h.SKey, h.RexID, h.RexHere
	}
}

// wtMarker sets or clears a Worktrunk branch marker in dir.
func wtMarker(dir, m string) {
	args := []string{"-C", dir, "config", "state", "marker", "clear"}
	if m != "" {
		args = []string{"-C", dir, "config", "state", "marker", "set", m}
	}
	bestEffort(exec.Command("wt", args...).Run()) // a branch's mark, nothing more
}
