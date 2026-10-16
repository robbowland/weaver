package wvr

import "github.com/robbowland/weaver/internal/session"

// Env is the weaver session, with what the application does in it.
type Env struct{ *session.Env }

// Load is the session weaver runs in, read from its environment.
func Load() *Env { return &Env{session.Load()} }

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
// marker, a cache, a view started early) costs nothing that matters.
func bestEffort(error) {}
