package linear

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// noKey is a Keychain with no key of weaver's, and none in the
// environment.
func noKey(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "security"), []byte("#!/bin/sh\nexit 44\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("LINEAR_API_KEY", "")
}

// fakeLinear is Linear's API at a test server, answering with answer for
// each request's query (and its variables), signed in as lin_test.
func fakeLinear(t *testing.T, answer func(q string, vars map[string]any) string) {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if r.Header.Get("Authorization") != "lin_test" || json.NewDecoder(r.Body).Decode(&req) != nil {
			http.Error(w, `{"errors":[{"message":"no"}]}`, http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, answer(req.Query, req.Variables))
	}))
	t.Cleanup(s.Close)
	t.Setenv("WEAVER_LINEAR_URL", s.URL)
	t.Setenv("LINEAR_API_KEY", "lin_test")
}

// issues is an issues answer: identifiers ids, and the cursor of the page
// after ("": none).
func issues(next string, ids ...string) string {
	var ns []string
	for _, id := range ids {
		ns = append(ns, `{"identifier":"`+id+`","title":"t","team":{"key":"ENG","name":"Engineering"},"state":{"name":"Todo","type":"unstarted"}}`)
	}
	return `{"data":{"issues":{"nodes":[` + strings.Join(ns, ",") + `],"pageInfo":{"hasNextPage":` + strconv.FormatBool(next != "") +
		`,"endCursor":"` + next + `"}}}}`
}

// A section says when Linear has more than it sent: Linear counts no list,
// so that is all there is to say.
func TestFetchSaysMore(t *testing.T) {
	fakeLinear(t, func(q string, _ map[string]any) string {
		if !strings.Contains(q, "pageInfo { hasNextPage endCursor }") {
			return `{"errors":[{"message":"no pageInfo asked"}]}`
		}
		if strings.Contains(q, Sections[0].Filter) {
			return issues("c30", "ENG-1", "ENG-2")
		}
		return issues("")
	})
	got, _ := Fetch()
	if got[0].Next != "c30" || len(got[0].Items) != 2 || got[0].Err != "" || got[1].Next != "" || got[1].Err != "" {
		t.Errorf("%+v", got)
	}
}

// A search of your own is asked with the sections: a team's by its key.
func TestFetchSearch(t *testing.T) {
	fakeLinear(t, func(q string, _ map[string]any) string {
		if strings.Contains(q, `team: { key: { eq: "ENG" } }`) {
			return issues("", "ENG-7")
		}
		return issues("")
	})
	got, found := Fetch(TeamFilter("ENG", "Engineering"))
	if len(got) != len(Sections) || len(found) != 1 || found[0].Title != "team Engineering" || len(found[0].Items) != 1 || found[0].Items[0].Identifier != "ENG-7" {
		t.Errorf("%+v %+v", got, found)
	}
}

// A section's next page is asked from where its last ended.
func TestAskPage(t *testing.T) {
	fakeLinear(t, func(q string, vars map[string]any) string {
		if vars["after"] == "c30" && strings.Contains(q, "after: $after") {
			return issues("", "ENG-31")
		}
		return issues("c30", "ENG-1")
	})
	if r := Ask(Sections[0], ""); r.Err != "" || r.Next != "c30" || r.Items[0].Identifier != "ENG-1" {
		t.Errorf("the first page: %+v", r)
	}
	if r := Ask(Sections[0], "c30"); r.Err != "" || r.Next != "" || r.Items[0].Identifier != "ENG-31" {
		t.Errorf("the page after: %+v", r)
	}
}

// Without a key, every section fails for that reason, and says so itself.
func TestFetchNoKey(t *testing.T) {
	noKey(t)
	got, _ := Fetch()
	if len(got) != len(Sections) {
		t.Fatalf("%d sections", len(got))
	}
	for i, r := range got {
		if r.Section != Sections[i] || r.Err != ErrNoKey.Error() || !r.Fetched.IsZero() || len(r.Items) != 0 {
			t.Errorf("%+v", r)
		}
	}
	if _, err := Key(); !errors.Is(err, ErrNoKey) {
		t.Errorf("a key: %v", err)
	}
}
