# weaver guide

This guide covers weaver in detail. For what weaver is, how to install it and
a first thread, start with the [README](../README.md).

- [The weaver session](#the-weaver-session)
- [Threads](#threads)
- [Pull requests and issues](#pull-requests-and-issues)
- [The board](#the-board)
- [Keeping threads](#keeping-threads)
- [What weaver tracks](#what-weaver-tracks)
- [Review](#review)
- [Mouse](#mouse)
- [Setup](#setup)
- [Settings](#settings)
- [Development](#development)

## The weaver session

weaver runs on [Rex](https://www.superlogical.com/rex). Outside weaver's
session, `wvr` starts a Rex session named `weaver` (home in its first
window), or shows it when it is already running; each thread is a Rex
session of its own beside it. To use another
session name, set `WEAVER_SESSION` or run `wvr start NAME`. Inside it, `wvr`
opens the board instead. Rex shows a session only when asked by an app with
Remote Control on (Rex's settings, Rex Server); otherwise switch to it in Rex
yourself. The session runs in Rex's server: closing the app leaves it
running, and `wvr` shows it again.

The weaver session's windows are:

- **home**, the first window: the board of every thread, full size;
- **the views**, GITHUB and LINEAR, beside home
  ([Pull requests and issues](#pull-requests-and-issues)).

A thread's session is labelled with its name (its branch) as it is made;
its first window has the thread's review on the left and the agent on the
right, and helpers its agent starts get windows beside it. Renaming the
session in Rex changes only its label: weaver finds its threads by Rex's
ids.

Rex binds keys in its app, so weaver's are a Lua file: `wvr` writes
`weaver.lua` beside Rex's `init.lua` (`~/.config/rex/`) as it starts, has
Rex read it when it is new to Rex, and says to load it by adding
`require("weaver")` to `init.lua` when that does not. It binds `alt+y`,
`alt+Y`, `alt+N`, `alt+v`, `alt+V`, `alt+1`, `alt+j`/`alt+k`, `alt+h`/`alt+l` and
`ctrl+q`, which asks every time before it quits. Rex's configuration is one
for every session, so the keys are weaver's only in the Rex sessions weaver
made; in any other they are the terminal's. In weaver's popups (a new
thread's form, a card, alt+y's board) they are the popup's. Rex runs them
without your shell's environment: weaver's settings that they read
(`XDG_STATE_HOME`, `WEAVER_AGENT_WIDTH` and the like) are the ones set where
you last ran `wvr`.

Rex takes weaver's keys before Option types anything, so they work whatever
Option sends; programs' own Alt keys (`alt+m` in a view's list) need Option
to send Alt: run Change Option Key Behavior from Rex's command palette and
have Option send Alt. Rex's default, Automatic, sends Alt only on a U.S.
keyboard layout; on any other (ABC, British, ...) Option types special
characters.

The Rex app shows the window it is asked to, so weaver's moves (its keys,
enter on the board, a new thread) ask it, which it does only with Remote
Control on. When a key does nothing, `rex server logs` has what wvr said.

Rex has no status line or message line: Rex shows each agent's state itself,
from what Claude Code reports to it, the board shows the rest, and weaver's
messages show for a moment at the foot of the window.

When Rex restarts (an update, say), it brings its sessions back without
what ran in them, a shell where each program was. `wvr` then starts the
`weaver` session again, its threads back on their conversations, and ends
the one Rex brought back.

weaver acts only on the session it made: agents you start by hand in another
Rex session do not report to it (adopt them, below). It follows Claude
Code.

## Threads

A thread is one task for one agent, in a window of its own: its review
(tuicr) on the left and the agent on the right, taking a fifth of the width.
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
| `mode` | `alt+m` | Work, or read-only: a review, or an investigation of a pull request or issue |
|  | `ctrl+e` | Write a long task in `$EDITOR` |
|  | `esc` | Cancel |

From a shell, `wvr new "fix the flaky test"` does the same. Its options are
`--name NAME`, `--repo DIR`, `--branch NAME`, `--base REF`, `--here` (the current checkout),
`--edit` (write the task in `$EDITOR`) and `--no-focus`.

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

The review on the left shows what the thread's branch has changed, as the
agent changes it ([Review](#review)). If you close it, `alt+v` brings it
back on the left, with the agent at its usual width, as long as nothing else
is in the window.

### Restarting an agent

After you update Claude Code, running agents carry on with the old
version. `R` on the board, or `wvr restart`, starts a thread's agent again in
its own pane, resuming the same conversation (`claude --resume`): its window, review and number stay, and the task is never sent
twice. `wvr restart --all` restarts every thread's agent, from any shell. An
agent that is working, or waiting on you, is left alone, since restarting it
would stop its turn; `--force` restarts it all the same.

### Renaming a thread

A thread is called by the name it started with: the form's `name` row (or
`wvr new --name`), which for a pull request or issue is its number and title
(`#45 Add a flag`, or `review :: #45 Add a flag` read-only) unless you change
it. With none, it is called by the title
its agent gives it (Claude Code names its conversations as they go), else by
its task. `r` on the board gives it a name of your own, there and then: the
board's search line becomes `rename 02 › its name`, `enter` saves and `esc`
leaves it as it was. The name shows on the board, its card and its window.
An empty name gives the agent's title back, and saving the
agent's title as it is leaves it the agent's, to change as it goes. `wvr
rename [PANE] [NAME]` does the same from a shell (with no name, it asks).
The name stays with the thread when it is lost and resumed.

### Closing a thread

`x` on the board, or `wvr close`, closes a thread's agent, its review and its
window, after asking (and says so when nvim is open in the review, whose
unsaved edits would go). Panes you added to the window stay.

If weaver created the thread's worktree, and nothing in it would be lost (no
uncommitted changes, and its commits merged into the default branch or
pushed), weaver then offers to remove it, with `wt remove` or `git worktree
remove`. The branch always stays.

## Pull requests and issues

Beside home are two views: **GITHUB**, your pull requests and issues, and
**LINEAR**, your Linear issues (every open one that involves you: assigned
to you, to do or for later, made by you, or followed). `tab` and `shift+tab` step between home and
the views like tabs, both in their windows and in `alt+y`'s popover over a
thread ([The board](#the-board)). On home, `alt+y` steps on through the
views, then back to the thread you came from.

A view that is not set up yet says in its window what it needs, such as gh installed and signed in, or a Linear
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
| `m` | More of a section that shows only some (its heading says `20 of 57`, or `30+` for Linear), kept as it refreshes (up to 100 a section on GitHub, 250 on Linear); `enter` on its last line, `⏎ more`, does too |
| `f` | A filter: a search of your own, shown first as SEARCH; for GitHub, assigned to you, a review asked of you or your teams, opened by you, mentioning you, or what is open in a repository or with a label weaver has seen; for Linear, assigned to you, created by you, subscribed to, or a team's. `f` again offers to clear it |
| `/` | Search what the view holds (its number, whole title, author, repository, branch and labels; for Linear, its team and people), best match first; a number or a reference (`45`, `#45`, `o/r#45`, `ENG-42`) finds that item first, exactly; a whole one the view doesn't hold (`o/r#45`, `#45`, `ENG-42`) is asked for, as `ctrl+s` would, once you stop typing |
| `ctrl+s` | Searching: ask GitHub or Linear for what you typed, shown as SEARCH (`enter` too, when nothing loaded matches; and words typed in `f`'s picker). GitHub's search syntax works as typed (`is:open label:bug`); an item named (`o/r#45`), or a number (`#45`) in the repositories weaver knows, is looked up as it is; bare words are searched in the repositories of the owners weaver knows |
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
pull request's branch whose review shows it against the branch it merges
into, and whose agent reviews without changing files (Claude Code in
plan mode). On an issue, it starts an
investigation that ends in a plan for you to approve. The form's `alt+m`
switches between work and read-only.

From a shell: `wvr new --pr 45` (or `owner/name#45`, or the pull request's
URL), `wvr new --issue 12`, or `wvr new --issue ENG-42`, each with `--review`
for the read-only version.

## The board

The board is home's main content. The [README](../README.md) shows what it looks like.

In a thread, `alt+y` opens the board in a popover over the thread, covering
most of the screen, with its cursor on the thread you are in. Its keys work
as they do on home; `tab` and `shift+tab` step to the views inside it, and
`esc`, `q` or `alt+y` close it, leaving you where you were. The board
leaves helpers out; a thread whose helper (an agent its agent started in a
window of the thread's session) waits on a permission or a question needs
you, and its row says which helper. `alt+Y` (`alt+shift+y`) opens the
thread's own board instead: its agent and its helpers, each helper by its
window's name, with the same keys; outside a thread it is `alt+y`. `alt+1`
goes to a waiting helper's window itself. Choosing
something that goes elsewhere or opens a dialog (a jump, `n`, `x`, `a`, or
`enter` on a pull request) closes the popover first, then does it. To go to
home's session itself, use `alt+j` and `alt+k`, which step through Rex's
sessions in its sidebar's order.

Agents are grouped under these headings:

- **NEEDS YOU**: waiting on you;
- **DONE**: finished, and you haven't looked yet;
- **WORKING**: running a turn;
- **IDLE**: idle or interrupted, or finished and seen;
- **REVIEWS**: read-only threads (reviews and investigations), whatever
  their state, since you watch them less closely; each row's cell still
  shows its state, and `alt+1` still goes to one that needs you;
- **LOST**: threads whose session went, and that weaver hasn't brought back
  ([Keeping threads](#keeping-threads));
- **HELD**: threads you put on hold, each with what's next
  ([Putting a thread on hold](#putting-a-thread-on-hold)).

Each row shows the thread's number and name, its state as a cell (`[!]`,
`[x]`, `[/]`, `[ ]`, `[-]`), one fact about it (what it waits on, the command
it runs, what its last turn said, or its subagents), its
repository and branch when there is room, and how long it has been in that
state. Titles are bright only where a thread needs you. `×2` after a branch
means two agents work in the same worktree.

The headings' counts are the board's border's. The cursor never rests on a
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
| `x` | Close the thread (`wvr close`); on a lost or held thread, forget it |
| `h` | Hold the thread: say what's next, and it waits under HELD ([Putting a thread on hold](#putting-a-thread-on-hold)) |
| `r` | Rename the thread ([Renaming a thread](#renaming-a-thread)) |
| `R` | Restart its agent, resuming its conversation ([Restarting an agent](#restarting-an-agent)) |
| `a` | Adopt the agent, if you started it by hand; elsewhere, pick a conversation to resume as a thread ([Adopting](#adopting-a-conversation)) |
| `v`, `e`, `ctrl+e` | Jump to the thread and open its review |
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

A thread lasts until you close it. If Rex restarts, you quit the session, or
the machine restarts while a thread's agent runs, weaver keeps the thread as
*lost* rather than forgetting it:

- `wvr` brings the `weaver` session back with its threads, each in a
  session of its own with its number, its agent resuming its conversation
  beside its review.
  With `WEAVER_RESTORE=0`, it lists them on home instead.
- Lost threads it doesn't bring back by itself (from another session, or
  with `WEAVER_RESTORE=0`) are listed last on the board, under LOST: `enter`
  resumes one in a new window, and `x` forgets it.
- `wvr resume` lists lost threads. `wvr resume NAME` (a number, a name or a
  session id) brings one back, `--all` brings back every one, and `--forget
  NAME` drops one.

An agent you ended yourself (`/exit`), or whose window you closed while the
session ran on, is not lost.

### Putting a thread on hold

A thread you are not working on but would lose something by closing (a
conversation with the context you'll want back) can wait under HELD
instead. `h` on its row asks, in the board's search line, what is next for
when you come back; `enter` holds it: its agent and review close, its
worktree and branch stay as they are, and its row moves to HELD with your
note. Nothing brings it back on its own, not even `wvr` starting the
session again: `enter` on it resumes it in a new window, on its
conversation, and `x` forgets it. A hold past a week shows its age a shade
brighter, a nudge to take it up or let it go. Past three held threads, the
board shows the newest three and one row for the rest (`… 2 more held`):
`enter` on it shows them all, and on it again folds them away. `wvr hold [PANE] [NOTE]` does
the same from a shell, and `wvr resume` lists held threads with lost ones
(`--all` brings back only the lost).

Claude Code deletes a conversation 30 days after its last use
(`cleanupPeriodDays` in its settings): a thread held longer than that comes
back without it.

### Adopting a conversation

weaver keeps only threads, so an agent you started yourself isn't kept until
you adopt it. Adopting makes a conversation a thread, kept and brought back
like any other:

- **An agent you started by hand**, in a pane weaver follows (a helper in
  a thread's session, say): `a` on its row, or `! wvr adopt` typed into the
  agent, ends it with its pane and resumes its conversation as a thread in
  a session of its own, with a review on its left. Adopt it between turns:
  while a turn is under way it is refused.
- **A conversation that isn't running**, such as one you ran outside weaver
  before a restart: `a` anywhere else on the board lists your recent Claude
  Code conversations, and `enter` resumes one as a thread, in the directory
  it started in. Type or paste a session id (or its first four or more
  characters) to find one, listed or not. From any shell, `wvr adopt SESSION-ID`
  (or its first four or more characters) does the same, into the weaver
  session; `wvr adopt` alone opens the list there.

A conversation still running somewhere else is marked `running`. `/exit` it
there before adopting it, so that two processes don't share one conversation.

### Quitting

Because threads come back, quitting everything is safe. `ctrl+q`, from any
pane, quits the session with every agent and review in it, and `wvr` brings
the threads back next time. It asks first, saying what would stop: a turn
under way (an agent working, or waiting on you) stops with it.

`wvr quit` quits after asking, and says what would stop: turns in progress,
and the reviews with Neovim open in them. From outside the session it quits
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
- **Processes.** Sockets nothing listens on are removed. What an agent
  left running in the background from its worktree (a dev server, a
  watcher) is not: end it yourself once its thread has gone.
- **Files.** Agents that are gone are forgotten. Lost threads go after a
  month.

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

An agent that starts needing you, or finishes, rings the terminal bell.
`WEAVER_BELL` changes that.

### Counts

`wvr status` prints the counts: `[!] 1  [x] 2  [/] 3` means one agent needs
you, two finished that you haven't seen, and three are working. The board's
border shows the same counts in colour, updated as they change rather than
polled.

### Turns

Each prompt of yours starts a turn, which the board and the card count.
weaver's files and their formats are in [design.md](design.md).

## Review

A thread's review is [tuicr](https://github.com/agavra/tuicr), in its
worktree, on the left of the agent. It shows what the branch has changed
since it left its base, commits and what is not committed yet together, and
follows the agent's edits as it makes them:

- **The branch at once**, and **commit by commit**: `(` and `)` step through
  its commits.
- **Neovim and back:** `e` on a diff line opens Neovim there, with your own
  config, in the same pane; quitting it brings the review back on the same
  hunk. (In the file list, `e` is tuicr's exclude filter.)
- **Notes for the agent:** `c` comments a line and `v` a range, saved as you
  write them. When you have reviewed, tell the agent: it reads them with
  `tuicr review list --repo .` and `tuicr review comments --session
  <slug>`, which give each comment's `path:line`.
- **A fresh range:** `q` (or `:q`) starts the review again, so commits made since it
  opened join its list (tuicr reads its range once, as it starts).

A branch with nothing of its own yet says `no changes on this branch yet`,
and the review starts as soon as there is an edit. A branch is compared
from where it left `$WEAVER_BASE`, else `git config
branch.<branch>.weaverbase` (a pull request's thread sets it), else the
default branch of the `upstream` or `origin` remote, else `main` or
`master`. weaver brings tuicr's Micrographics theme, and a tuicr config
that follows edits as they are made when you have none
([Setup](#what-wvr-sets-up)).

## Mouse

Every key has a place to click, as in btop or lazygit, and every click has a
key:

- **Hints are buttons.** A key shown under a list (`n NEW`, `x CLOSE`, `esc
  CANCEL`) does what it says when clicked, as do a card's choices.
- **Rows.** A click selects a row, and a second opens it, like `enter`. In the
  form and its pickers, one click is enough.
- **The wheel** scrolls what is under it: a list, a card, or a pane's
  history. A card you scroll
  (the wheel, or `shift+↑↓`) stays where you left it: the list beside it
  holds still as threads change, and catches up when you move the cursor
  or leave it.

Dragging in a pane selects and copies, as Rex does.

## Setup

### What wvr sets up

`wvr` sets itself up as it starts, when it is the `wvr` on your PATH:

- **Hooks.** Claude Code's hooks (`~/.claude/settings.json`) run `wvr`. It
  backs the file up before it first changes it, replaces any older weaver
  hooks, and leaves everyone else's alone. The hooks run the binary
  directly, without a shell; those nobody needs to wait for run in the
  background.
- **tuicr's settings and theme.** For its review panel it writes tuicr's
  Micrographics theme (`micrographics.toml` and the `Micrographics.tmTheme`
  beside it) into `~/.config/tuicr/themes/`, and, when you have no
  `~/.config/tuicr/config.toml`, one with `diff_watch_interval_ms = 500`, so
  the diff follows an agent's edits as it makes them (no flag sets it), and
  `q_quits = true`, so `q` starts the review again. Each says it is
  weaver's in its first lines, and weaver keeps it up to date while that
  line stays; a file of yours, or one whose line you removed, is left alone.
- **The weaver skill.** It writes the `weaver` skill where Claude Code
  reads skills (`~/.claude/skills/weaver/`): how an agent works in a thread
  (helpers as agents in windows of the thread's session, closed once their
  work is committed, small commits of only its own paths, done as `git log --oneline <base>..HEAD`, and your review read
  from tuicr). Like tuicr's files, it is rewritten while its first lines
  say it is weaver's, and left alone once you remove that line.
- **Old copies.** The first time, it removes what an older weaver kept
  for its Neovim plugin: its copy at `~/.local/share/weaver/nvim` and the
  plugins it fetched, `~/.local/share/weaver/plugins`. A Neovim config of
  yours that loaded the plugin from there no longer finds it.

It prints one line whenever it changes a file. `wvr setup --dry-run` shows
what it would change, and `WEAVER_SELF_SETUP=0` turns this off.

Claude Code reads hooks only when a session starts, so restart running
sessions after the hooks change.

### Signing in

The views sign in where you use them. A view that isn't set up offers the one key that signs it in: `gh auth
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

After an upgrade, a running weaver session's keys are rewritten the next
time `wvr` runs, and home and the views restart in the
`wvr` on your PATH at their next reload, or within a few seconds. That
includes switching builds: `./install.sh`'s `~/.local/bin/wvr` ahead of
Homebrew's, and Homebrew's again once you remove it.

### Files

wvr unpacks the files it carries under `~/.local/share/weaver/runtime`. Its state,
including agents' states and lost threads, lives under
`~/.local/state/weaver`.

## Settings

Environment variables change weaver's defaults.

| Variable | Default | Does |
| --- | --- | --- |
| `WEAVER_CLAUDE` | `claude` | The command a thread's agent runs, with its arguments (`claude --model opus`) |
| `WEAVER_TUICR` | `tuicr` | The tuicr a review runs |
| `WEAVER_SESSION` | `weaver` | The session `wvr` shows or starts |
| `WEAVER_RESTORE` | `1` | `0`: when the weaver session starts again after its server went, list its lost threads on home instead of resuming them |
| `WEAVER_WIP` | `5` | How many threads you mean to have open at once: past it, the board's count shows it (`7/5`) and the new-thread form says how to hold or close one; `0`: no limit |
| `WEAVER_AGENT_WIDTH` | `20%` | The agent's share of a thread window's width (`25%`), or columns (`60`) |
| `WEAVER_AGENT_MIN_COLS` | `44` | The least a percentage gives the agent, as long as the review keeps 60 columns |
| `WEAVER_BELL` | `1` | `needs-you`: ring only when an agent needs you; `0`: never |
| `WEAVER_FETCH_TIMEOUT` | `10` | Seconds a new thread waits for its remote's fetch before starting from the local default branch |
| `WEAVER_WORKTRUNK` | `1` | `0`: create worktrees with plain git even when Worktrunk is installed |
| `WEAVER_WT_MARKERS` | `1` | `0`: stop setting [Worktrunk](https://github.com/max-sixty/worktrunk) branch markers (the state's cell: `[/]` working, `[!]` needs you, `[x]` done) |
| `WEAVER_VIEWS` | both, except hidden ones | The views beside home: `github`, `linear`, both, or `none` |
| `WEAVER_ITEM_TASK` | `0` | `1`: a thread from a pull request or issue starts with weaver's task written into the form (`Review #45`, `Implement ENG-42`); otherwise the task is empty, and an empty task starts the agent with no prompt |
| `LINEAR_API_KEY` | unset | The Linear API key to use instead of the one `wvr linear login` keeps in the Keychain |
| `WEAVER_SELF_SETUP` | `1` | `0`: `wvr` leaves the hooks and plugins alone as it starts (`wvr setup` still sets them up) |
| `WEAVER_DEBUG` | unset | Write hook errors to `~/.local/state/weaver/hook.log`; `trace` traces every hook there |

## Development

weaver is Go (`cmd/wvr`, `internal/`). Its contracts are in
[design.md](design.md), and [decisions.md](decisions.md) says why things
are as they are.

`./install.sh` builds a checkout into `~/.local/bin/wvr`, ahead of Homebrew's
on PATH. It needs Go, is safe to rerun, and `--dry-run` shows what it would
do. Remove `~/.local/bin/wvr` to go back to Homebrew's build.

`test/run.sh` runs every suite in a few minutes, each with its own state and
nothing of yours touched. Besides weaver's own dependencies, they need jq,
python3 and Rex:

- `go vet` and `go test`, including the contract between the Go half and
  the design doc (the states);
- `test/hooks.sh`: the suites in `test/suites/`: the hooks' latency, their
  state machine and its robustness, `alt+1`, the counts, subagents,
  other agents' events, the installer and the board's
  renderings. They run on a throwaway Rex server with three fake agents;
  `test/hooks.sh SUITE` runs one;
- `test/rex.sh`: weaver on Rex end to end, on a throwaway Rex server with
  the app's environment: its session and board, a thread in its own
  session, widths, its keys (pressed as the app sends them), hooks, seen
  through alt+1, rename (weaver's, and Rex's of the thread's session), hold
  and resume, a Rex restart and quit.

`test/hooks.sh` and `test/rex.sh` need rex, and skip without it (CI has
none).
