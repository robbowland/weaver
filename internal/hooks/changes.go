package hooks

import (
	"encoding/json"
	"strings"

	"github.com/robbowland/weaver/internal/jsonx"
	"github.com/robbowland/weaver/internal/oneline"
)

// What a tool call changed, as Claude Code's PostToolUse says: Edit and
// Write name their file and what it held before (originalFile, else the
// hunks of structuredPatch), and a Bash command, with bashEditDiff on, the
// files it changed and their hunks. Undocumented, so read leniently: a
// shape weaver does not know is a call that did not say.

// Change is a file a tool call changed (an absolute path), and what it held
// before.
type Change struct {
	Path  string
	Prior Prior
}

// PriorKind is how much a call said of a file's content before it ran.
type PriorKind int

const (
	PriorUnknown PriorKind = iota // the call did not say
	PriorAbsent                   // the call created the file
	PriorText                     // Prior.Text is what it held
	PriorHunks                    // undo Prior.Hunks from what it holds now
)

// Prior is what a file held before a call.
type Prior struct {
	Kind  PriorKind
	Text  string
	Hunks []Hunk
}

// Hunk is one hunk of a unified diff: Lines are its lines with their " ",
// "-" or "+".
type Hunk struct {
	OldStart int      `json:"oldStart"`
	OldLines int      `json:"oldLines"`
	NewStart int      `json:"newStart"`
	NewLines int      `json:"newLines"`
	Lines    []string `json:"lines"`
}

// toolResponse is what weaver reads of a PostToolUse tool_response.
type toolResponse struct {
	Type            string  `json:"type"` // Write: create, update
	OriginalFile    *string `json:"originalFile"`
	StructuredPatch []Hunk  `json:"structuredPatch"`
	BashEditDiff    *struct {
		ChangedFiles []string `json:"changedFiles"`
		Files        []struct {
			FilePath string `json:"filePath"`
			Hunks    []Hunk `json:"hunks"`
		} `json:"files"`
		Unavailable bool `json:"unavailable"`
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

// Changes are the files a finished call changed, and whether it said: an
// edit tool always names its file; a Bash command only with bashEditDiff
// (Claude Code's, when it could tell; at most 200 files), and then an empty
// list means it changed none, unless the repo is shared. known false: look
// for them some other way.
func (in Input) Changes() (cs []Change, known bool) {
	if in.Event != "PostToolUse" {
		return nil, false
	}
	r := in.response()
	if f := in.EditFile(); f != "" {
		p := Prior{}
		switch {
		case r.OriginalFile != nil:
			p = Prior{Kind: PriorText, Text: *r.OriginalFile}
		case r.Type == "create":
			p = Prior{Kind: PriorAbsent}
		case len(r.StructuredPatch) > 0:
			p = Prior{Kind: PriorHunks, Hunks: r.StructuredPatch}
		}
		return []Change{{Path: f, Prior: p}}, true
	}
	d := r.BashEditDiff
	if in.ToolName != "Bash" || d == nil || d.Unavailable || d.Shared && len(d.ChangedFiles) == 0 {
		return nil, false
	}
	hunks := map[string][]Hunk{}
	for _, f := range d.Files {
		hunks[f.FilePath] = f.Hunks
	}
	for _, f := range d.ChangedFiles {
		f = oneline.Line(f)
		if !strings.HasPrefix(f, "/") {
			continue
		}
		p := Prior{}
		if hs := hunks[f]; len(hs) > 0 {
			p = Prior{Kind: PriorHunks, Hunks: hs}
			if len(hs) == 1 && hs[0].OldStart == 0 && hs[0].OldLines == 0 {
				p = Prior{Kind: PriorAbsent}
			}
		}
		cs = append(cs, Change{Path: f, Prior: p})
	}
	return cs, true
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
