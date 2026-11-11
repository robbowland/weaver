package hooks

import (
	"encoding/json"
	"strings"

	"github.com/robbowland/weaver/internal/jsonx"
	"github.com/robbowland/weaver/internal/oneline"
)

// What a tool call changed, as Claude Code's PostToolUse says: Edit and
// Write name their file, and a Bash command, with bashEditDiff on, the files
// it changed. Undocumented, so read leniently: a shape weaver does not know
// is a call that did not say.

// toolResponse is what weaver reads of a PostToolUse tool_response.
type toolResponse struct {
	BashEditDiff *struct {
		ChangedFiles []string `json:"changedFiles"`
		Unavailable  bool     `json:"unavailable"`
		// Set when others write in the repo too; with no files, it may have
		// held back what it saw.
		Shared bool `json:"shared"`
	} `json:"bashEditDiff"`
	// The Agent tool's.
	AgentID   string `json:"agentId"`
	AgentType string `json:"agentType"`
}

func (in Input) response() toolResponse {
	var r toolResponse
	_ = jsonx.Decode(in.ToolResponse, &r) // none, or another shape: nothing said
	return r
}

// Changes are the files (absolute paths) a finished call changed, and
// whether it said: an edit tool always names its file; a Bash command only
// with bashEditDiff (Claude Code's, when it could tell; at most 200 files),
// and then an empty list means it changed none, unless the repo is shared.
// known false: look for them some other way.
func (in Input) Changes() (paths []string, known bool) {
	if in.Event != "PostToolUse" {
		return nil, false
	}
	if f := in.EditFile(); f != "" {
		return []string{f}, true
	}
	d := in.response().BashEditDiff
	if in.ToolName != "Bash" || d == nil || d.Unavailable || d.Shared && len(d.ChangedFiles) == 0 {
		return nil, false
	}
	for _, f := range d.ChangedFiles {
		if f = oneline.Line(f); strings.HasPrefix(f, "/") {
			paths = append(paths, f)
		}
	}
	return paths, true
}

// Launched is the subagent an Agent tool call started: its id and type, and
// what it was asked to do (the call's description). "" when the call is not
// one.
func (in Input) Launched() (id, typ, desc string) {
	if in.Event != "PostToolUse" || in.ToolName != "Agent" && in.ToolName != "Task" {
		return "", "", ""
	}
	r := in.response()
	var t struct {
		Description  string `json:"description"`
		SubagentType string `json:"subagent_type"`
	}
	_ = json.Unmarshal(in.ToolInput, &t) // none: no description
	if r.AgentID == "" {
		return "", "", ""
	}
	typ = r.AgentType
	if typ == "" {
		typ = t.SubagentType
	}
	return oneline.Line(r.AgentID), typ, t.Description
}
