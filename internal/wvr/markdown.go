package wvr

import (
	"regexp"
	"strings"

	"github.com/robbowland/weaver/internal/markdown"
)

var reHTMLComment = regexp.MustCompile(`(?s)<!--.*?-->`)

// markdownLines is how much of a description a card shows.
const markdownLines = 60

// renderMarkdown is a description rendered for a card w wide, in weaver's
// palette, at most markdownLines lines: "" when there is nothing to show.
func renderMarkdown(md string, w int) string {
	md = reHTMLComment.ReplaceAllString(md, "")
	if strings.TrimSpace(md) == "" {
		return ""
	}
	out := markdown.Render(md, w, markdown.Style{Text: CGrey, Strong: CInk, Faint: CDim, Good: CGreen, Off: COff})
	if ls := strings.Split(out, "\n"); len(ls) > markdownLines {
		out = strings.Join(ls[:markdownLines], "\n") + "\n" + CDim + "…" + COff
	}
	return out
}
