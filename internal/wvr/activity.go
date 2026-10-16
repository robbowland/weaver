package wvr

import (
	"bytes"
	"encoding/json"
	"os"

	"github.com/robbowland/weaver/internal/agent"
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
	logSnapshot struct {
		logHead
		Root   string `json:"root"`
		Commit string `json:"commit"`
		Point  string `json:"point"`
	}
)

// logHead is the head of a line of the activity log for event ev.
func (h *hk) logHead(ev string) logHead {
	return logHead{T: h.Now, Pane: string(h.Pane), Turn: h.Turn, Ev: ev}
}

// logLine appends line to the agent session's activity log.
func (h *hk) logLine(e *Env, line any) {
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

// nvimHead is what every event to the agent's nvim has: the event, its
// order (ms), and the agent as the event leaves it.
type nvimHead struct {
	Ev    string      `json:"ev"`
	Seq   int64       `json:"seq"`
	Pane  string      `json:"pane"`
	Agent string      `json:"agent"`
	Sid   string      `json:"sid"`
	Turn  int         `json:"turn"`
	State agent.State `json:"state"`
}

func (n nvimHead) name() string { return n.Ev }

// nvimMessage is an event to the agent's nvim.
type nvimMessage interface{ name() string }

// The nvim events, each with its own members.
type (
	nvimRoot struct {
		nvimHead
		Root string `json:"root"`
	}
	nvimEdit struct {
		nvimHead
		Root    string `json:"root"`
		File    string `json:"file"`
		Tool    string `json:"tool"`
		AgentID string `json:"agent_id,omitempty"`
	}
	nvimState struct {
		nvimHead
		Prompt  string `json:"prompt"`
		Waiting string `json:"waiting"`
		Title   string `json:"title"`
		Doing   string `json:"doing"`
	}
	nvimSnapshot struct {
		nvimHead
		Root   string `json:"root"`
		Commit string `json:"commit"`
		Point  string `json:"point"`
		Ref    string `json:"ref"`
	}
)

// nvimHead is the head of event ev to the agent's nvim.
func (h *hk) nvimHead(ev string) nvimHead {
	return nvimHead{Ev: ev, Seq: h.NowMs, Pane: string(h.Pane), Agent: h.AgentKind, Sid: h.Sid, Turn: h.Turn, State: h.State}
}
