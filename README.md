# weaver

**Pick up whichever thread needs you.**

Run several coding agents side by side in zellij and weaver keeps track of them:
which are working, which are done, and which are waiting on you. One key shows
them all, another jumps to whoever has waited longest, and a neovim bound to
each agent follows it into the worktree and file it is really editing.

weaver drives Claude Code through its hooks, and Codex through an adapter;
any other agent can report through `weaver event`
([docs/design.md](docs/design.md#events-from-other-agents)).

## Keys

| Key | Does |
| --- | --- |
| `Alt y` | Live dashboard of every agent in the session, with a resume card for the selected one. `enter` jumps, `ctrl-e` jumps and opens nvim, `ctrl-r` reloads |
| `Alt 1` | Jump to the agent that has needed you longest, then the one that finished longest ago and you haven't seen; press again for the next, round and round |
| `Alt v` | Open, or jump back to, the nvim beside the focused agent |
| `Alt V` | The same, fullscreen |

## What it tracks

Each agent is **working**, **needs you** (a permission dialog, a question, an
MCP prompt, an API error), **done**, **interrupted** (Esc, or a rejected
dialog) or **idle**. Needs-you clears only when you answer; done counts as
unread until you look at the agent. Compaction, `/clear` and resume keep the
right state, a session nested inside an agent (`claude -p`, an SDK script) is
ignored, and an agent killed without a word drops off the board. Where the
hooks can't see (Esc while the model writes, a long tool you approved), the
state is refined from Claude Code's own session files.

An agent that starts needing you, or finishes, rings the terminal bell, so
zellij marks its tab until you look at it.

The dashboard reloads by itself as agents change. Its resume card says what the
agent was asked, what it waits on, what it last said, its tasks, subagents and
files, and how long it has been in that state; then its git status and screen.
Two agents in one worktree are flagged: their per-turn diffs mix.

### Status bar

`weaver status` prints the counts (`! 1  ✓ 2  ● 3`: needs you, done and
unseen, working). In [zjstatus](https://github.com/dj95/zjstatus), its command
widget shows them in Micrographics signal colours:

```kdl
format_right "{command_weaver}{mode} "
command_weaver_command    "weaver status --zjstatus"
command_weaver_format     "{stdout}"
command_weaver_interval   "2"
command_weaver_rendermode "dynamic"
// and the tab mark for an agent's bell (zjstatus 0.24+):
tab_normal_bell    "#[fg=#ffffff,bold][{index}] {name}{bell_indicator} "
tab_bell_indicator " ●"
```

`weaver status --zjstatus` only reads a small file the hooks keep, so running
it every two seconds in every tab costs a few milliseconds each. It needs
`weaver` on the PATH zellij was started with.

### Turns and the activity log

Each prompt of yours is a turn. weaver snapshots the agent's worktree at the
start and end of every turn into `refs/worktree/weaver/sessions/<session>`,
through a private index (your staging area is never touched), so each turn's
diff, and `git blame` back to the prompt that produced a line, are a git
command away; edits you make between turns are their own commit.
`weaver snapshot --reviewed` marks the worktree reviewed
(`refs/worktree/weaver/reviewed`). Everything an agent does is also logged to
`~/.local/state/weaver/log/<session>.jsonl`. The formats are in
[docs/design.md](docs/design.md).

## Neovim

The nvim weaver opens for an agent loads your own config, then:

- **follows the agent politely.** An edit by the agent (or its subagents, or
  its Bash commands) is followed only once you have been idle for about 3
  seconds in normal mode, in a file window, and not reviewing a diff. Until
  then it waits in the bar as `→ file`, and `<leader>aj` goes there at once.
  It never types into nvim, never replaces a buffer with unsaved changes, a
  terminal, quickfix or git window, and brings other tabs up to date without
  switching to them;
- gives each worktree its own tab (closed again when the worktree is removed);
- reloads files the moment they change on disk, never over unsaved changes;
- shows the agent at the top of each file window: its state, turn, task
  progress, the edit waiting to be followed, and what it was asked or is
  waiting on; once a turn is over, how many files it changed and the key to
  review them (`2 files ␣at`).

### Reviewing

Reviews open in one [diffview-plus](https://github.com/dlyongemallo/diffview-plus.nvim)
view. A scope's key opens it, pressing it again from elsewhere brings the view
back (refreshed), pressing it inside the view closes it, and another scope
replaces it. The view refreshes itself as the agent edits.

| Key | Command | Shows |
| --- | --- | --- |
| `<leader>at` | `:AgentReview turn` | The agent's last turn (the turn so far while it works) |
| `<leader>au` | `:AgentReview uncommitted` | The worktree against the index |
| `<leader>ab` | `:AgentReview branch` | The whole branch, committed or not |
| `<leader>ar` | `:AgentReview reviewed` | Everything since you last marked it reviewed |
| `<leader>am` | `:AgentReview mine` | Only the files this agent changed |
| `<leader>aR` | `:AgentReviewed` | Mark the worktree reviewed as it is now |
| `<leader>aT` | `:AgentTurns` | Pick a turn and review its own diff |
| `<leader>ac` | `:AgentChanges turn` | The last turn's hunks in quickfix (any scope works) |
| `<leader>al` | `:AgentLog` | The agent's activity, turn by turn |
| `<leader>aw` | `:AgentWhy` | Which turn and prompt last changed this line (`!` opens that turn) |
| `<leader>aj` | `:AgentJump` | Go to the agent's latest edit now |
| `<leader>af` | `:AgentFollow` | Toggle following |

In a review, `gf` opens the file at the same line in the tab of its own
worktree. Untracked files the agent created are listed like any other (your
index is never touched to do it). In diffview-plus's file panel, `w` marks a
file reviewed and `H` hides the reviewed ones.

The branch is compared against `$WEAVER_BASE`, else
`git config branch.<branch>.weaverbase`, else the default branch of the
`upstream` or `origin` remote, else `main` or `master`.

`:checkhealth weaver` says whether diffview-plus, the snacks picker, the
weaver script and the agent binding are in place.

Your own config can use the same one-view review:
`require('weaver.diffview').show({ key = ..., label = ..., root = ..., compare = { kind = 'branch', base = 'origin/main' } })`
(see `nvim/lua/weaver/diffview.lua`). `require('weaver.status').statusline()`
is the agent's state for a statusline, empty where the bar already shows it,
and `User WeaverEvent` autocmds receive every event from the agent.

## Install

Needs zellij 0.45+, neovim 0.10+, jq and git; fzf for the interactive
dashboard, and diffview-plus.nvim in your nvim for reviews.

```sh
./install.sh --configure
```

This copies weaver to `~/.local/share/weaver`, links `~/.local/bin/weaver`, adds
the keybindings to your zellij config and the hooks to
`~/.claude/settings.json` (replacing any older weaver hooks), backing up each
file it changes. The hooks run `~/.local/bin/weaver` directly, without a shell;
the ones nobody needs to wait for run in the background. Pass
`--zellij-config FILE` (repeatable) for other zellij configs and `--dry-run` to
see the diff first. It is safe to rerun, and it replaces an install from when
weaver was called agentnav.

Restart running Claude Code sessions afterwards: they read hooks at start.
When `~/.codex` exists, Codex's hooks (`~/.codex/hooks.json`) get weaver too
(`--no-codex` to skip); trust them in Codex's `/hooks` before they run.

## Settings

| Variable | Default | Does |
| --- | --- | --- |
| `WEAVER_BELL` | `1` | `needs-you`: ring only when an agent needs you; `0`: never |
| `WEAVER_SNAPSHOTS` | `1` | `0` stops the per-turn snapshots |
| `WEAVER_SNAPSHOT_MAX_KB` | `5120` | Untracked files bigger than this stay out of snapshots |
| `WEAVER_WT_MARKERS` | `1` | `0` stops setting [Worktrunk](https://github.com/max-sixty/worktrunk) branch markers (🤖 working, 💬 needs you) |
| `WEAVER_DEBUG` | unset | Write hook errors to `~/.local/state/weaver/hook.log`; `trace` traces every hook there |

## Tests

```sh
test/run.sh [suite...]
```

End-to-end, in a throwaway zellij session with its own state, Claude Code
config dir and minimal nvim config; your real sessions are not touched. Each
suite starts from a clean slate, so any one can run alone.
`test/suites/05-latency.sh` measures what each hook costs the agent.
`test/nvim/run.sh` runs only the Neovim specs (headless, a few seconds); set
`WEAVER_TEST_PLUGINS` to a directory holding `diffview-plus.nvim` if
lazy.nvim's plugin directory doesn't have it.
`WEAVER_TEST_ZJSTATUS=1 test/run.sh test/soak/zjstatus.sh` soaks the status
bar widget in zjstatus for a few minutes (it asks zellij for zjstatus's
permissions, and puts zellij's permission cache back as it was).
