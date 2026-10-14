# weaver

**Pick up whichever thread needs you.**

Run several coding agents side by side in zellij and weaver keeps track of them:
which are working, which are done, and which are waiting on you. One key shows
them all, another jumps to whoever has waited longest, and a neovim bound to
each agent follows it into the worktree and file it is really editing.

weaver drives Claude Code through its hooks, and Codex through an adapter;
any other agent can report through `weaver event`
([docs/design.md](docs/design.md#events-from-other-agents)).

## Start

```sh
weaver
```

Outside zellij, `weaver` attaches to the zellij session `weaver`, or starts it
with your default layout (`WEAVER_SESSION`, or `weaver start NAME`, for
another). Inside zellij it opens the dashboard.

Then `Alt N`: say what the agent should do, press enter, and a new tab opens
for that thread, with its nvim on the left and the agent on the right, started
with your task as its first prompt, in a new worktree on a branch named after
the task.

## Keys

| Key | Does |
| --- | --- |
| `Alt N` | Start a thread: a form for the task, then a tab with nvim and the agent |
| `Alt y` | The dashboard: every agent in the session, grouped by state, with a resume card for the selected one. `enter` or `1`-`9` jumps, `e` jumps and opens nvim, `?` lists its keys |
| `Alt 1` | Jump to the agent that has needed you longest, then the one that finished longest ago and you haven't seen; press again for the next, round and round |
| `Alt v` | Open, or jump back to, the nvim beside the focused agent |
| `Alt V` | The same, fullscreen |

## Threads

A thread is one task for one agent, in a tab of its own: the agent's nvim on
the left, the agent on the right (a fifth of the width), in a worktree of its
own or in the checkout you are in. The tab is named after the branch (after
the task, in this checkout), keeps your status bar, and you land on the agent.

The form (`Alt N`, `weaver new`, or `n` in the dashboard) starts in the
repository of the pane you were on (else the one you used last):

| Key | Does |
| --- | --- |
| `enter` | Start the thread |
| `tab` | New worktree, or this checkout (for a quick question) |
| `alt-b` | Edit the branch name (from the task by default); an existing branch, here or on the remote, continues in its own worktree |
| `alt-B` | Where a new branch starts: the remote's default branch, fetched (the default), `@` (the HEAD of the checkout you are in), or any branch |
| `ctrl-r` | Another repository: recent ones, any zoxide knows, or a path |
| `shift-tab` | Claude Code or Codex |
| `ctrl-e` | Write a long task in `$EDITOR` |
| `esc` | Cancel |

From a shell: `weaver new "fix the flaky test"`, with `--repo DIR`,
`--branch NAME`, `--base REF`, `--here`, `--agent codex`, `--edit` (write the
task in `$EDITOR`) or `--no-focus`.

New worktrees come from [Worktrunk](https://worktrunk.dev) when it is
installed (`wt switch --create`, so your `worktree-path` and hooks apply),
else from `git worktree add` at `../<repo>.<branch>`. A new branch starts from
the remote's default branch (`origin`, else the first remote) freshly fetched,
as `wnew` does: the form starts the fetch as it opens and its `base` row says
how it is going, so enter rarely waits. Offline, slower than
`WEAVER_FETCH_TIMEOUT`, or with no remote, it starts from the local default
branch and says so. The nvim on the left is
the agent's own weaver nvim, so following, the bar, reviews and `Alt v` all
work from the first prompt.

If you quit a thread's nvim, `Alt v` brings it back on the left with the
agent at its width again (when nothing else is in the tab).

`weaver close` (`x` in the dashboard) closes an agent, its nvim and its
thread's tab, after asking; panes you added to the tab stay. For a worktree
weaver made, and only when nothing would be lost (nothing uncommitted, and
merged into the default branch or pushed), it then offers to remove it
(`wt remove`, else `git worktree remove`); the branch always stays.

When the agent exits its pane stays; enter there resumes its conversation
(`claude --resume`, `codex resume`), never sending the task twice.

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

### The dashboard

```
┌─ weaver · 5 threads ───────────────────────────────── ! 2  ✓ 1  ● 1 ─┐
│                                                                      │
│   NEEDS YOU 2                                                        │
│   1 !  Add a CLI flag           Bash: python app.py --name rob  11m  │
│   2 !  Tidy the fish config     question: Which config file s…   8m  │
│                                                                      │
│   DONE 1                                                             │
│   3 ✓  Add a README             Wrote README.md and CONTRIBUT…   8m  │
│                                                                      │
│   WORKING 1                                                          │
│   4 ●  Group the dashboard ro…  2 subagents                      9m  │
│                                                                      │
│   IDLE 1                                                             │
│   5 ✓  Audit yabai float rules  Removed two dead rules; the r…  47m  │
│   ↵ jump  1-9  n new  x close  / search  s screen  ? help            │
│ ─────────────────────────────────────────────────────────────────────│
│ Add a CLI flag · try/add-a-cli-flag · turn 3                         │
│ needs you · 11m                                                      │
│                                                                      │
│ asked      add a CLI flag to app.py, then run it to check it works   │
│ waits      Bash: python app.py --name rob                            │
│ said       Added --name and --shout; running it to check.            │
│ tasks      ✓ parse the flag  ✓ wire it into main  ○ run it           │
└──────────────────────────────────────────────────────────────────────┘
```

Agents are grouped under NEEDS YOU, DONE (finished, not yet seen), WORKING
and IDLE (seen, idle or interrupted), in the order `Alt 1` visits them; the
headings' counts are the status bar's. Each row is the thread's name, one fact
(what it waits on, what its last turn changed and said, its subagents),
repo/branch when there is room, and how long it has been in that state. The
shapes read without colour: `!` needs you, `✓` done, `●` working, `○` idle,
`◌` interrupted; `×2` after a branch means two agents share the worktree, so
their per-turn diffs mix.

The cursor never rests on a heading, and stays on its agent as the board
reloads by itself. The keys: `enter` jumps, `1`-`9` jump to that row,
`j`/`k`/`g`/`G` move, `/` searches (`esc` back), `n` starts a thread
(`weaver new`), `x` closes one (`weaver close`), `e` jumps and opens its nvim,
`s` (or `tab`) swaps the resume card for the agent's screen, `?` lists them
all, `q` or `esc` closes the dashboard. With no agents, it says how to start
one.

The resume card says what the agent was asked, what it waits on, what it last
said, its tasks, subagents and files, and its worktree's git status in one
line. The dashboard's pane fits what it shows: the card sits beside the list
when there is room for 150 columns, else under it, and gives way to the list
when space is short (`s` still shows it). `weaver ls | cat` prints the board
as text.

### Sidebar and home

In the `weaver` session every thread tab has a sidebar on its left: every
thread in its own number's order, its state, name and age, the tab's own
thread inverted; what a thread waits on shows under it. Under 16 columns it
narrows to a rail of numbers and glyphs. Click or move into it and `j`/`k`,
`enter` or a digit go to a thread. The home tab holds the dashboard at full
size, numbered the same way, so a digit means the same thread everywhere. The
status line shows the current thread (`2 Add a CLI flag · try/add-a-cli-flag`),
the counts and zellij's mode.

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
tab_bell_indicator " *"
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
| `WEAVER_SESSION` | `weaver` | The zellij session `weaver` attaches to or starts |
| `WEAVER_AGENT_WIDTH` | `20%` | The agent's share of a thread tab's width (`25%`), or columns (`60`) |
| `WEAVER_AGENT_MIN_COLS` | `44` | The least a percentage gives the agent, as long as nvim keeps 60 columns |
| `WEAVER_WORKTRUNK` | `1` | `0` makes threads' worktrees with plain git even when Worktrunk is installed |
| `WEAVER_FETCH_TIMEOUT` | `10` | Seconds a new thread waits for its remote's fetch before starting from the local default branch |

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
