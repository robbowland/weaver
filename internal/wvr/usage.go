package wvr

import (
	"strings"

	"github.com/robbowland/weaver/internal/cli"
)

// usageCol is where Usage's descriptions start.
const usageCol = 31

// UsageHelp is Usage, in weaver's palette at a terminal: commands in ink, what
// they do in grey, the internal ones faint.
func UsageHelp() string {
	if !cli.IsTTY(1) {
		return Usage
	}
	var b strings.Builder
	for _, l := range strings.Split(strings.TrimSuffix(Usage, "\n"), "\n") {
		switch {
		case strings.HasPrefix(l, "usage:"):
			b.WriteString(CBold + l + COff)
		case len(l) > usageCol && strings.TrimSpace(l[:usageCol]) == "":
			b.WriteString(strings.Repeat(" ", usageCol) + CGrey + l[usageCol:] + COff)
		case len(l) > usageCol && strings.HasPrefix(l[usageCol:], "internal:"):
			b.WriteString(CDim + l + COff)
		case len(l) > usageCol && l[usageCol-1] == ' ' && l[usageCol-2] == ' ':
			b.WriteString(CInk + l[:usageCol] + COff + CGrey + l[usageCol:] + COff)
		default:
			b.WriteString(CInk + l + COff)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// Usage is `wvr help`.
const Usage = `usage: wvr [command]
  (none)                       outside tmux: attach to, or start, the session "weaver"
                               ($WEAVER_SESSION) on weaver's own tmux server; inside
                               tmux: the dashboard
  start [session]              the same, for another session
  new [task...]                start a thread: a new window with nvim and an agent, in a new
                               worktree whose branch starts from the remote's default
                               branch, fetched; with no task, a form. Options: --repo DIR,
                               --branch NAME, --base REF (@: this checkout's HEAD),
                               --here (the current checkout),
                               --agent claude|codex, --edit (write the task in $EDITOR),
                               --from PANE (that agent's repository), --no-focus;
                               --pr REF / --issue REF: from a pull request or issue
                               (a number, owner/name#N, its URL, or a Linear ENG-42),
                               --review: read-only
  close [--yes] [--remove-worktree] [pane-id]
                               close an agent (the focused one) with its nvim, and its
                               thread's window; offers to remove a thread's worktree when it
                               is clean and merged or pushed
  resume [--all] [which]       threads lost when their session went (a crash, or quit):
                               alone, list them; with a name, number or session id, bring
                               that one back in a new window resuming its conversation;
                               --all, every one. --forget [--yes] which: drop one
  adopt [pane-id | session-id] make a conversation weaver did not start a thread, kept
                               and brought back as threads are: the agent in this pane
                               (or pane-id) where it runs, or conversation session-id
                               resumed in a new window; alone, pick one of Claude Code's
                               recent conversations. Outside tmux, into the weaver session
  rename [pane-id] [name]      call a thread name on the board and its window, in place of
                               its agent's title; with no name, ask; an empty one gives the
                               agent's title back
  restart [--all] [--force] [pane-id]
                               start a thread's agent again where it is (after an update),
                               resuming its conversation; --all, every thread's; one
                               mid-turn is left unless --force
  send [--force] PANE < text   the text to agent PANE's prompt (its nvim's :AgentSend), when
                               it waits for you to say more; --force, even while it works
  quit [--yes]                 quit this session (outside it, "weaver") with
                               every agent and nvim in it, after asking; its threads are
                               kept and come back as the weaver session starts again
  hook [--async]               Claude Code hook (reads the event JSON on stdin)
  event [--bell]               an event of any agent, as normalized JSON on stdin
  adapt codex [--bell]         Codex hook (reads Codex's event JSON on stdin)
  edit [--full] [pane-id]      open or jump to the nvim for the focused agent
  ls                           live dashboard of the agents in this session
  next                         jump to the next agent that needs you; repeat to cycle
  view [github|linear]         go to a view beside home: GITHUB or LINEAR (one
                               not set up shows how; one you hid shows again)
  status [--tmux SESSION]      agent counts (--tmux: for tmux's status line)
  snapshot --reviewed [dir]    mark the worktree holding dir reviewed
  gc                           forget agents that are gone; end the nvims and
                               remove the worktrees weaver left behind
  setup [--dry-run] [--no-codex] [--bin PATH]
                               add weaver's hooks to Claude Code (and Codex, when ~/.codex
                               exists), run by this binary (or PATH), backing up each file
                               it changes; --dry-run shows the changes only
  doctor [--fix]               what weaver works with: there, set up, and how to fix
                               what is not; --fix installs what is missing (Homebrew),
                               sets up the hooks, and offers the sign-ins
  linear login                 sign in to Linear with a personal API key (kept in the
                               macOS Keychain), for the LINEAR view and Linear issues
  version                      weaver's version
  nvim <agent-pane> [file]     internal: start the nvim bound to an agent
`
