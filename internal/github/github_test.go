package github

import (
	"strings"
	"testing"
)

func TestQuery(t *testing.T) {
	q := Query(PRs, 0)
	for _, want := range []string{`query: "is:pr archived:false is:open review-requested:@me`, "...pr", "fragment pr on PullRequest", "statusCheckRollup"} {
		if !strings.Contains(q, want) {
			t.Errorf("PR query lacks %q", want)
		}
	}
	if q := Query(Issues, 1); !strings.Contains(q, `"is:issue archived:false is:open author:@me`) || !strings.Contains(q, "fragment issue on Issue") {
		t.Errorf("issue query: %s", q)
	}
}

func TestParse(t *testing.T) {
	answer := `{"data":{"s":{"issueCount":55,"nodes":[
	  {"number":45,"title":"Add a flag","body":"Adds --name.","url":"https://github.com/o/r/pull/45","state":"OPEN","isDraft":false,
	   "author":{"login":"alice"},"updatedAt":"2026-10-16T10:00:00Z","repository":{"nameWithOwner":"o/r"},
	   "headRefName":"add-flag","baseRefName":"main","isCrossRepository":false,"reviewDecision":"CHANGES_REQUESTED","mergeable":"MERGEABLE",
	   "additions":130,"deletions":13,"changedFiles":6,"commits":{"totalCount":2},"comments":{"totalCount":3},
	   "labels":{"nodes":[{"name":"bug"}]},"assignees":{"nodes":[]},
	   "reviewRequests":{"nodes":[{"requestedReviewer":{"__typename":"Team","slug":"back-end"}},{"requestedReviewer":{"__typename":"User","login":"bob"}}]},
	   "last":{"nodes":[{"commit":{"statusCheckRollup":{"state":"FAILURE","contexts":{"nodes":[
	     {"__typename":"CheckRun","name":"lint","status":"COMPLETED","conclusion":"FAILURE"},
	     {"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"SUCCESS"},
	     {"__typename":"CheckRun","name":"e2e","status":"IN_PROGRESS","conclusion":null},
	     {"__typename":"StatusContext","context":"ci/legacy","state":"SUCCESS"}]}}}}]}},
	  {}
	]}}}`
	r, err := Parse(PRs, 0, []byte(answer))
	if err != nil {
		t.Fatal(err)
	}
	if r.Title != "Queue" || r.Total != 55 || len(r.Items) != 1 {
		t.Fatalf("%+v", r)
	}
	it := r.Items[0]
	if it.Ref() != "o/r#45" || it.Head != "add-flag" || it.Review != "CHANGES_REQUESTED" || it.Files != 6 || it.Commits != 2 {
		t.Errorf("%+v", it)
	}
	if strings.Join(it.Requested, " ") != "back-end @bob" {
		t.Errorf("requested %v", it.Requested)
	}
	if it.Checks != "FAILURE" || strings.Join(it.FailingChecks, " ") != "lint" || it.PassingChecks != 2 || it.RunningChecks != 1 {
		t.Errorf("checks %s %v %d %d", it.Checks, it.FailingChecks, it.PassingChecks, it.RunningChecks)
	}
	if _, err := Parse(PRs, 0, []byte(`{"errors":[{"message":"Bad credentials"}]}`)); err == nil || !strings.Contains(err.Error(), "Bad credentials") {
		t.Errorf("an error: %v", err)
	}
}
