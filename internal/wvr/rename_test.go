package wvr

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
)

// A thread's branch is renamed in git and in its record, its name with it
// when that was the branch's; one the remote has already is left as it
// is, for a pull request on it keeps the old name.
func TestRenameBranch(t *testing.T) {
	for _, c := range []struct {
		name    string
		pushed  bool
		want    string
		wantErr string
	}{
		{"before it is pushed", false, "fix-login-redirect", ""},
		{"once the remote has it", true, "in-my-panel-it-seems",
			"in-my-panel-it-seems is on origin already: a pull request on it would keep the old name"},
	} {
		t.Run(c.name, func(t *testing.T) {
			td, _ := filepath.EvalSymlinks(t.TempDir())
			repo := td + "/repo"
			git := func(args ...string) string {
				t.Helper()
				out, err := exec.Command("git", append([]string{"-C", repo, "-c", "user.email=t@e", "-c", "user.name=t"}, args...)...).CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %v %s", args, err, out)
				}
				return strings.TrimSpace(string(out))
			}
			if err := exec.Command("git", "init", "-q", "-b", "main", repo).Run(); err != nil {
				t.Fatal(err)
			}
			git("commit", "-q", "--allow-empty", "-m", "base")
			git("remote", "add", "origin", "https://example.invalid/o/r.git")
			git("switch", "-q", "-c", "in-my-panel-it-seems")
			if c.pushed {
				git("update-ref", "refs/remotes/origin/in-my-panel-it-seems", "HEAD")
			}
			e := &Env{&session.Env{StateDir: td + "/state", SKey: "rex-s"}}
			th := store.Thread{Num: 1, Name: "in-my-panel-it-seems", Repo: repo, Branch: "in-my-panel-it-seems", Worktree: repo}
			if err := store.Write(e.ThreadFile("5"), th); err != nil {
				t.Fatal(err)
			}

			_, err := e.renameBranch("5", "fix-login-redirect")

			got := ""
			if err != nil {
				got = err.Error()
			}
			if got != c.wantErr {
				t.Errorf("error %q, want %q", got, c.wantErr)
			}
			if b := git("branch", "--show-current"); b != c.want {
				t.Errorf("git's branch %q, want %q", b, c.want)
			}
			rec, _ := store.ReadThread(e.ThreadFile("5"))
			if rec.Branch != c.want || rec.Name != c.want {
				t.Errorf("record's branch %q, name %q, want %q", rec.Branch, rec.Name, c.want)
			}
		})
	}
}
