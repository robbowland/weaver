package store

import (
	"fmt"
	"hash/fnv"
)

// Dir is the directory of thread t's own files under stateDir
// (`$STATE_DIR/threads/<created>-<worktree's hash>`, docs/design.md,
// Files): what its agent is told as it starts, the review comments it has
// been given. Named for when the thread was made and the worktree it runs
// in, which no change to its record touches (a rename, a new pane, lost
// and resumed), so it stays the thread's for its life.
func (t Thread) Dir(stateDir string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(t.Worktree)) // a hash's Write never fails
	return fmt.Sprintf("%s/threads/%d-%08x", stateDir, t.Created, h.Sum32())
}
