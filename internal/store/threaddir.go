package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"hash/fnv"
)

// NewThreadID is the ID of a thread being made (Thread.ID): random, so no
// two threads share one, made in the same second in the same checkout
// too.
func NewThreadID() string {
	var b [8]byte
	_, _ = rand.Read(b[:]) // crypto/rand's Read never fails
	return hex.EncodeToString(b[:])
}

// Dir is the directory of thread t's own files under stateDir
// (`$STATE_DIR/threads/<created>-<id>`, docs/design.md, Files): what its
// agent is told as it starts, the review comments it has been given.
// Named for its ID, which no change to its record touches (a rename, a
// new pane, lost and resumed), so it stays the thread's for its life. A
// thread made before threads had IDs keeps the directory it was made
// with, named for its worktree's hash.
func (t Thread) Dir(stateDir string) string {
	if t.ID != "" {
		return fmt.Sprintf("%s/threads/%d-%s", stateDir, t.Created, t.ID)
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(t.Worktree)) // a hash's Write never fails
	return fmt.Sprintf("%s/threads/%d-%08x", stateDir, t.Created, h.Sum32())
}
