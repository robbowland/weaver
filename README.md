# weaver

**Pick up whichever thread needs you.**

> Slop readme, will update eventually

Run several coding agents side by side in tmux and weaver keeps track of them:
which are working, which are done, and which are waiting on you. One key shows
them all, another jumps to whoever has waited longest, and a neovim bound to
each agent follows it into the worktree and file it is really editing.

Beside the threads, the session shows your pull requests and issues, on
GitHub and on Linear, and enter on any of them starts a thread on it, Alt R
a read-only review.

wvr drives Claude Code through its hooks, and Codex through an adapter;
any other agent can report through `wvr event`
([docs/design.md](docs/design.md#events-from-other-agents)).

## Install

macOS, with [Homebrew](https://brew.sh). One command installs weaver and
everything it works with: tmux, Neovim, fzf, [Worktrunk](https://worktrunk.dev)
for the worktrees, and gh for the [GITHUB view](#views-github-and-linear).
Then start it:

```sh
brew install robbowland/kegs/weaver
wvr
```

Bring your own agent: [Claude Code](https://github.com/anthropics/claude-code)
or [Codex](https://github.com/openai/codex). Signing in to GitHub and Linear
is done in place: a view not set up yet has a dot in the status line, and
going there offers the one key that signs it in (`gh auth login`, or
`wvr linear login`, which asks for a Linear API key and keeps it in the
Keychain); `h` there hides a view you do not use.

`wvr doctor` says what is there and what is not, with the command that
fixes each; `wvr doctor --fix` does what it can: Homebrew installs what is
missing or too old, weaver sets up its hooks and fetches its plugin, and
each sign-in is offered.

`brew upgrade` keeps it current; `brew install --HEAD
robbowland/kegs/weaver` installs what is on main, unreleased.

Its command is `wvr`, and it sets itself up as it starts, when it is the
`wvr` on your PATH: Claude Code's hooks (`~/.claude/settings.json`, backed
up before it first changes; and Codex's, `~/.codex/hooks.json`, when you have
Codex) run it, replacing any older weaver hooks and touching nobody else's,
and [diffview-plus.nvim](https://github.com/dlyongemallo/diffview-plus.nvim),
which reviews are built on, is fetched for nvims whose config has no
diffview of its own. It says one line when it changes a file.
`wvr setup --dry-run` shows what it would change; `WEAVER_SELF_SETUP=0`
turns it off. The hooks run the binary directly, without a shell; the ones
nobody needs to wait for run in the background. Trust Codex's in its
`/hooks` before they run.

Restart running Claude Code sessions when their hooks change: they read
hooks at start. weaver's tmux needs no config of yours. After an upgrade, a
running weaver session reads weaver's tmux config again the next time `wvr`
attaches to it, and an open home board restarts in the new weaver at its
next reload.

wvr unpacks its files under `~/.local/share/weaver/runtime`, and keeps a
copy of its Neovim plugin at `~/.local/share/weaver/nvim`. The nvims weaver
opens load it themselves; to use its review or statusline in your other
nvims ([Neovim](#neovim)), add it to your config, with lazy.nvim:

```lua
{ dir = (vim.env.XDG_DATA_HOME or vim.env.HOME .. '/.local/share') .. '/weaver/nvim', name = 'weaver', lazy = false }
```

## Start

```sh
wvr
```

Outside tmux, `wvr` starts the tmux session `weaver`, or attaches to it
(`WEAVER_SESSION`, or `wvr start NAME`, for another). Inside it opens the
dashboard. The session runs on weaver's own tmux server (`tmux -L weaver`,
configured by `config/tmux.conf`, then `~/.config/weaver/tmux.conf` when you
have one), so it never touches a tmux of yours, and closing the terminal only
detaches from it: `wvr` again picks up where you were.

The weaver session has no window list; weaver does the switching:

- **home**, window 0: the board of every thread, full size;
- one window per thread, its index the thread's number: its nvim, the agent
  on the right;
- one status line: the current thread, its repo and branch, the counts, and
  tmux's mode (`[NRM]`, `[PFX]` with the prefix down, `[CPY]` in copy mode).

Then `Alt N`: say what the agent should do, press enter, and a new window
opens for that thread, with its nvim and the agent, started with your task as
its first prompt, in a new worktree on a branch named after the task.

Agents you start by hand in any tmux session report to weaver as well, and
`wvr` inside such a session opens its dashboard.

## Keys

| Key | Does |
| --- | --- |
| `Alt N` | Start a thread: a form for the task, then a window with nvim and the agent. In a view, a thread on the item under the cursor |
| `Alt R` | In a view, a read-only review of the item under the cursor |
| `Alt j` / `Alt k` | The next / previous window, so the threads in their numbers' order (home included, the views not) |
| `Alt h` / `Alt l` | The pane to the left / right |
| `Alt y` | Home, then each view (GITHUB, LINEAR), then back to where you came from |
| `Tab` / `Shift Tab` | On home or in a view: the next / previous view, round as a tab bar goes (THREADS, GITHUB, LINEAR) |
| `Alt 1` | Jump to the agent that has needed you longest, then the one that finished longest ago and you haven't seen; press again for the next, round and round |
| `Alt v` | Open, or jump back to, the nvim beside the focused agent |
| `Alt V` | The same, fullscreen |
| `Cmd D` / `Cmd Shift D` | Split the pane right / down (Ghostty sends them as `keybind = super+d=csi:100;9~` and `super+shift+d=csi:100;10~`) |
| `Ctrl q` | Quit the session, every agent and nvim in it; the threads come back as `wvr` starts (while an agent is mid-turn, it asks first) |

tmux's own keys work behind its prefix, `Ctrl b` (`Ctrl b d` detaches, `Ctrl b
[` scrolls back).

### Mouse

Every key has its place to click, as in btop or lazygit, and every click
has its key:

- **Hints are buttons.** A key shown under a list (`n NEW`, `x CLOSE`, `esc
  CANCEL`) does what it says when clicked, as do a card's choices.
- **Rows.** A click picks a row, a second opens it (as `enter`). In the
  form and a picker, one click is enough.
- **Right click** opens a menu of what can be done there, each item with
  its key: on a row of the board or a view, that row's (jump, nvim, close;
  start a thread, review, open); on an agent, its thread's; on the status
  line, weaver's (home, the views, the next that needs you, a new thread,
  quit). tmux's own menu stays on `Alt` and a right click.
- **The status line.** `THREADS`, `GITHUB` and `LINEAR` go there; the counts
  go to the agent that needs you (`[!]`, `[x]`) or home (`[/]`).
- **The wheel** scrolls what is under it: a list, a card, or a pane's
  history, with a scrollbar to drag while you are back in it.

A drag in a pane selects and copies, in tmux's copy mode. To select the
terminal's own way, hold `Shift` (Ghostty) or `Option` (iTerm2) as you drag.

## Threads

A thread is one task for one agent, in a window of its own:
the agent's nvim on the left, the agent on the right (a fifth of the width), in
a worktree of its own or in the checkout you are in. The window is named after
the thread (`01 Add a CLI flag · repo/add-a-cli-flag` once its agent reports),
keeps your status bar, and you land on the agent.

The form (`Alt N`, `wvr new`, or `n` in the dashboard) starts in the
repository of the pane you were on (else the one you used last):

You type the task; under it are its choices, a row each, with `start`
first. `↑`/`↓` (or `tab`) move between the rows, and `enter` (or a click)
acts on the one you are on: on `start`, it starts the thread; on a choice,
it changes it, then puts you back on `start`. The footer says what `enter`
does where you are, and each choice has a key that changes it from
anywhere:

| Row | Key | Does |
| --- | --- | --- |
| `start` | `enter` | Start the thread (the row says what it would start) |
| `repo` | `ctrl-r` | Another repository: recent ones, any zoxide knows, or a path |
| `where` | `alt-w` | A new worktree, or a checkout that is there: the main one, or a worktree (one a thread works in says so) |
| `branch` | `alt-b` | The new worktree's branch (from the task by default); an existing branch, here or on the remote, continues in its own worktree |
| `base` | `alt-B` | Where a new branch starts: the remote's default branch, fetched (the default), `@` (the HEAD of the checkout you are in), or any branch |
| `agent` | `alt-a` | Claude Code or Codex |
| `mode` | `alt-r` | Work, or read-only: a review, or an investigation (from a pull request or issue) |
|  | `ctrl-e` | Write a long task in `$EDITOR` |
|  | `esc` | Cancel |

From a shell: `wvr new "fix the flaky test"`, with `--repo DIR`,
`--branch NAME`, `--base REF`, `--here`, `--agent codex`, `--edit` (write the
task in `$EDITOR`) or `--no-focus`.

New worktrees come from [Worktrunk](https://worktrunk.dev) when it is
installed (`wt switch --create`, so your `worktree-path` and hooks apply),
else from `git worktree add` at `../<repo>.<branch>`. A new branch starts from
the remote's default branch (`origin`, else the first remote) freshly fetched:
the form starts the fetch as it opens and its `base` row says
how it is going, so enter rarely waits. Offline, slower than
`WEAVER_FETCH_TIMEOUT`, or with no remote, it starts from the local default
branch and says so. The nvim on the left is
the agent's own weaver nvim, so following, the bar, reviews and `Alt v` all
work from the first prompt.

If you quit a thread's nvim, `Alt v` brings it back on the left with the
agent at its width again (when nothing else is in the window).

`wvr close` (`x` in the dashboard) closes an agent, its nvim and its
thread's window, after asking; panes you added to the window stay. For a worktree
wvr made, and only when nothing would be lost (nothing uncommitted, and
merged into the default branch or pushed), it then offers to remove it
(`wt remove`, else `git worktree remove`); the branch always stays.

When the agent exits its pane stays; enter there resumes its conversation
(`claude --resume`, `codex resume`), never sending the task twice.

### Views: GitHub and Linear

Beside home are two views: **GITHUB**, your pull requests and issues, and
**LINEAR**, your Linear issues. `Alt y` steps from a thread to home, on through them,
and back; `Tab` and `Shift Tab` step between them as tabs; the status line shows the strip of them on every window, a tab bar
(`THREADS   GITHUB   LINEAR`). A view not set up yet has a dot
(`LINEAR ·`): its window says what it needs (gh installed and signed in, a
Linear API key), and enter does that, after which it is the view. `h` there hides it
(`wvr view linear` shows it again; `$WEAVER_VIEWS` picks them outright).

On a pull request or an issue there, `Alt N` opens the new-thread form
filled in for it: its branch (the pull request's own, `12-fix-the-flaky-test`
for a GitHub issue, Linear's name for a Linear one) and the repository's
checkout, found among those weaver knows (`^r` when it knows none). The task
is yours to write. Left empty, enter starts the agent told nothing, waiting
for you, its window named for the item (`03 ENG-42 Fix it`); written, the
agent gets it with the item's description, its unresolved review comments
and its comments, fenced and marked as untrusted text
(`WEAVER_ITEM_TASK=1` writes weaver's task in for you: `Address the review
on #45`, `Implement #12`). A branch that already has a thread goes to that
thread instead.

`Alt R` is the read-only twin: on a pull request, a thread on its head
whose nvim opens on the review against what it merges into, and whose agent
reviews without changing files (Claude Code in plan mode, Codex in a
read-only sandbox); on an issue, an investigation that ends in a plan for you
to approve. The form's `alt-r` switches between the two.

From a shell, the same: `wvr new --pr 45` (or `owner/name#45`, or its URL),
`wvr new --issue 12`, `wvr new --issue ENG-42`, each with `--review`.

The views look like the board: items under their sections, Queue (yours to
do: review requested, assigned), Mine (yours, with someone else) and Watch
(what you follow); a row says the one thing to know (changes requested, a
check failing, draft, approved; urgent, or the issue's state) and shows the
thread it has, and the card says what enter does (start a thread on its
branch, or go to the one it has), then the item's fields and its
description. `s` switches GITHUB between pull requests and issues, `o`
opens an item where it lives, `/` searches, `^r` asks again (a view does
every 5 minutes, and shows what it heard last at once). A thread from a
Linear issue starts in the repository you used last.

The GitHub searches and the fields they read come from
[gh-dash](https://github.com/dlvhdr/gh-dash) (MIT, Dolev Hadar), and the
Linear queries from [linear-tui](https://github.com/k1-c/linear-tui) (MIT,
k1-c), with thanks.

### When the session goes

A thread lasts until you close it. If the session's tmux server crashes, or
you quit the session, while a thread's agent runs, the
thread is kept as lost rather than forgotten:

- `wvr` brings the `weaver` session back with its threads, each in its
  window, its number kept, its agent resuming its conversation beside a bound
  nvim (`WEAVER_RESTORE=0` lists them on home instead).
- Anywhere else, lost threads are listed last on the dashboard, under `LOST`:
  enter resumes one in a new window, `x` forgets it.
- `wvr resume` lists them; `wvr resume NAME` (a number, or a session
  id) brings one back, `--all` every one, `--forget NAME` drops one.

An agent you ended yourself (`/exit`), or whose window you closed while the
session ran on, is not lost. Agents you started by hand, outside a thread, are not
kept: `claude --resume` finds them.

So quitting everything is safe: `Ctrl q` (from any pane) quits the session
with every agent and nvim in it, and `wvr` brings its threads back next
time. While an agent is mid-turn (working, or waiting on you), whose turn
quitting would stop, it asks first. `wvr quit` does the same after asking, says what stops (turns under
way, unsaved buffers in the agents' nvims), and from outside the session
quits the `weaver` session left running behind a closed window.

### What it cleans up

Nothing weaver makes piles up. As the session starts (and on `wvr gc`):

- **Worktrees.** One weaver made for a thread goes once no thread (open or
  lost) and no pane uses it and nothing in it would be lost: no uncommitted
  or untracked files, its commits merged or pushed. Its branch stays. One
  you chose to keep as you closed its thread is yours, and stays.
- **Processes.** An nvim a crashed or killed tmux server left running (with
  its language servers) is ended, and sockets nothing listens on go.
- **Files.** Agents that are gone are forgotten; activity logs, per-turn
  snapshots and lost threads go after a month.

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
tmux marks its window until you look at it.

### The dashboard

```
┌─ [ WEAVER :: THREADS ] 5 ─────────────────── [!] 2  [x] 1  [/] 1 ─┐
│                                                                      │
│   NEEDS YOU · 2                                                      │
│   01 [!]  Add a CLI flag          Bash: python app.py --name … 11m  │
│   02 [!]  Tidy the fish config    question: Which config file…  8m  │
│                                                                      │
│   DONE · 1                                                           │
│   03 [x]  Add a README            Wrote README.md and CONTRIB…  8m  │
│                                                                      │
│   WORKING · 1                                                        │
│   04 [/]  Group the dashboard r…  2 subagents                   9m  │
│                                                                      │
│   IDLE · 1                                                           │
│   05 [x]  Audit yabai float rules Removed two dead rules; th…  47m  │
│   ⏎ JUMP  1-9  n NEW  x CLOSE  / SEARCH  s SCREEN  ? HELP            │
│ ─────────────────────────────────────────────────────────────────────│
│ Add a CLI flag · try/add-a-cli-flag · turn 3                         │
│ needs you · 11m                                                      │
│                                                                      │
│ asked      add a CLI flag to app.py, then run it to check it works   │
│ waits      Bash: python app.py --name rob                            │
│ said       Added --name and --shout; running it to check.            │
│ tasks      [x] parse the flag  [x] wire it into main  [ ] run it     │
└──────────────────────────────────────────────────────────────────────┘
```

Agents are grouped under NEEDS YOU, DONE (finished, not yet seen), WORKING
and IDLE (seen, idle or interrupted), in the order `Alt 1` visits them; the
headings' counts are the status bar's. Threads lost when their session went
come last, under LOST ([When the session goes](#when-the-session-goes)). Each row is the thread's name, one fact
(what it waits on, the command it runs, what its last turn changed and
said, its subagents),
repo/branch when there is room, and how long it has been in that state. The
states are task cells, which read without colour: `[!]` needs you,
`[x]` done, `[/]` working, `[ ]` idle, `[-]` interrupted. Titles are in ink only
where a thread needs you. `×2` after a branch means two agents share the
worktree, so their per-turn diffs mix.

The cursor never rests on a heading, and stays on its agent as the board
reloads by itself. The keys: `enter` jumps, `1`-`9` jump to that row,
`j`/`k`/`g`/`G` move, `/` searches (`esc` back), `n` starts a thread
(`wvr new`), `x` closes one (`wvr close`), `v` (or `e`) jumps and opens its
nvim, `s` (or `tab`) swaps the resume card for the agent's screen, `?` lists
them all and the keys that work anywhere, `q` or `esc` goes back to your
thread, and `Ctrl q` quits the session. With no
agents, it says how to start one.

The resume card says what the agent was asked, what it waits on or runs,
what it last said, its tasks, subagents and files, and its worktree's git
status in one line. The card sits beside the list when there is room for 150
columns, else under it, and gives way to the list
when space is short (`s` still shows it). `wvr ls | cat` prints the board
as text.

### Home

Home, window 0 of the `weaver` session, holds the dashboard at full size,
numbered by thread, so a digit means the same thread everywhere. The status
line shows the current thread (`02 Add a CLI flag · try/add-a-cli-flag`) and
the counts.

### Status bar

`wvr status` prints the counts (`[!] 1  [x] 2  [/] 3`: needs you, done and
unseen, working). weaver's tmux status line shows them in their signal
colours, set by weaver as they change, so nothing polls. A click on one goes
to it.

### Turns and the activity log

Each prompt of yours is a turn. weaver snapshots the agent's worktree at the
start and end of every turn into `refs/worktree/weaver/sessions/<session>`,
through a private index (your staging area is never touched), so each turn's
diff, and `git blame` back to the prompt that produced a line, are a git
command away; edits you make between turns are their own commit.
`wvr snapshot --reviewed` marks the worktree reviewed
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
| `<leader>an` | `:AgentNote` | Note this line (or the selected lines) for the agent |
| `<leader>as` | `:AgentSend` | Send your notes to the agent (`!` even while it works) |

As you review, note what the agent should change: `<leader>an` on a line or
a selection, in a review or in any file of its nvim, asks for the note and
marks the line. `<leader>as` sends them all as one message, each with its
file, lines and the code it is on, to the agent's prompt as if you typed it
(`wvr send`), when it waits for you to say more; while it waits on a
dialog or works, the notes stay for later. `:AgentNotes` lists them in
quickfix, `:AgentNotesClear` drops them.

In a review, `gf` opens the file at the same line in the tab of its own
worktree. Untracked files the agent created are listed like any other (your
index is never touched to do it). In diffview-plus's file panel, `w` marks a
file reviewed and `H` hides the reviewed ones.

The branch is compared against `$WEAVER_BASE`, else
`git config branch.<branch>.weaverbase`, else the default branch of the
`upstream` or `origin` remote, else `main` or `master`.

`:checkhealth weaver` says whether diffview-plus, the snacks picker and the
agent binding are in place.

Your own config can use the same one-view review:
`require('weaver.diffview').show({ key = ..., label = ..., root = ..., compare = { kind = 'branch', base = 'origin/main' } })`
(see `nvim/lua/weaver/diffview.lua`). `require('weaver.status').statusline()`
is the agent's state for a statusline, empty where the bar already shows it,
and `User WeaverEvent` autocmds receive every event from the agent.

## Settings

| Variable | Default | Does |
| --- | --- | --- |
| `WEAVER_BELL` | `1` | `needs-you`: ring only when an agent needs you; `0`: never |
| `WEAVER_SNAPSHOTS` | `1` | `0` stops the per-turn snapshots |
| `WEAVER_SNAPSHOT_MAX_KB` | `5120` | Untracked files bigger than this stay out of snapshots |
| `WEAVER_WT_MARKERS` | `1` | `0` stops setting [Worktrunk](https://github.com/max-sixty/worktrunk) branch markers (the state's cell: `[/]` working, `[!]` needs you, `[x]` done) |
| `WEAVER_DEBUG` | unset | Write hook errors to `~/.local/state/weaver/hook.log`; `trace` traces every hook there |
| `WEAVER_TMUX_SOCKET` | `weaver` | The name of weaver's own tmux server (`tmux -L <name>`) |
| `WEAVER_SESSION` | `weaver` | The session `wvr` attaches to or starts |
| `WEAVER_RESTORE` | `1` | `0`: the weaver session, starting again after its server went, lists its lost threads on home instead of resuming them |
| `WEAVER_AGENT_WIDTH` | `20%` | The agent's share of a thread window's width (`25%`), or columns (`60`) |
| `WEAVER_AGENT_MIN_COLS` | `44` | The least a percentage gives the agent, as long as nvim keeps 60 columns |
| `WEAVER_WORKTRUNK` | `1` | `0` makes threads' worktrees with plain git even when Worktrunk is installed |
| `WEAVER_CLAUDE`, `WEAVER_CODEX` | `claude`, `codex` | The command a thread's agent runs, with its arguments (`claude --model opus`) |
| `WEAVER_FETCH_TIMEOUT` | `10` | Seconds a new thread waits for its remote's fetch before starting from the local default branch |
| `WEAVER_ITEM_TASK` | `0` | `1`: a thread from a pull request or an issue starts with weaver's task written in the form (`Review #45`, `Implement ENG-42`), sent with the item as the agent's first prompt; else the task is empty, and an empty one starts the agent told nothing |
| `WEAVER_VIEWS` | both, but hidden ones | The views beside home: `github`, `linear`, both, or `none` |
| `LINEAR_API_KEY` | unset | The Linear API key weaver reads Linear with, over the one `wvr linear login` keeps in the Keychain |
| `WEAVER_SELF_SETUP` | `1` | `0`: `wvr` leaves the hooks and plugins as they are as it starts (`wvr setup` still does it) |

## Development

The engine is Go (`cmd/wvr`, `internal/`), the Neovim side Lua
(`nvim/`); they meet at the contracts in [docs/design.md](docs/design.md),
and [docs/decisions.md](docs/decisions.md) says why things are as they are.

```sh
test/run.sh
```

To run a checkout's build, `./install.sh` builds it into `~/.local/bin/wvr`,
ahead of Homebrew's on PATH (it needs Go; safe to rerun, `--dry-run` shows
what it would do); remove that file to go back to Homebrew's.

runs, in a few minutes, each with its own state and nothing of yours
touched (they need jq and python3 as well as weaver's own dependencies):

- `go vet` and `go test`, which include the contracts between the Go half,
  its Neovim side and the design doc (the states and their cells);
- `test/nvim/run.sh`: the Neovim specs, headless (set `WEAVER_TEST_PLUGINS`
  to a directory holding `diffview-plus.nvim` if lazy.nvim's plugin
  directory doesn't have it);
- `test/hooks.sh`: the suites in `test/suites/` (the hooks' latency, their
  state machine and its robustness, Alt 1, the status counts, subagents,
  per-turn snapshots, other agents' events, the installer, the dashboard's
  renderings), on a throwaway tmux server with three fake agents and a client
  attached (`test/hooks.sh SUITE` runs one);
- `test/tmux.sh`: weaver on tmux end to end. `wvr` starts its session on a
  throwaway tmux server in a pseudo-terminal and the tests press its keys
  there: threads, hooks and their late async events, Alt y, Alt v, Alt 1,
  Cmd+D, Alt N, the board, seen as you look, a narrower window, crashes and
  their threads resumed, Ctrl q, and threads from pull requests and issues
  in the GITHUB and LINEAR views (with a fake `gh`, Keychain and Linear API).

`test/soak/nvim-exit.sh` is a long run, by hand: nvims weaver starts, killed
together round after round, none of which may hang.

## AI use

wvr is built with AI coding agents, Claude Code among them: they write
most of its code, tests and documentation. I direct the work, make the design
decisions, review what lands and use weaver every day. Read the code with
that in mind, and open an issue for anything that looks wrong.

## Credits

The GITHUB view's searches of GitHub, and the fields of a pull request they
read, come from [gh-dash](https://github.com/dlvhdr/gh-dash) by Dolev Hadar
(MIT); the LINEAR view's queries, and the fields of an issue they read, from
[linear-tui](https://github.com/k1-c/linear-tui) by k1-c (MIT);
[diffview-plus.nvim](https://github.com/dlyongemallo/diffview-plus.nvim)
(GPL-3.0) is what reviews are built on, fetched rather than carried.

## Licence

MIT: see [LICENSE](LICENSE).
