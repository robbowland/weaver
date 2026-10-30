package wvr

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/oneline"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
	"github.com/robbowland/weaver/internal/tmux"
)

// The board's edits in place: r (rename) turns its input line into a
// prompt for the row's thread, `rename 03 › its name`, the board's keys
// let go so they type; enter does it, esc leaves things as they were. The
// prompt names the thread, so moving the cursor meanwhile changes nothing.

// The edits.
const editRename = "rename"

var reEditPrompt = regexp.MustCompile(`^(rename) (%?\d+) › $`)

// editing is the edit the board's prompt ($FZF_PROMPT) is open for, and
// which thread: its number, or %pane for one without; "" when browsing.
func editing() (kind, which string) {
	m := reEditPrompt.FindStringSubmatch(os.Getenv("FZF_PROMPT"))
	if m == nil {
		return "", ""
	}
	return m[1], m[2]
}

// browseActs are fzf's actions from an edit back to browsing the board.
func browseActs(q string) string {
	return "clear-query+hide-input+enable-search+change-prompt(/ )+change-ghost(search threads)+rebind(" + lsKeys + ")+transform(" + q + " _hf)"
}

// EditMode is `wvr _mode KIND PANE`: fzf's actions that open the edit KIND
// on the board for the thread of the agent in PANE.
func (e *Env) EditMode(args []string) error {
	kind, p := arg(args, 0), mux.PaneID(arg(args, 1))
	q := session.Quote(e.Self)
	th, err := store.ReadThread(e.ThreadFile(p))
	if err != nil {
		fmt.Println("change-footer(" + CGrey + "not a thread yet: a adopts it" + COff + ")")
		return nil
	}
	which := "%" + string(p)
	if th.Num > 0 {
		which = fmt.Sprintf("%02d", th.Num)
	}
	open := "show-input+unbind(" + lsKeys + ")+disable-search+change-prompt(" + kind + " " + which + " › )"
	switch kind {
	case editRename:
		name, _ := e.shownName(p)
		fmt.Println(open + "+change-ghost(empty: its agent's title)+transform(" + q + " _hf)+change-query:" + oneline.Line(name))
	default:
		return errors.New("usage: wvr _mode rename PANE")
	}
	return nil
}

// EditDone is `wvr _modedone PROMPT TEXT`: the edit PROMPT is open for,
// done with TEXT; then fzf's actions back to browsing. What it cannot do it
// says in the footer.
func (e *Env) EditDone(args []string) error {
	q := session.Quote(e.Self)
	m := reEditPrompt.FindStringSubmatch(arg(args, 0))
	text := oneline.Line(strings.TrimSpace(arg(args, 1)))
	back := browseActs(q)
	if m == nil {
		fmt.Println(back)
		return nil
	}
	p, ok := e.paneOfThread(m[2])
	if !ok {
		fmt.Println(back + "+change-footer(" + CGrey + "that thread has gone" + COff + ")")
		return nil
	}
	if m[1] == editRename {
		// The name it shows already (its agent's title, say) stays its
		// agent's: only a new one is yours.
		if cur, _ := e.shownName(p); text != cur {
			bestEffort(e.retitle(p, text))
		}
	}
	fmt.Println(back + "+reload-sync(" + q + " _rows)")
	return nil
}

// paneOfThread is the agent's pane of the thread an edit's prompt names:
// its number in this session, or %pane.
func (e *Env) paneOfThread(which string) (mux.PaneID, bool) {
	if strings.HasPrefix(which, "%") {
		p, ok := tmux.ParsePane(which)
		return p, ok && files.Exists(e.ThreadFile(p))
	}
	n, err := strconv.Atoi(which)
	if err != nil {
		return "", false
	}
	fs, _ := filepath.Glob(e.StateDir + "/" + e.SKey + "_*.thread")
	for _, f := range fs {
		if th, err := store.ReadThread(f); err == nil && th.Num == n {
			return mux.PaneID(strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), e.SKey+"_"), ".thread")), true
		}
	}
	return "", false
}
