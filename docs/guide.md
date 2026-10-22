# weaver guide

This guide covers weaver in detail. For what weaver is, how to install it and
a first thread, start with the [README](../README.md).

- [The weaver session](#the-weaver-session)
- [Threads](#threads)
- [Pull requests and issues](#pull-requests-and-issues)
- [The board](#the-board)
- [Keeping threads](#keeping-threads)
- [What weaver tracks](#what-weaver-tracks)
- [Neovim](#neovim)
- [Mouse](#mouse)
- [Setup](#setup)
- [Settings](#settings)
- [Development](#development)

## The weaver session

Outside tmux, `wvr` starts the tmux session `weaver`, or attaches to it if it
is already running. To use another session name, set `WEAVER_SESSION` or run
`wvr start NAME`. Inside tmux, `wvr` opens the board instead.

The session runs on a tmux server of its own (`tmux -L weaver`), so it never
touches your own tmux sessions. That server reads weaver's tmux config, then
`~/.config/weaver/tmux.conf` if you have one. Closing the terminal only
detaches from the session; `wvr` picks up where you were.

The session's windows are:

- **home**, window 0: the board of every thread, full size;
- **one window per thread**, its index the thread's number: the thread's
  Neovim on the left, the agent on the right;
- **the views**, GITHUB and LINEAR, beside home
  ([Pull requests and issues](#pull-requests-and-issues)).

There is no window list. Instead, one status line shows the current thread
with its repository and branch, the counts of agents in each state, the strip
of views (`THREADS   GITHUB   LINEAR`), and tmux's mode: `[NRM]`, `[PFX]` while
the prefix is down, or `[CPY]` in copy mode.

tmux's own keys work behind its prefix, `ctrl+b`: `ctrl+b d` detaches, and
`ctrl+b [` scrolls back.

To split a pane in weaver's tmux, bind keys in your terminal to send
`ESC [ 100 ; 9 ~` (split right) and `ESC [ 100 ; 10 ~` (split down). In
Ghostty, for example, this keeps `cmd+d` for Ghostty's own splits and gives
weaver `cmd+alt+d`:

```
keybind = super+alt+d=csi:100;9~
keybind = super+alt+shift+d=csi:100;10~
```

Outside weaver's tmux, those keys do nothing.

Agents you start by hand in any tmux session also report to weaver, and `wvr`
inside such a session opens a board of that session's agents.

## Threads

A thread is one task for one agent, in a window of its own: the agent's
Neovim on the left and the agent on the right, taking a fifth of the width.
It works in a worktree of its own, or in a checkout you choose. The window is
named after the thread (`01 Add a CLI flag · repo/add-a-cli-flag`), and
opening a thread puts you on its agent.

### Starting a thread

`alt+N`, `wvr new`, or `n` on the board opens the new-thread form, in the
repository of the pane you were on (else the one you used last).

You type the task at the top. Under it, each choice has a row, with `start`
first. `↑` and `↓` (or `tab`) move between rows, and `enter` (or a click) acts
on the row you are on: on `start` it starts the thread; on a choice it changes
that choice, then puts you back on `start`. The footer says what `enter` does
where you are. Each choice also has a key that changes it from anywhere:

| Row | Key | Does |
| --- | --- | --- |
| `start` | `enter` | Start the thread (the row says what it would start) |
| `name` | `alt+n` | What the board and its window call it: by default a pull request's or issue's number and title (read-only, after `review :: `), else the title its agent gives it |
| `repo` | `ctrl+r` | Choose another repository: recent ones, any zoxide knows, or a path |
| `where` | `alt+w` | A new worktree, or a checkout that exists: the main one or a worktree (one a thread already works in says so) |
| `branch` | `alt+b` | The new worktree's branch, named from the task by default. An existing branch, here or on the remote, continues in its own worktree |
| `base` | `alt+B` | Where a new branch starts: the remote's default branch, freshly fetched (the default); `@`, the HEAD of the checkout you are in; or any branch |
| `agent` | `alt+c` | Claude Code or Codex |
| `mode` | `alt+m` | Work, or read-only: a review, or an investigation of a pull request or issue |
|  | `ctrl+e` | Write a long task in `$EDITOR` |
|  | `esc` | Cancel |

From a shell, `wvr new "fix the flaky test"` does the same. Its options are
`--name NAME`, `--repo DIR`, `--branch NAME`, `--base REF`, `--here` (the current checkout),
`--agent codex`, `--edit` (write the task in `$EDITOR`) and `--no-focus`.

### Worktrees and branches

weaver creates new worktrees with [Worktrunk](https://worktrunk.dev) when it
is installed (`wt switch --create`), so your Worktrunk `worktree-path` and
hooks apply. Without Worktrunk, or with `WEAVER_WORKTRUNK=0`, it uses `git
worktree add` at `../<repo>.<branch>`.

A new branch starts from the remote's default branch (`origin`, else the first
remote), freshly fetched. The form starts that fetch as it opens, and its
`base` row shows how the fetch is going, so `enter` rarely waits. If you are
offline, the fetch takes longer than `WEAVER_FETCH_TIMEOUT` seconds, or there
is no remote, the branch starts from the local default branch, and the form
says so.

### Working in a thread

The Neovim on the left belongs to the thread's agent: it follows the agent's
edits, shows its state and reviews its turns ([Neovim](#neovim)). If you quit
it, `alt+v` brings it back on the left, with the agent at its usual width,
as long as nothing else is in the window.

### Restarting an agent

After you update Claude Code or Codex, running agents carry on with the old
version. `R` on the board, or `wvr restart`, starts a thread's agent again in
its own pane, resuming the same conversation (`claude --resume`, `codex
resume`): its window, Neovim and number stay, and the task is never sent
twice. `wvr restart --all` restarts every thread's agent, from any shell. An
agent that is working, or waiting on you, is left alone, since restarting it
would stop its turn; `--force` restarts it all the same.

### Renaming a thread

A thread is called by the name it started with: the form's `name` row (or
`wvr new --name`), which for a pull request or issue is its number and title
(`#45 Add a flag`, or `review :: #45 Add a flag` read-only) unless you change
it. With none, it is called by the title
its agent gives it (Claude Code names its conversations as they go), else by
its task. `r` on the board, or `wvr rename [PANE] [NAME]`, gives it a name of
your own later, on the board, its card, its window and the status line; with
no name, it asks. An empty name gives the agent's title back. The name stays with the thread when it is lost and
resumed.

### Closing a thread

`x` on the board, or `wvr close`, closes a thread's agent, its Neovim and its
window, after asking. Panes you added to the window stay.

If weaver created the thread's worktree, and nothing in it would be lost (no
uncommitted changes, and its commits merged into the default branch or
pushed), weaver then offers to remove it, with `wt remove` or `git worktree
remove`. The branch always stays.

## Pull requests and issues

Beside home are two views: **GITHUB**, your pull requests and issues, and
**LINEAR**, your Linear issues. `tab` and `shift+tab` step between home and
the views like tabs, both in their windows and in `alt+y`'s popover over a
thread ([The board](#the-board)). On home, `alt+y` steps on through the
views, then back to the thread you came from.

A view that is not set up yet shows a dot on the status line (`LINEAR ·`).
Its window says what it needs, such as gh installed and signed in, or a Linear
API key, and `enter` sets that up. `h` in a view hides it; `wvr view linear`
shows it again, and `WEAVER_VIEWS` chooses the views outright.

Each view groups its items like the board:

- **Queue**: yours to act on, such as reviews requested from you or issues
  assigned to you;
- **Mine**: yours, waiting on someone else;
- **Watch**: what you follow.

A row shows the one thing to know about an item (changes requested, a check
failing, draft, approved; for an issue, urgent or its state) and the thread
it has, if any. The card says what `enter` does, then the item's fields and
description.

| Key | Does |
| --- | --- |
| `enter`, `alt+N` | A thread from the item, or go to the thread it has ([A thread from an item](#a-thread-from-an-item)) |
| `alt+m` | A read-only thread from it |
| `o` | Open it in the browser |
| `s` | GITHUB: switch between pull requests and issues |
| `m` | More of a section that shows only some (its heading says `20 of 57`, or `30+` for Linear), until the next refresh; `enter` on its last line, `⏎ more`, does too |
| `f` | A filter: a search of your own, shown first as SEARCH; for GitHub, assigned to you, a review asked of you or your teams, opened by you, mentioning you, or what is open in a repository or with a label weaver has seen; for Linear, assigned to you, created by you, subscribed to, or a team's. `f` again offers to clear it |
| `/` | Search what the view holds (its number, whole title, author, repository, branch and labels; for Linear, its team and people), best match first; a number or a reference (`45`, `#45`, `o/r#45`, `ENG-42`) finds that item first, exactly |
| `esc` | End the SEARCH, if there is one; else back to your thread |
| `ctrl+r` | Ask again |

Each view refreshes every 5 minutes, its SEARCH too, and shows what it
last heard straight away, and how old that is. A section GitHub or Linear
fails to answer keeps what it showed, its heading marked `failed` with how
old that is, and the header says why. After you upgrade weaver, a view
restarts in the new version, as home does ([Upgrading](#upgrading)).

### A thread from an item

On a pull request or an issue, `alt+N` opens the new-thread form filled in for
it: its branch (the pull request's own, `12-fix-the-flaky-test` for a GitHub
issue, or Linear's branch name for a Linear issue) and the repository's
checkout, if weaver knows one (`ctrl+r` to choose one when it doesn't). A thread
from a Linear issue starts in the repository you used last. If the branch
already has a thread, weaver goes to that thread instead.

The task is yours to write:

- **Left empty**, `enter` starts the agent with no prompt, waiting for you,
  and names its window after the item (`03 ENG-42 Fix it`).
- **Written**, the agent gets your task with the item's description, its
  unresolved review comments and its comments. weaver fences that text and
  marks it as untrusted, since anyone who can comment wrote it.

With `WEAVER_ITEM_TASK=1`, weaver writes a task in for you (`Address the
review on #45`, `Implement #12`).

`alt+m` is the read-only version. On a pull request, it starts a thread on the
pull request's branch whose Neovim opens on a review against the branch it
merges into, and whose agent reviews without changing files (Claude Code in
plan mode, Codex in a read-only sandbox). On an issue, it starts an
investigation that ends in a plan for you to approve. The form's `alt+m`
switches between work and read-only.

From a shell: `wvr new --pr 45` (or `owner/name#45`, or the pull request's
URL), `wvr new --issue 12`, or `wvr new --issue ENG-42`, each with `--review`
for the read-only version.

## The board

The board is home's main content, and `wvr` opens it in any other tmux session
too. The [README](../README.md) shows what it looks like.

In a thread, `alt+y` opens the board in a popover over the thread, covering
most of the screen, with its cursor on the thread you are in. Its keys work
as they do on home; `tab` and `shift+tab` step to the views inside it, and
`esc`, `q` or `alt+y` close it, leaving you where you were. Choosing
something that goes elsewhere or opens a dialog (a jump, `n`, `x`, `a`, or
`enter` on a pull request) closes the popover first, then does it. To go to
the home window itself, click THREADS on the status line, or use `alt+j` and
`alt+k`.

Agents are grouped under these headings:

- **NEEDS YOU**: waiting on you;
- **DONE**: finished, and you haven't looked yet;
- **WORKING**: running a turn;
- **IDLE**: idle or interrupted, or finished and seen;
- **REVIEWS**: read-only threads (reviews and investigations), whatever
  their state, since you watch them less closely; each row's cell still
  shows its state, and `alt+1` still goes to one that needs you;
- **LOST**: threads whose session went, and that weaver hasn't brought back
  ([Keeping threads](#keeping-threads)).

Each row shows the thread's number and name, its state as a cell (`[!]`,
`[x]`, `[/]`, `[ ]`, `[-]`), one fact about it (what it waits on, the command
it runs, what its last turn changed and said, or its subagents), its
repository and branch when there is room, and how long it has been in that
state. Titles are bright only where a thread needs you. `×2` after a branch
means two agents share the worktree, so their per-turn diffs mix.

The headings' counts are the status line's. The cursor never rests on a
heading, and stays on its agent as the board reloads itself.

When a thread's agent exits (you quit it, or it fails as it starts), its
pane stays with what it said last, and its row shows it exited: `R` starts
its agent again on its conversation, `x` closes the thread.

| Key | Does |
| --- | --- |
| `enter`, `1`–`9` | Jump to the thread (`1`–`9`: that row) |
| `j` `k`, `g` `G` | Move; first, last |
| `/` | Search (`esc` to go back) |
| `n` | Start a thread (`wvr new`) |
| `x` | Close the thread (`wvr close`); on a lost thread, forget it |
| `r` | Rename the thread ([Renaming a thread](#renaming-a-thread)) |
| `R` | Restart its agent, resuming its conversation ([Restarting an agent](#restarting-an-agent)) |
| `a` | Adopt the agent, if you started it by hand; elsewhere, pick a conversation to resume as a thread ([Adopting](#adopting-a-conversation)) |
| `v`, `e`, `ctrl+e` | Jump to the thread and open its Neovim |
| `s` | Swap the card for the agent's screen, and back |
| `tab`, `shift+tab` | The next, previous view |
| `ctrl+r` | Reload |
| `?` | Every key, including those that work anywhere (`?` again to go back) |
| `q`, `esc` | Back to your thread |
| `ctrl+q` | Quit the session |

The card shows what the agent was asked, what it waits on or runs, what it
last said, its tasks, subagents and files, and its worktree's git status. It
sits beside the list when the pane is at least 150 columns wide, else under
it, and gives way to the list when space is short (`s` still shows it).

`wvr ls | cat` prints the board as text.

## Keeping threads

### When the session goes

A thread lasts until you close it. If tmux crashes, you quit the session, or
the machine restarts while a thread's agent runs, weaver keeps the thread as
*lost* rather than forgetting it:

- `wvr` brings the `weaver` session back with its threads, each in its window
  with its number, its agent resuming its conversation beside its Neovim.
  With `WEAVER_RESTORE=0`, it lists them on home instead.
- Lost threads it doesn't bring back by itself (from another session, or
  with `WEAVER_RESTORE=0`) are listed last on the board, under LOST: `enter`
  resumes one in a new window, and `x` forgets it.
- `wvr resume` lists lost threads. `wvr resume NAME` (a number, a name or a
  session id) brings one back, `--all` brings back every one, and `--forget
  NAME` drops one.

An agent you ended yourself (`/exit`), or whose window you closed while the
session ran on, is not lost.

### Adopting a conversation

weaver keeps only threads, so an agent you started yourself isn't kept until
you adopt it. Adopting makes a conversation a thread, kept and brought back
like any other:

- **An agent you started by hand**, in a pane weaver follows: `a` on its row,
  or `! wvr adopt` typed into the agent, makes it a thread where it runs. In
  the weaver session it moves to a window of its own at its number, with a
  Neovim on its left. It is not restarted, so a turn in progress carries on.
- **A conversation that isn't running**, such as one you ran outside tmux
  before a restart: `a` anywhere else on the board lists your recent Claude
  Code conversations, and `enter` resumes one as a thread, in the directory
  it started in. Type or paste a session id (or its first four or more
  characters) to find one, listed or not. From any shell, inside tmux or not, `wvr adopt SESSION-ID`
  (or its first four or more characters) does the same, into the weaver
  session; `wvr adopt` alone opens the list there.

A conversation still running somewhere else is marked `running`. `/exit` it
there before adopting it, so that two processes don't share one conversation.

### Quitting

Because threads come back, quitting everything is safe. `ctrl+q`, from any
pane, quits the session with every agent and Neovim in it, and `wvr` brings
the threads back next time. While an agent is mid-turn (working, or waiting
on you), quitting would stop that turn, so `ctrl+q` asks first.

`wvr quit` quits after asking, and says what would stop: turns in progress,
and unsaved buffers in the agents' Neovims. From outside the session it quits
a `weaver` session left running behind a closed terminal.

### What weaver cleans up

As the session starts, and on `wvr gc`, weaver removes what it no longer
needs:

- **Worktrees.** A worktree weaver made for a thread goes once no thread
  (open or lost) and no pane uses it, and nothing in it would be lost: no
  uncommitted or untracked files, and its commits merged or pushed. Its
  ignored files (an `.env`, local settings) are kept a month in
  `trash/` in weaver's state directory first. Its branch stays. A worktree you chose to keep when you closed its thread is yours, and
  stays.
- **Processes.** A Neovim that a crashed or killed tmux server left running,
  with its language servers, is ended, and sockets nothing listens on are
  removed. So is what an agent left running in the background from its
  worktree (a dev server, a watcher) once its thread has gone: closing a
  thread ends it, and the next sweep catches any a crash or restart left.
  A daemon that runs from elsewhere (Codex's app server, say) stays, even
  when a gone thread's command runs under it (that command alone ends). `wvr gc
  --dry-run` lists what would end, with its memory; `gc.log` in weaver's
  state directory says what did.
- **Files.** Agents that are gone are forgotten. Activity logs, per-turn
  snapshots and lost threads go after a month.

## What weaver tracks

Each agent is in one of these states:

- **working**: running a turn;
- **needs you**: a permission dialog, a question, an MCP prompt or an API
  error. It stays until you answer;
- **done**: finished. It counts as unread until you look at the agent;
- **interrupted**: you pressed Esc, or rejected a dialog;
- **idle**: waiting for your next prompt.

Compaction, `/clear` and resuming keep the right state. A session nested
inside an agent (`claude -p`, an SDK script) is ignored, and an agent killed
without a word drops off the board. Where the hooks can't see what happened
(Esc while the model writes, a long tool you approved), weaver refines the
state from Claude Code's own session files.

An agent that starts needing you, or finishes, rings the terminal bell, so
tmux marks its window until you look at it. `WEAVER_BELL` changes that.

### Status line

`wvr status` prints the counts: `[!] 1  [x] 2  [/] 3` means one agent needs
you, two finished that you haven't seen, and three are working. weaver's tmux
status line shows the same counts in colour, updated as they change rather
than polled. Clicking a count goes to the agent it counts
([Mouse](#mouse)).

### Turns and the activity log

Each prompt of yours starts a turn. weaver snapshots the agent's worktree at
the start and end of every turn into `refs/worktree/weaver/sessions/<session>`,
through a private index, so your staging area is never touched. Each turn's
diff, and `git blame` back to the prompt that produced a line, are a git
command away. Edits you make between turns are their own commit.

`wvr snapshot --reviewed` marks the worktree as reviewed
(`refs/worktree/weaver/reviewed`). Everything an agent does is also logged to
`~/.local/state/weaver/log/<session>.jsonl`. The formats are in
[design.md](design.md).

## Neovim

The Neovim weaver opens for an agent loads your own config, then:

- **follows the agent, politely.** An edit by the agent, its subagents or its
  Bash commands is followed only once you have been idle for about three
  seconds in normal mode, in a file window, and not reviewing a diff. Until
  then the bar shows it as `→ file`, and `<leader>aj` goes there at once. It
  never types into Neovim, and never replaces a buffer with unsaved changes,
  a terminal, quickfix or a git window. It brings other tabs up to date
  without switching to them. Of the files it opened that are out of sight,
  it keeps the ten most recent loaded (`vim.g.weaver_follow_keep`); older
  ones are unloaded, unless you typed in them, and a language server left
  serving none of your buffers is stopped, so a long thread's Neovim doesn't
  grow without end.
- **gives each worktree its own tab**, closed when the worktree is removed. A
  new worktree takes a tab with nothing in it (the start screen, or an empty
  one) before it opens another.
- **reloads files** as soon as they change on disk, never over unsaved
  changes.
- **shows the agent at the top of each file window**: its state, turn, task
  progress, the edit waiting to be followed, and what it was asked or is
  waiting on. Once a turn is over, it shows how many files changed and the
  key to review them (`2 files ␣at`).

### Reviewing

Reviews open in one
[diffview-plus](https://github.com/dlyongemallo/diffview-plus.nvim) view. A
scope's key opens the view; pressing it again from elsewhere brings the view
back, refreshed; pressing it inside the view closes it; and another scope's
key replaces it. The view refreshes itself as the agent edits.

| Key | Command | Shows |
| --- | --- | --- |
| `<leader>at` | `:AgentReview turn` | The agent's last turn (the turn so far, while it works) |
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
| `<leader>af` | `:AgentFollow` | Turn following on or off |
| `<leader>an` | `:AgentNote` | Note this line, or the selected lines, for the agent |
| `<leader>as` | `:AgentSend` | Send your notes to the agent (`!`: even while it works) |

As you review, note what the agent should change. `<leader>an` on a line or a
selection, in a review or in any file of its Neovim, asks for the note and
marks the line. `<leader>as` sends every note as one message, each with its
file, lines and the code it is on, to the agent's prompt as if you had typed
it (`wvr send`). It sends only when the agent is waiting for your next
prompt; while the agent works or waits on a dialog, the notes stay for later.
`:AgentNotes` lists them in quickfix, and `:AgentNotesClear` drops them.

In a review, `gf` opens the file at the same line in its own worktree's tab.
Untracked files the agent created are listed like any other, without touching
your index. In diffview-plus's file panel, `w` marks a file reviewed and `H`
hides the reviewed ones.

A branch is compared against `$WEAVER_BASE`, else `git config
branch.<branch>.weaverbase`, else the default branch of the `upstream` or
`origin` remote, else `main` or `master`.

`:checkhealth weaver` says whether diffview-plus, the snacks picker and the
agent binding are in place.

### In your own Neovim

wvr keeps a copy of its Neovim plugin at `~/.local/share/weaver/nvim`. The
Neovims weaver opens load it themselves. To use its reviews or statusline in
your other Neovims, add it to your config; with lazy.nvim:

```lua
{ dir = (vim.env.XDG_DATA_HOME or vim.env.HOME .. '/.local/share') .. '/weaver/nvim', name = 'weaver', lazy = false }
```

Then:

- `require('weaver.diffview').show({ key = ..., label = ..., root = ..., compare = { kind = 'branch', base = 'origin/main' } })`
  opens the same one-view review (see `nvim/lua/weaver/diffview.lua`);
- `require('weaver.status').statusline()` is the agent's state for a
  statusline, empty where the bar already shows it;
- `User WeaverEvent` autocmds receive every event from the agent.

## Mouse

Every key has a place to click, as in btop or lazygit, and every click has a
key:

- **Hints are buttons.** A key shown under a list (`n NEW`, `x CLOSE`, `esc
  CANCEL`) does what it says when clicked, as do a card's choices.
- **Rows.** A click selects a row, and a second opens it, like `enter`. In the
  form and its pickers, one click is enough.
- **Right click** opens a menu of what can be done there, each item with its
  key: on a row of the board or a view, that row's actions; on an agent, its
  thread's; on the status line, weaver's (home, the views, the next agent that
  needs you, a new thread, quit). tmux's own menu stays on `Alt` and a right
  click.
- **The status line.** `THREADS`, `GITHUB` and `LINEAR` go there. The counts
  go to the agent that needs you (`[!]`, `[x]`), or home (`[/]`).
- **The wheel** scrolls what is under it: a list, a card, or a pane's history,
  with a scrollbar to drag while you are scrolled back.

Dragging in a pane selects and copies, in tmux's copy mode. To select the
terminal's own way, hold `Shift` (Ghostty) or `Option` (iTerm2) as you drag.

## Setup

### What wvr sets up

`wvr` sets itself up as it starts, when it is the `wvr` on your PATH:

- **Hooks.** Claude Code's hooks (`~/.claude/settings.json`) and Codex's
  (`~/.codex/hooks.json`, when you have Codex) run `wvr`. It backs each file
  up before it first changes it, replaces any older weaver hooks, and leaves
  everyone else's alone. The hooks run the binary directly, without a shell;
  those nobody needs to wait for run in the background. Codex runs them only
  after you trust them in its `/hooks`.
- **Review plugin.** It fetches
  [diffview-plus.nvim](https://github.com/dlyongemallo/diffview-plus.nvim),
  which reviews are built on, for Neovims whose config has no diffview of
  their own.

It prints one line whenever it changes a file. `wvr setup --dry-run` shows
what it would change, and `WEAVER_SELF_SETUP=0` turns this off.

Claude Code reads hooks only when a session starts, so restart running
sessions after the hooks change. weaver's tmux needs no config of yours.

### Signing in

The views sign in where you use them. A view that isn't set up has a dot on
the status line; going there offers the one key that signs it in: `gh auth
login` for GitHub, or `wvr linear login`, which asks for a Linear API key and
keeps it in the macOS Keychain.

### Checking and fixing

`wvr doctor` says what weaver works with, what is set up, and the command that
fixes each problem. `wvr doctor --fix` does what it can: Homebrew installs
what is missing or too old, weaver sets up its hooks and fetches its plugin,
and each sign-in is offered.

### Upgrading

`brew upgrade` keeps weaver current, and `brew install --HEAD
robbowland/kegs/weaver` installs what is on main, unreleased.

After an upgrade, a running weaver session reads weaver's tmux config again
the next time `wvr` attaches to it, and home and the views restart in the
`wvr` on your PATH at their next reload, or within a few seconds. That
includes switching builds: `./install.sh`'s `~/.local/bin/wvr` ahead of
Homebrew's, and Homebrew's again once you remove it.

### Files

wvr unpacks the files it carries under `~/.local/share/weaver/runtime`, and
keeps a copy of its Neovim plugin at `~/.local/share/weaver/nvim`. Its state,
including agents' states, lost threads and activity logs, lives under
`~/.local/state/weaver`.

## Settings

Environment variables change weaver's defaults.

| Variable | Default | Does |
| --- | --- | --- |
| `WEAVER_CLAUDE`, `WEAVER_CODEX` | `claude`, `codex` | The command a thread's agent runs, with its arguments (`claude --model opus`) |
| `WEAVER_SESSION` | `weaver` | The session `wvr` attaches to or starts |
| `WEAVER_TMUX_SOCKET` | `weaver` | The name of weaver's own tmux server (`tmux -L <name>`) |
| `WEAVER_RESTORE` | `1` | `0`: when the weaver session starts again after its server went, list its lost threads on home instead of resuming them |
| `WEAVER_AGENT_WIDTH` | `20%` | The agent's share of a thread window's width (`25%`), or columns (`60`) |
| `WEAVER_AGENT_MIN_COLS` | `44` | The least a percentage gives the agent, as long as Neovim keeps 60 columns |
| `WEAVER_BELL` | `1` | `needs-you`: ring only when an agent needs you; `0`: never |
| `WEAVER_FETCH_TIMEOUT` | `10` | Seconds a new thread waits for its remote's fetch before starting from the local default branch |
| `WEAVER_WORKTRUNK` | `1` | `0`: create worktrees with plain git even when Worktrunk is installed |
| `WEAVER_WT_MARKERS` | `1` | `0`: stop setting [Worktrunk](https://github.com/max-sixty/worktrunk) branch markers (the state's cell: `[/]` working, `[!]` needs you, `[x]` done) |
| `WEAVER_SNAPSHOTS` | `1` | `0`: stop the per-turn snapshots |
| `WEAVER_SNAPSHOT_MAX_KB` | `5120` | Untracked files bigger than this stay out of snapshots |
| `WEAVER_VIEWS` | both, except hidden ones | The views beside home: `github`, `linear`, both, or `none` |
| `WEAVER_ITEM_TASK` | `0` | `1`: a thread from a pull request or issue starts with weaver's task written into the form (`Review #45`, `Implement ENG-42`); otherwise the task is empty, and an empty task starts the agent with no prompt |
| `LINEAR_API_KEY` | unset | The Linear API key to use instead of the one `wvr linear login` keeps in the Keychain |
| `WEAVER_SELF_SETUP` | `1` | `0`: `wvr` leaves the hooks and plugins alone as it starts (`wvr setup` still sets them up) |
| `WEAVER_DEBUG` | unset | Write hook errors to `~/.local/state/weaver/hook.log`; `trace` traces every hook there |

## Development

The engine is Go (`cmd/wvr`, `internal/`) and the Neovim side is Lua
(`nvim/`). They meet at the contracts in [design.md](design.md), and
[decisions.md](decisions.md) says why things are as they are.

`./install.sh` builds a checkout into `~/.local/bin/wvr`, ahead of Homebrew's
on PATH. It needs Go, is safe to rerun, and `--dry-run` shows what it would
do. Remove `~/.local/bin/wvr` to go back to Homebrew's build.

`test/run.sh` runs every suite in a few minutes, each with its own state and
nothing of yours touched. Besides weaver's own dependencies, they need jq and
python3:

- `go vet` and `go test`, including the contracts between the Go half, its
  Neovim side and the design doc (the states and their cells);
- `test/nvim/run.sh`: the Neovim specs, headless. Set `WEAVER_TEST_PLUGINS`
  to a directory holding `diffview-plus.nvim` if lazy.nvim's plugin directory
  doesn't have it;
- `test/hooks.sh`: the suites in `test/suites/`: the hooks' latency, their
  state machine and its robustness, `alt+1`, the status counts, subagents,
  per-turn snapshots, other agents' events, the installer and the board's
  renderings. They run on a throwaway tmux server with three fake agents and
  a client attached; `test/hooks.sh SUITE` runs one;
- `test/tmux.sh`: weaver on tmux end to end. `wvr` starts its session on a
  throwaway tmux server in a pseudo-terminal, and the tests press its keys:
  threads, hooks and their late async events, `alt+y`, `alt+v`, `alt+1`,
  split keys, `alt+N`, the board, crashes and their threads resumed, adopting
  and renaming,
  `ctrl+q`, and threads from pull requests and issues in the views (with a
  fake gh, Keychain and Linear API).

`test/soak/nvim-exit.sh` is a long run to start by hand: Neovims weaver
starts, killed together round after round, none of which may hang.
