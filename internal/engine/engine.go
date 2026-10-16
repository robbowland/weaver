// Package engine is weaver's hook engine: what each event of a coding
// agent's hooks (or another agent's, through wvr event) does to the agent's
// state file, its activity log, its turn snapshots and its nvim, in the
// hook and then in a background run.
package engine

// digits is whether s is one or more ASCII digits.
func digits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// bestEffort marks an error left unhandled on purpose: what failed (a
// marker, a log line, a stalled nvim's mark) costs nothing that matters.
func bestEffort(error) {}
