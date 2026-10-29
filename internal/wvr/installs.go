package wvr

import (
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/session"
)

// The home board and the views outlive installs of weaver: each runs for
// good, its keys bound to the weaver it started in, while what it shows is
// read by whichever weaver runs. So each starts again, in the same pane and
// process, in the weaver `wvr` resolves to now, as tmux's keys and the
// hooks run it: when that is another file than the one it started as (an
// upgrade, or Homebrew's swapped for install.sh's, either way), or that
// file changed since. Its born file says when it started (its time) and
// the file it started as (its real path, in it).

// markBorn marks a board or view started now, from this weaver, in born.
func (e *Env) markBorn(born string) {
	path, err := filepath.EvalSymlinks(e.Self)
	if err != nil {
		path = e.Self
	}
	bestEffort(os.WriteFile(born, []byte(path+"\n"), 0o600))
}

// successor is the weaver a board or view marked in born starts again in,
// if it is not the one it runs: $WEAVER_WVR, the weaver that started its
// pane (on Rex, whose login shells may put another wvr first on PATH),
// else the wvr on $PATH (else this binary), when that is another file than
// the one it started as, or changed since. A few stats, for chrome asks on
// every reload. An older weaver's born (empty) says only when.
func (e *Env) successor(born string) (string, bool) {
	b, err := os.Stat(born)
	if err != nil {
		return "", false
	}
	now := os.Getenv("WEAVER_WVR")
	if !filepath.IsAbs(now) {
		if now, err = exec.LookPath("wvr"); err != nil || !filepath.IsAbs(now) {
			now = e.Self
		}
	}
	path, err := filepath.EvalSymlinks(now)
	if err != nil {
		return "", false
	}
	if was, _ := files.FirstLine(born); was != "" && was != path {
		return now, true
	}
	if st, err := os.Stat(path); err == nil && st.ModTime().After(b.ModTime()) {
		return now, true
	}
	return "", false
}

// watchInstalls ends the fzf listening on sock once a board or view marked
// in born has a successor. Chrome ends it on a reload, but cannot once the
// file its keys run by has gone (that install removed): this asks from the
// board's own process.
func (e *Env) watchInstalls(born, sock string) {
	go func() {
		for range time.Tick(5 * time.Second) {
			if _, ok := e.successor(born); ok {
				session.FzfPost(sock, "abort")
			}
		}
	}()
}
