package wvr

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/robbowland/weaver/internal/session"
)

// The start time from the kernel reads as ps prints it (agentAlive compares
// it with proc_start, which hooks take from ps or Claude Code).
func TestProcStartsMatchPs(t *testing.T) {
	pids := []int{os.Getpid(), os.Getppid(), 1}
	got := session.ProcStarts(pids)
	for _, p := range pids {
		c := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(p))
		c.Env = append(os.Environ(), "TZ=UTC", "LC_ALL=C")
		out, err := c.Output()
		if err != nil {
			t.Fatal(err)
		}
		want := strings.Join(strings.Fields(string(out)), " ")
		if g := strings.Join(strings.Fields(got[p]), " "); g != want {
			t.Errorf("pid %d: kernel %q, ps %q", p, g, want)
		}
		// launchd (pid 1) is root's: kill -0 is refused.
		if p != 1 && !session.AgentAlive(strconv.Itoa(p), string(out), got) {
			t.Errorf("pid %d: alive with ps's start time", p)
		}
	}
}
