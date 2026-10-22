package github

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestQuery(t *testing.T) {
	q := Query(PRs, Sections[PRs][0], "")
	for _, want := range []string{`s: search(type: ISSUE, first: 100, query: "is:pr archived:false is:open user-review-requested:@me`,
		`t: search(type: ISSUE, first: 100, query: "is:pr archived:false is:open assignee:@me -author:@me`,
		"...pr", "fragment pr on PullRequest", "statusCheckRollup"} {
		if !strings.Contains(q, want) {
			t.Errorf("PR query lacks %q", want)
		}
	}
	if q := Query(PRs, Sections[PRs][1], ""); !strings.Contains(q, `first: 50, query: "is:pr archived:false is:open review-requested:@me -user-review-requested:@me draft:false`) || strings.Contains(q, "t: search") {
		t.Errorf("Team query: %s", q)
	}
	if q := Query(Issues, Sections[Issues][1], ""); !strings.Contains(q, `"is:issue archived:false is:open author:@me`) || !strings.Contains(q, "fragment issue on Issue") {
		t.Errorf("issue query: %s", q)
	}
}

// A section with a second search shows the items of both once each, most
// recently updated first, and counts each once.
func TestParseMerges(t *testing.T) {
	pr := func(n int, at string) string {
		return `{"number":` + strconv.Itoa(n) + `,"updatedAt":"` + at + `","repository":{"nameWithOwner":"o/r"}}`
	}
	answer := `{"data":{"s":{"issueCount":2,"nodes":[` + pr(1, "2026-10-16T10:00:00Z") + `,` + pr(2, "2026-10-16T09:00:00Z") +
		`]},"t":{"issueCount":2,"nodes":[` + pr(2, "2026-10-16T09:00:00Z") + `,` + pr(3, "2026-10-16T11:00:00Z") + `]}}}`
	r, err := Parse(PRs, Sections[PRs][0], "", []byte(answer))
	if err != nil {
		t.Fatal(err)
	}
	var refs []string
	for _, it := range r.Items {
		refs = append(refs, it.Ref())
	}
	if got := strings.Join(refs, " "); got != "o/r#3 o/r#1 o/r#2" || r.Total != 3 {
		t.Errorf("items %s, total %d; want o/r#3 o/r#1 o/r#2, 3", got, r.Total)
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
	r, err := Parse(PRs, Sections[PRs][0], "", []byte(answer))
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
	if _, err := Parse(PRs, Sections[PRs][0], "", []byte(`{"errors":[{"message":"Bad credentials"}]}`)); err == nil || !strings.Contains(err.Error(), "Bad credentials") {
		t.Errorf("an error: %v", err)
	}
}

// Each section says how asking for it went: one that fails, why, and the
// rest what GitHub found, when.
func TestFetchEachSection(t *testing.T) {
	bin := t.TempDir()
	gh := "#!/bin/sh\ncase \"$*\" in\n*'is:open author:@me'*) echo 'HTTP 502' >&2; exit 1 ;;\nesac\n" +
		`echo '{"data":{"s":{"issueCount":0,"nodes":[]}}}'` + "\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(gh), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	search := Filters[4].Section()
	got, found := Fetch(search)
	for _, k := range []Kind{PRs, Issues} {
		for i, r := range got[k] {
			mine := r.Title == "Mine"
			if r.Section != Sections[k][i] || mine != (r.Err == "gh: HTTP 502") || mine != r.Fetched.IsZero() {
				t.Errorf("%s %s: %+v", k, r.Title, r)
			}
		}
		// A search of your own, of each kind, with them.
		if len(found[k]) != 1 || found[k][0].Section != search || found[k][0].Err != "" || found[k][0].Fetched.IsZero() {
			t.Errorf("%s search: %+v", k, found[k])
		}
	}
}

// A label is a repository's: its search is within the repositories it was
// seen in, the label quoted.
func TestLabelFilter(t *testing.T) {
	f := LabelFilter("good first issue", []string{"o/r", "o/s"})
	if f.Title != "label good first issue" || f.Filters != `is:open label:"good first issue" repo:o/r repo:o/s sort:updated-desc` {
		t.Errorf("%+v", f)
	}
}

// A section pages on from where its last page ended; one with a second
// search pages both, each from its own cursor, leaving out one at its end.
func TestQueryPages(t *testing.T) {
	if q := Query(PRs, Sections[PRs][2], "c1"); !strings.Contains(q, `s: search(type: ISSUE, first: 20, after: "c1", query: "is:pr archived:false is:open author:@me`) ||
		!strings.Contains(q, "issueCount pageInfo { hasNextPage endCursor } nodes") {
		t.Errorf("Mine's next page: %s", q)
	}
	queue := Sections[PRs][0]
	if q := Query(PRs, queue, "c1 c2"); !strings.Contains(q, `s: search(type: ISSUE, first: 100, after: "c1"`) || !strings.Contains(q, `t: search(type: ISSUE, first: 100, after: "c2"`) {
		t.Errorf("Queue's next page: %s", q)
	}
	if q := Query(PRs, queue, "c1 -"); !strings.Contains(q, `s: search(type: ISSUE, first: 100, after: "c1"`) || strings.Contains(q, "t: search") {
		t.Errorf("Queue's next page, its second search at its end: %s", q)
	}
	if q := Query(PRs, queue, "- c2"); strings.Contains(q, "s: search") || !strings.Contains(q, `t: search(type: ISSUE, first: 100, after: "c2"`) {
		t.Errorf("Queue's next page, its own search at its end: %s", q)
	}
}

// A page says where the next starts: for a section with a second search,
// both searches' cursors, one at its end marked so, and none once both
// are.
func TestParseNext(t *testing.T) {
	search := func(more bool, cursor string) string {
		return `{"issueCount":1,"pageInfo":{"hasNextPage":` + strconv.FormatBool(more) + `,"endCursor":"` + cursor + `"},"nodes":[]}`
	}
	queue := Sections[PRs][0]
	for _, c := range []struct {
		s           Section
		after, data string
		next        string
	}{
		{queue, "", `{"s":` + search(true, "c1") + `,"t":` + search(false, "x") + `}`, "c1 -"},
		{queue, "c1 -", `{"s":` + search(false, "c2") + `}`, ""},
		{queue, "c1 c9", `{"s":` + search(false, "c2") + `,"t":` + search(true, "c10") + `}`, "- c10"},
		{Sections[PRs][2], "", `{"s":` + search(true, "m1") + `}`, "m1"},
		{Sections[PRs][2], "m1", `{"s":` + search(false, "m2") + `}`, ""},
	} {
		r, err := Parse(PRs, c.s, c.after, []byte(`{"data":`+c.data+`}`))
		if err != nil || r.Next != c.next {
			t.Errorf("%s after %q: next %q, %v; want %q", c.s.Title, c.after, r.Next, err, c.next)
		}
	}
	if _, err := Parse(PRs, queue, "c1 c2", []byte(`{"data":{}}`)); err == nil {
		t.Error("no search answered")
	}
}

// Words are asked as typed when they say where (a qualifier); a reference
// as its number in its repository; bare words within the owners known, as
// GitHub's search is of all of GitHub.
func TestWords(t *testing.T) {
	for _, c := range []struct {
		words  string
		owners []string
		want   string
	}{
		{"flaky  test", []string{"o", "p"}, "user:o user:p flaky test"},
		{"flaky", nil, "involves:@me flaky"},
		{"is:open label:bug", []string{"o"}, "is:open label:bug"},
		{"crash -author:bot", []string{"o"}, "crash -author:bot"},
		{"o/r#45", []string{"p"}, "repo:o/r 45"},
	} {
		if f := Words(c.words, c.owners); f.Filters != c.want || f.Title != strings.Join(strings.Fields(c.words), " ") {
			t.Errorf("%q: %+v, want %q", c.words, f, c.want)
		}
	}
}
