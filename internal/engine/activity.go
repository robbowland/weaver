package engine

import (
	"bytes"
	"encoding/json"
	"os"

	"github.com/robbowland/weaver/internal/agent"
	"github.com/robbowland/weaver/internal/session"
)

// What the hook tells beyond the state file: the agent session's activity
// log, and the events its nvim follows (docs/design.md).

// logHead is what every line of the activity log has: when, the agent's
// pane and turn, and the event.
type logHead struct {
	T    int64  `json:"t"`
	Pane string `json:"pane"`
	Turn int    `json:"turn"`
	Ev   string `json:"ev"`
}

// The activity log's events, each with its own members.
type (
	logSession struct {
		logHead
		Source string `json:"source"`
	}
	logPrompt struct {
		logHead
		Text     string `json:"text"`
		PromptID string `json:"prompt_id"`
	}
	logEdit struct {
		logHead
		File    string `json:"file"`
		Root    string `json:"root"`
		Tool    string `json:"tool"`
		AgentID string `json:"agent_id,omitempty"`
	}
	logBash struct {
		logHead
		Cmd  string `json:"cmd"`
		File string `json:"file,omitempty"`
	}
	logInterrupt struct {
		logHead
		Tool string `json:"tool"`
	}
	logSubagent struct {
		logHead
		AgentID   string `json:"agent_id"`
		AgentType string `json:"agent_type"`
		Phase     string `json:"phase"`
	}
	logStop struct {
		logHead
		Last string `json:"last"`
	}
	logError struct {
		logHead
		Error string `json:"error"`
	}
	logCwd struct {
		logHead
		Cwd  string `json:"cwd"`
		Root string `json:"root"`
	}
	logNeedsYou struct {
		logHead
		Waiting string `json:"waiting"`
	}
	logState struct {
		logHead
		State agent.State `json:"state"`
	}
	logAgent struct {
		logHead
		AgentID   string `json:"agent_id"`
		AgentType string `json:"agent_type"`
		Desc      string `json:"desc"`
	}
)

// logHead is the head of a line of the activity log for event ev.
func (h *hk) logHead(ev string) logHead {
	return logHead{T: h.Now, Pane: string(h.Pane), Turn: h.Turn, Ev: ev}
}

// logLine appends line to the agent session's activity log.
func (h *hk) logLine(e *session.Env, line any) {
	if h.Sid == "" {
		return
	}
	b, err := jsonLine(line)
	if err != nil {
		return
	}
	f, err := os.OpenFile(e.LogDir+"/"+h.Sid+".jsonl", os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return
	}
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	bestEffort(err) // the log is the agent's story, not its state
}

// jsonLine is v as one line of JSON, HTML left as it is.
func jsonLine(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	err := enc.Encode(v)
	return b.Bytes(), err
}
