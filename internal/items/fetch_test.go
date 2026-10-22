package items

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGH puts a gh on PATH that answers as GitHub does: pr view, and the
// review threads only for a query naming fields GitHub's schema has (any
// other query fails, as GraphQL's validation does). threads false: the
// threads query fails all the same.
func fakeGH(t *testing.T, threads bool) {
	t.Helper()
	d := t.TempDir()
	answer := `echo '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[` +
		`{"isResolved":false,"isOutdated":false,"path":"flag","line":1,"comments":{"nodes":[{"author":{"login":"bob"},"body":"Name it --who"}]}},` +
		`{"isResolved":true,"isOutdated":false,"path":"x","line":2,"comments":{"nodes":[{"author":{"login":"bob"},"body":"a resolved one"}]}}]}}}}}'`
	if !threads {
		answer = `echo 'gh: API rate limit exceeded' >&2; exit 1`
	}
	script := `#!/bin/sh
case "$1 $2" in
"pr view") echo '{"number":45,"title":"Add a flag","body":"Adds --name.","url":"u","author":{"login":"alice"},"state":"OPEN","headRefName":"add-flag","baseRefName":"main","isCrossRepository":false,"comments":[]}' ;;
"api graphql")
	case "$*" in
	*'{repository(owner:$o,name:$r){pullRequest(number:$n){reviewThreads('*) ` + answer + ` ;;
	*) echo "gh: Field doesn't exist on type 'Repository'" >&2; exit 1 ;;
	esac ;;
*) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(d, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", d+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// A pull request comes with its unresolved review comments; when they
// cannot be read, it says so, and so does its prompt.
func TestFetchPRReview(t *testing.T) {
	fakeGH(t, true)
	it, err := fetchPR(GitHubRef{Repo: "o/r", Num: 45})
	if err != nil {
		t.Fatal(err)
	}
	pr := it.(PullRequest)
	if len(pr.Review) != 1 || pr.Review[0].Where != "flag:1" || pr.Review[0].Body != "Name it --who" || pr.ReviewUnread != "" {
		t.Errorf("review: %+v unread %q", pr.Review, pr.ReviewUnread)
	}

	fakeGH(t, false)
	it, err = fetchPR(GitHubRef{Repo: "o/r", Num: 45})
	if err != nil {
		t.Fatal(err)
	}
	pr = it.(PullRequest)
	if len(pr.Review) != 0 || !strings.Contains(pr.ReviewUnread, "rate limit") {
		t.Errorf("unread review: %+v %q", pr.Review, pr.ReviewUnread)
	}
	p := Prompt(pr, "Address the review on #45", ModeWork, "origin/main")
	if !strings.Contains(p, "could not read its review comments") || strings.Contains(p, "## Unresolved review comments") {
		t.Errorf("prompt without its review comments:\n%s", p)
	}
}
