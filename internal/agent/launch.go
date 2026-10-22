package agent

import "strings"

// Mode is how a thread's agent may act on its work.
type Mode string

const (
	// ModeWork changes what it works on.
	ModeWork Mode = ""
	// ModeReview only reads: Claude Code in plan mode, Codex in a read-only
	// sandbox.
	ModeReview Mode = "review"
)

// Command is the command line that runs kind k's agent from base (the
// command its thread was given, WEAVER_CLAUDE's say) held to mode m. A
// review is read-only whatever base says: what would loosen it goes, and
// the agent's own read-only mode is added. The one place a thread's agent
// gets its mode, so every start of it (new, resumed, restarted) keeps it.
func Command(k Kind, base string, m Mode) []string {
	w := strings.Fields(base)
	if m != ModeReview || len(w) == 0 {
		return w
	}
	// Each loosening option: its name, and whether a value follows it
	// (as the next word, unless given as name=value). Claude Code's, unless
	// it is Codex.
	loose := map[string]bool{"--permission-mode": true, "--dangerously-skip-permissions": false,
		"--allow-dangerously-skip-permissions": false}
	hold := []string{"--permission-mode", "plan"}
	switch k {
	case Codex:
		loose = map[string]bool{"--sandbox": true, "-s": true, "--full-auto": false,
			"--dangerously-bypass-approvals-and-sandbox": false, "--yolo": false}
		hold = []string{"--sandbox", "read-only"}
	case Claude:
	}
	out := []string{w[0]}
	for i := 1; i < len(w); i++ {
		name, _, hasValue := strings.Cut(w[i], "=")
		takes, ok := loose[name]
		if !ok {
			out = append(out, w[i])
			continue
		}
		if takes && !hasValue {
			i++
		}
	}
	return append(out, hold...)
}
