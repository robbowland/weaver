// Package engine is weaver's hook engine: what each event of Claude Code's
// hooks does to the agent's state file, in the hook and then in a
// background run.
package engine

// bestEffort marks an error left unhandled on purpose: what failed (a
// marker, a log line) costs nothing that matters.
func bestEffort(error) {}
