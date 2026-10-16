package wvr

// Where is where a thread works: a new worktree, or the checkout you are
// in.
type Where string

const (
	WhereWorktree Where = "worktree"
	WhereHere     Where = "here"
)
