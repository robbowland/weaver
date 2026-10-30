package wvr

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/robbowland/weaver/internal/files"
	"github.com/robbowland/weaver/internal/mux"
	"github.com/robbowland/weaver/internal/oneline"
	"github.com/robbowland/weaver/internal/session"
	"github.com/robbowland/weaver/internal/store"
	"github.com/robbowland/weaver/internal/tmux"
)

// The board's edits in place: r (rename) and h (hold) turn its input line
// into a prompt for the row's thread, `rename 03 › its name`, the board's
// keys let go so they type; enter does it, esc leaves things as they were.
// The prompt names the thread, so moving the cursor meanwhile changes
// nothing.

// The edits.
const (
	editRename = "rename"
	editHold   = "hold"
)

var reEditPrompt = regexp.MustCompile(`^(rename|hold) (%?\d+) › $`)

// editing is the edit the board's prompt ($FZF_PROMPT) is open for, and
// which thread: its number, or %pane for one without; "" when browsing.
func editing() (kind, which string) {
	m := reEditPrompt.FindStringSubmatch(os.Getenv("FZF_PROMPT"))
	if m == nil {
		return "", ""
	}
	return m[1], m[2]
}

// browseActs are fzf's actions from an edit (or a search) back to browsing
// the board. The search for the cleared query is fzf's, after: without
// waiting for it, hiding the input leaves the list filtered by what was
// typed.
func browseActs(q string) string {
	return "clear-query+enable-search+wait+hide-input+change-prompt(/ )+change-ghost(search threads)+rebind(" + lsKeys + ")+transform(" + q + " _hf)"
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
	case editHold:
		// Its footer says, while you type, if holding stops a turn.
		fmt.Println(open + "+clear-query+change-ghost(what's next, for when you come back)+transform(" + q + " _hf)")
	default:
		return errors.New("usage: wvr _mode rename|hold PANE")
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
	switch m[1] {
	case editRename:
		// The name it shows already (its agent's title, say) stays its
		// agent's: only a new one is yours.
		if cur, _ := e.shownName(p); text != cur {
			bestEffort(e.retitle(p, text))
		}
	case editHold:
		// Its agent takes a moment to end: behind, as close does, and
		// its row moves to HELD when it has; till then, the footer says
		// it is on its way.
		if b := sockBase(); b != "" {
			bestEffort(files.Put(b+".holding", m[2]))
		}
		c := exec.Command(e.Self, "hold", string(p), text)
		if f, err := os.OpenFile(e.StateDir+"/close.log", os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644); err == nil {
			c.Stdout, c.Stderr = f, f
		}
		c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		bestEffort(c.Start())
	}
	fmt.Println(back + "+reload-sync(" + q + " _rows)")
	return nil
}

// Hold is `wvr hold [PANE] [NOTE...]`: the thread of the agent in PANE
// (else this pane's) on hold, with NOTE, what is next: kept as a held
// thread (docs/design.md, Lost threads), its agent and nvim closed, its
// worktree and branch left as they are. enter on its row under HELD (or
// `wvr resume`) brings it back on its conversation; nothing else does.
func (e *Env) Hold(args []string) error {
	if err := e.toWeaverSession(); err != nil {
		return err
	}
	var p mux.PaneID
	if len(args) > 0 {
		if q, ok := e.paneArg(args[0]); ok {
			p, args = q, args[1:]
		}
	}
	if p == "" && e.MyPane != "" {
		p = e.agentOf(e.MyPane)
	}
	if p == "" {
		return errors.New("usage: wvr hold [pane-id] [note]")
	}
	if !files.Exists(e.ThreadFile(p)) {
		return errNotThread
	}
	if !e.Env.Hold(e.StateFile(p), oneline.Line(strings.TrimSpace(strings.Join(args, " ")))) {
		return errors.New("cannot write its record")
	}
	return e.Close([]string{"--hold", string(p)})
}

// Fold is `wvr _fold`: the board's HELD threads past its first few shown,
// or folded again (enter on the row that stands for them).
func Fold() {
	b := sockBase()
	if b == "" {
		return
	}
	if files.Exists(b + ".heldopen") {
		bestEffort(os.Remove(b + ".heldopen"))
		return
	}
	bestEffort(files.Put(b+".heldopen", "1"))
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
