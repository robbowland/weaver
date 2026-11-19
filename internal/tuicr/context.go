package tuicr

import (
	"fmt"
	"strings"
)

// contextMax caps the comments one prompt carries: Claude Code takes a
// hook's additionalContext up to 10,000 characters.
const contextMax = 9000

// Context is cs as the context a prompt carries to the agent of a thread
// on worktree: marked as its user's review comments, fenced, each its
// location then its text, as many as fit (n of them); the rest come with
// the next prompt.
func Context(cs []Comment, worktree string) (text string, n int) {
	var body strings.Builder
	for _, c := range cs {
		head := c.Location
		if c.Type != "" {
			head += " (" + c.Type + ")"
		}
		e := head + "\n" + strings.TrimSpace(c.Text) + "\n\n"
		if n > 0 && body.Len()+len(e) > contextMax {
			break
		}
		body.WriteString(e)
		n++
	}
	if n == 0 {
		return "", 0
	}
	s := strings.TrimRight(body.String(), "\n")
	if len(s) > contextMax {
		s = s[:contextMax] + "\n(cut short)"
	}
	// A fence longer than any run of backticks in the comments.
	fence := "```"
	for strings.Contains(s, fence) {
		fence += "`"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Your user's review comments, written in tuicr (weaver's review panel) on this thread's worktree "+
		"since its agent was last given them. Each is its location (path:line, the file, or \"review\" for the whole review), "+
		"then the comment. To read all of them again: `tuicr review list --repo %s`, then `tuicr review comments --session <slug>`.\n\n", worktree)
	b.WriteString(fence + "review-comments\n" + s + "\n" + fence + "\n")
	if left := len(cs) - n; left > 0 {
		fmt.Fprintf(&b, "%d more come with the next prompt.\n", left)
	}
	return b.String(), n
}
