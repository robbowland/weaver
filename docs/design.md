# weaver design: contracts

The engine (the `wvr` binary, `cmd/wvr` and `internal/`) and the
Neovim side (`nvim/`) meet at the contracts below. Change one only with both
sides agreeing, and record why in [decisions.md](decisions.md).

## Files

| Path | What |
| --- | --- |
| `$STATE_DIR` | `${XDG_STATE_HOME:-~/.local/state}/weaver` |
| `$STATE_DIR/<skey>_<pane>.json` | state of the agent in tmux pane `%<pane>` of session `<skey>` (`tmux-` and the session name with `/` as `_`); one JSON object on one line |
| `$STATE_DIR/<skey>_<pane>.nvim` | line 1: the pane id of the agent's nvim; line 2: that nvim's pid, empty while not known (the nvim writes its own as it starts); a pid of 1 or less is no process, never a live nvim |
| `$STATE_DIR/log/<session_id>.jsonl` | the agent session's activity log |
| `$STATE_DIR/<skey>.status` | the session's counts for its status line: `needs-you done-unseen working` |
| `$STATE_DIR/<skey>_<pane>.prompt` | the current turn's prompt, for its snapshots |
| `/tmp/weaver-<uid>/<skey>_<pane>.sock` | the agent's nvim `--listen` socket |
| `$STATE_DIR/<skey>_<pane>.thread` | the thread record of the agent in pane `<pane>`, when `wvr new` started it (below) |
| `$STATE_DIR/new/<skey>_<token>/` | a thread window being opened: `task` (or `resume`) and `thread.json` until the agent's pane takes them |
| `$STATE_DIR/repos` | repositories threads were started in, most recent first |
| `$STATE_DIR/fetch/<repo path, / as %>.{pid,rc,err}` | a repository's background fetch for a new branch's base: its pid while it runs, then git's status (or `none`: no remote) and its errors |
| `$STATE_DIR/close.log` | what a `wvr close` finishing in the background said |
| `$STATE_DIR/github.json`, `linear.json` | what GitHub and Linear said last for the views' sections, each section with when it was read and why asking for it last failed; changed under `.lock` beside it (see [Views](#views)) |
| `/tmp/weaver-<uid>/<skey>.fzf.<pid>.sock` | an open dashboard's fzf `--listen` socket (the hooks reload it); beside it `.keys` (the fzf actions for 1-9) and `.view` (`screen` or `help` while the preview shows them) |
| `$STATE_DIR/turns/<commit>` | `<turn> <point> <files>` of a snapshot commit, for the dashboard's file counts; pruned after 30 days |
| `$STATE_DIR/lost/<skey>_<session_id>.json` | a thread lost with its session (see [Lost threads](#lost-threads)); pruned after 30 days |
| `$STATE_DIR/rex/` | on Rex, weaver's numbers for its blocks and windows, and what Rex does not keep of them (see [Multiplexers](#multiplexers)) |

## State file

Readers must ignore fields they don't know and tolerate missing ones.

| Field | Type | Meaning |
| --- | --- | --- |
| `pane` | int | the agent's pane id (tmux's `%N` as `N`) |
| `agent` | string | `claude`, `codex`, ... |
| `state` | string | `working`, `needs-you`, `done`, `idle`, `interrupted`, `ended` |
| `since` | int | epoch s the state began; `ts` is the last event |
| `seen` | bool | you have looked since it changed; unread = `done` and not `seen` |
| `session_id`, `prompt_id`, `transcript` | string | from the agent's hook input |
| `pid`, `proc_start` | int, string | the agent process; `proc_start` is formatted like `TZ=UTC LC_ALL=C ps -o lstart=` (and `procStart` in `~/.claude/sessions/<pid>.json`) |
| `title` | string | the session title, when the agent reports one |
| `turn` | int | +1 on each prompt of yours (not on subagent reports or task notifications) |
| `prompt`, `waiting`, `last_message` | string | your last prompt; what it waits on; its last reply |
| `cwd`, `root`, `start_cwd`, `project_root` | string | `root` is the repo or worktree it is really working in; `start_cwd` the directory Claude Code snaps its cwd back to (where it started, moved by the worktree tools), the baseline a move is measured from; `project_root` its repository, `-` when it has none |
| `pending` | string | while it needs you for a tool: that call's key (name and input), so the tool's PostToolUse can be matched to it |
| `marker`, `marked_root` | string | the Worktrunk marker weaver set, and the worktree it set it on |
| `file` | string | the last file it edited |
| `files` | [string] | absolute paths edited this session, most recent last, at most 200 |
| `subagents` | [{id, type, since}] | subagents running now |
| `tasks` | [{id, subject, done}] | its task list (TaskCreated, TaskCompleted), at most 30 |
| `turn_ended` | int | the last turn that got its `end` snapshot |
| `turn_roots` | [string] | the roots the current (or last) turn touched |
| `background`, `background_tasks` | int, string | background subagents it waits on at Stop |
| `doing`, `doing_id` | string | the command it runs now, from the async PreToolUse on Bash until that tool's PostToolUse (its `tool_use_id`), the turn's end or the agent stopping work; absent when none |
| `done_ids` | string | the `tool_use_id`s of the last 8 tools that finished this turn, space-separated: an async PreToolUse landing after its PostToolUse sets nothing |
| `log` | string | absolute path of the activity log |
| `nvim_pane` | int or null | the agent's nvim pane |
| `server` | int | tmux only: the pid of the tmux server the agent runs on (from `$TMUX`, or the pane a Codex daemon's hook is placed in); a server started again is another, however soon, so its threads count as lost (absent in older states: then a session made after `ts`) |
| `tmux_session` | string | tmux only: the id of its session on that server (`$3`); a session quit and made again, even on the same server, is another (absent in older states: then a session made after `ts`). With `server`, the life of the session it runs in (`agent.Where`); a resumed state takes the life of the session it resumes in |
| `end_reason` | string | after SessionEnd, its `reason` (`prompt_input_exit`, `clear`, `logout`, `other`, ...); absent before |

## Hook to Neovim

The engine calls, off the hook's synchronous path, bounded at 2 s:

    nvim --server $SOCK --remote-expr "v:lua.WeaverEvent('<json>')"

The JSON is one line, inside a Vim single-quoted string (`'` doubled).
`WeaverEvent` returns 0 at once and schedules its work, then fires
`User WeaverEvent` with the event as data. Calls can arrive out of order:
nvim drops an `edit` or `root` older (`seq`) than the last one it applied.

Every event: `ev`, `seq` (epoch ms), `pane`, `agent`, `sid`, `turn`, `state`.

| `ev` | Extra fields | Sent when |
| --- | --- | --- |
| `edit` | `root`, `file` (absolute), `tool`, `agent_id`? | Edit, Write, MultiEdit, NotebookEdit, an MCP edit tool, or a Bash command whose changed file was identified |
| `root` | `root` | the agent moved to another repo or worktree without a file |
| `checktime` | | Bash or an MCP edit tool finished and no file was identified |
| `state` | `prompt`?, `waiting`?, `title`?, `doing`? | any change of state, turn, prompt, title or the command it runs (`doing`, `""` for none) |
| `snapshot` | `root`, `ref`, `commit`, `point`, `turn` | a snapshot commit landed |
| `focus` | `root`, `file` (may be `""`) | `wvr edit` (alt+v, alt+V, dashboard ctrl-e): where the agent is, for the review keys |

nvim debounces review refreshes itself (250 ms); there is no separate poke.
`AgentFollow(root, file)` and `AgentChecktime()` remain as aliases for one
release.

`wvr nvim <pane>` sets `WEAVER_PANE`, `WEAVER_STATE` (state file) and
`WEAVER_BIN` (the weaver binary) in nvim's environment.

## Snapshots

Per-turn snapshots use a private index, so your staging area is never touched:

1. the index is `$(git rev-parse --git-path weaver)/index-<sid>`, seeded from the real index the first time;
2. `git add --all`, `write-tree`; skip if the tree equals the tip's tree;
3. `commit-tree` with the tip as parent (the first snapshot is a root commit);
4. `update-ref` with the old value, so racing writers cannot lose a link.

| Ref (per worktree) | Chain |
| --- | --- |
| `refs/worktree/weaver/sessions/<session_id>` | the agent session's turns |
| `refs/worktree/weaver/reviewed` | what you marked reviewed, shared by every agent in the worktree |

Points: `start` at your prompt (UserPromptSubmit), `end` at Stop, StopFailure,
or an interrupt (PostToolUseFailure with `is_interrupt`). When a prompt arrives
and the previous turn never got an `end` (Esc during generation fires nothing),
the snapshot is labelled `end` of the previous turn. Snapshots are taken in the
agent's root and in every root it edited during the turn, in the background.

Author and committer `weaver <weaver@localhost>`; message:

    weaver: turn N end: <prompt, one line, at most 72 chars>

    <prompt, at most 2000 chars>

    Weaver-Session: <session_id>
    Weaver-Turn: N
    Weaver-Point: start|end|reviewed
    Weaver-Pane: <pane>
    Prompt-Id: <prompt_id>
    Transcript: <transcript_path>

So: turn N's diff is `end_N^..end_N`; the live turn is tip..worktree; since
reviewed is `reviewed..worktree`; `git blame` against the tip maps a line to
its turn and prompt.

A root first touched mid-turn (a subagent in a fresh worktree) whose chain
does not exist yet gets a `start` commit with HEAD's tree, so the turn there
has a parent to diff against. An `end` on top of an `end` of the same turn
(a turn that resumes after background work) replaces it: one `end` per turn.

`wvr snapshot --reviewed [dir]` snapshots `dir` (default: cwd) onto
`refs/worktree/weaver/reviewed` and prints the commit, or the existing tip when
the tree is unchanged; it exits non-zero with a message when `dir` is not in a
repo. `Weaver-Point: reviewed` commits appear only on the reviewed chain, so
any other point is `start` or `end`.

Untracked files larger than `WEAVER_SNAPSHOT_MAX_KB` (5 MB) are left out;
`WEAVER_SNAPSHOTS=0` turns snapshots off.

As a chain starts in a worktree (once per session there), that worktree's
session chains whose last snapshot is more than 31 days old are deleted, and
so are the private indexes no snapshot has written in that time (the
compiled half only). A worktree's refs go with it when it is removed; this
keeps a main checkout from holding every session's chain, and a copy of its
index for each, for good. The reviewed chain stays.

## Activity log

`$STATE_DIR/log/<session_id>.jsonl`, append-only, one object per line, each
under 4 KB so appends stay atomic. Every line has `t` (epoch s), `pane`,
`turn` and `ev`:

| `ev` | Fields |
| --- | --- |
| `session` | `source` (startup, resume, clear, compact) |
| `prompt` | `text`, `prompt_id` |
| `edit` | `file`, `root`, `tool`, `agent_id`? |
| `bash` | `cmd` (at most 200 chars), `file`? |
| `needs-you` | `waiting` |
| `state` | `state` |
| `interrupt` | `tool` |
| `cwd` | `cwd`, `root` |
| `subagent` | `agent_id`, `agent_type`, `phase` (start, stop), `desc`? |
| `stop` | `last` (at most 400 chars) |
| `error` | `error` |
| `snapshot` | `root`, `commit`, `point` |
| `end` | |

## Events from other agents

`wvr event [--bell]` reads one event as JSON on stdin; the pane is
`$TMUX_PANE` unless the event names one. Adapters translate an agent's own
hook input into it (`wvr adapt codex`). `--bell` rings the terminal bell on
`/dev/tty` (a Codex daemon's hook: its pane's tty) when the agent starts
needing you or finishes, for agents that cannot ring through their hook
output. Codex runs each hook in turn and waits on it, so its events are all
taken as sync ones; what would make a hook wait on git (following a shell
call's edits) goes to the background run.

Codex runs its hooks in its app server, a daemon every Codex session shares,
with the environment of whichever session started it: `$TMUX`, `$TMUX_PANE`
and the session there are that session's pane's, on a server that may have
gone since. A Codex hook run from inside the pane `$TMUX_PANE` names (that
pane's process is its ancestor, whatever it runs) is that pane's. One that
is not finds its pane by its session (`engine.codexPane`): the pane bound to
it on this server in `$STATE_DIR/codex/<session>` (`<pane> <server pid>`; a
binding of another server, or one without its server, counts for nothing),
else the one pane running `codex` whose directory is the session's `cwd` (of
two, the one no other session is bound to), bound to it from then on. Its
`pid` is that pane's process, and its session, key, `server`,
`tmux_session` and bell are that pane's, not the daemon's environment's. A
SessionEnd drops the binding, and GC old ones after a month. Neither found,
the event is dropped: never filed under `$TMUX_PANE`'s pane.

Neither a hook's input (its session, `cwd` and transcript) nor its process
(the daemon's child) says where its session runs, and the daemon's
environment is any session's, a Rex block's too: placing goes by tmux's
panes alone, whatever started the daemon, so a tmux Codex thread reports
whoever started it. A placed event acts on tmux entirely (no Rex session
id: its keys, files and multiplexer calls are the tmux pane's), and so does
its background run, which takes the session the hook acted on (`hk`'s
`Session`, `SKey`, `RexID`) rather than its own environment's. A Codex in a
Rex block is told only by its process: `codex`, its environment a Rex
block's (`$REX_BLOCK`, no `$TMUX`), its directory `$PWD` or its `--cd`.
While one runs in a directory, the directory names no tmux pane (the event
may be its session's), and an unbound tmux Codex there waits to be placed
until it has gone; a binding made before still places. A Codex weaver
cannot read the environment of counts as tmux's. Weaver does not follow
Codex on Rex, so it refuses a Codex thread there: `wvr new`, resuming,
adopting and restarting one say so.

| Field | For | Meaning |
| --- | --- | --- |
| `agent` | all, required | `codex`, `gemini`, `pi`, ... (lowercase letters, digits, `_`, `-`; anything else is ignored): the state's `agent` |
| `event` | all, required | below |
| `session` | all | the agent's session id |
| `pid` | all | the agent process (liveness); adapters find it at `session_start` |
| `pane`, `cwd`, `transcript`, `prompt_id`, `title` | all | as in the state file; `pane` digits, a string or a number (tmux's `%3` and zellij's `terminal_3` are read as 3) |
| `source` | `session_start` | `startup`, `resume`, `clear`, `fork` |
| `prompt`, `user` | `prompt` | the text; `user: false` for a machine-injected prompt (no turn) |
| `waiting` | `needs_you` | what it waits on |
| `tool_key` | `needs_you`, `tool_done` | which tool call, any text the same for a call's dialog and its end: while a dialog waits, another call ending leaves it waiting, its own moves on (the Codex adapter: the tool's name and input) |
| `tool`, `file`, `command`, `paths` | `tool_done` | with `file`, an edit; without, its changes are found as for a Bash command (in `cwd`, the root and `paths`) |
| `tool`, `error` | `tool_failed` | |
| `message` | `stop` | its last reply |
| `error` | `error` | ends the turn, needs you |
| `subagent`, `subagent_type` | `subagent_start`, `subagent_stop` | |
| `reason` | `session_end` | why it ended: the state's `end_reason` (an end on purpose is not lost) |

`event` is one of `session_start`, `prompt`, `needs_you`, `answered`,
`tool_done`, `tool_failed`, `interrupt`, `stop`, `error`, `compact`,
`session_end`, `subagent_start`, `subagent_stop`. Each maps onto the Claude
Code event the state machine already handles, so every agent gets the same
states, turns, snapshots, log and nvim events.

## Threads

`wvr new` makes the worktree (Worktrunk's `wt switch [--create] <branch>
--no-cd --format json`, whose `path` and `action` say where and whether it was
made; else `git worktree add` at `<repo>/../<repo>.<branch with / as ->`), then
one window: `new-window -n <label> -c <dir> 'weaver _agent <token> <agent>'`,
then `split-window -d -h -b -l <nvim's columns> 'weaver nvim --fresh <agent
pane>'` (argv, no shell): nvim on the left, told its agent's pane and
recorded as bound to it (`.nvim`) before `wvr new` returns, the agent on
the right at `$WEAVER_AGENT_WIDTH` (default `20%`, at least 44 columns while
nvim keeps 60) with the focus. In the weaver session the window moves to the index of its
number and is named with its label from the start.

A new branch starts from the remote's default branch
(`origin`, else the first remote; its `HEAD`, else its `main` or `master`)
after `git fetch --prune`. The form starts that fetch as it opens (and again
when the repository changes), in its own session so ssh cannot prompt on the
form's terminal, and redraws its header through fzf's `--listen` socket when
the fetch is done; enter waits for it at most `$WEAVER_FETCH_TIMEOUT` seconds
(10), and a successful fetch less than a minute old is reused (a failed one
is tried again). Offline, too slow, or
without a remote, the branch starts from the local default branch (the one
the remote's `HEAD` names, else `main` or `master`) and one dim line says so.
An explicit base (`--base`, the form's `alt-B`) is used as it is: `@` is the
HEAD of the checkout you are in (Worktrunk runs with `-C` that checkout; git
gets its commit), `^` the default branch. A branch that exists only on the
remote is checked out tracking it (`wt switch` / `git worktree add`).

The agent's pane takes the thread from `$STATE_DIR/new/<skey>_<token>/`:
it moves `thread.json` to `<skey>_<its pane>.thread`, deletes `task` (read
first, which claims it), removes the directory, then execs the agent with the
task as its prompt (`claude "<task>"`, `codex "<task>"`). A thread restored
after its session went leaves `resume` (the session id) there instead of
`task`, and its agent's pane resumes: `claude --resume <session_id>`
(the agent afresh when unknown), `codex resume <id>`. A thread closed
before its nvim is recorded as bound is closed whole all the same: `close`
finds that nvim as the pane started for its agent. Before it execs the
agent, the pane sets its own `remain-on-exit on` (and a
`remain-on-exit-format` naming the agent, its status, and R and x), so an
agent that exits, cleanly or not (a broken install, a bad flag in
`WEAVER_CLAUDE`), leaves what it said on its pane and its thread on the
board, exited: `R` (`wvr restart`) starts its agent again in that pane on
its conversation (afresh when none is known), `x` closes the thread. A
thread lasts until you close it, so its window never stays with nvim
alone in it.

### Thread record

`$STATE_DIR/<skey>_<pane>.thread`, one JSON object on one line, removed with
the agent's other files. Readers ignore fields they don't know.

| Field | Type | Meaning |
| --- | --- | --- |
| `name` | string | the tab's name as `wvr new` set it: the branch, or the task's slug for a thread in the current checkout |
| `task` | string | what you asked, in full |
| `repo` | string | the repository's main checkout; `""` when not in one |
| `branch` | string | the thread's branch (`""` on a detached HEAD) |
| `worktree` | string | the directory the thread runs in (the checkout itself when `here`) |
| `here` | bool | started in the current checkout, not a new worktree |
| `worktree_created` | bool | `wvr new` made this worktree (only then may `wvr close` offer to remove it) |
| `agent` | string | `claude` or `codex` |
| `cmd` | string | the command its agent runs (`$WEAVER_CLAUDE`, `$WEAVER_CODEX`, else the agent's name) |
| `num` | int | its number in the session (below) |
| `created` | int | epoch s |
| `base` | string | where a branch it made started (`origin/main`, `main`, `@`, …); `""` when it made none |
| `pane` | int | the agent's pane |
| `session_id` | string | its conversation: Claude Code's from the start (weaver chooses it), any agent's from its SessionStart on; absent while unknown |
| `item` | object | the work item it started from: `kind` (`pr`, `issue`, `linear`), `ref` (`owner/name#45`, `ENG-42`), `url`, `title`; absent without one |
| `mode` | string | `review`: read-only, whenever its agent starts (new, resumed, restarted): `agent.Command` takes from `cmd` what would loosen it and adds Claude Code's `--permission-mode plan` or Codex's `--sandbox read-only`; absent otherwise |
| `title` | string | its name (the form's `name` row or `wvr new --name`; for one from an item, by default the item's, `#45 Add a flag`, read-only `review :: #45 Add a flag`; later `wvr rename`, the board's `r`), which rows, cards, window labels and lost rows show in place of its agent's `title`; absent for none |

The window's current name is tmux's; in the weaver session weaver labels it
(`threadTabLabels`).

A task's slug: its first line in lowercase ASCII letters and digits, every
other run of characters one `-`, at most 48 characters, cut at a `-`.

### Adopting

`wvr adopt [PANE | SESSION-ID]` makes a conversation weaver did not start a
thread. PANE is `%N` or a number (pane numbers have no length: on Rex they
only grow); one of 4+ digits is a pane only when weaver follows an agent
there (its state, thread or nvim file). Anything else is a session id or
its first 4+ characters.
Alone, it adopts the agent in its own pane when it has one (so `! wvr
adopt` inside an agent), else opens the picker. Outside
tmux it acts on the weaver session (`$WEAVER_SESSION`, else `weaver`) on
weaver's own server, as `wvr resume`, `wvr rename` and `wvr quit` do
(`toWeaverSession`), and says so when that is not running.

- **In place** (an agent weaver follows, without a thread record): refused
  when its agent has ended, is not one weaver can resume, or has not said
  its session id. Under the numbering lock (so two adopts never both number
  it) it writes the record: `num` the lowest free, `name` the slug of its
  title (else its prompt, else its branch), `repo` and `branch` of its
  `root`, `worktree` its `root`, `here` true, `worktree_created` false (so
  close never offers to remove it), `cmd` the agent's command, `pane`,
  `session_id`. In the weaver session its window becomes the thread's: kept
  when the agent has it to itself (with its bound nvim at most) and it is
  neither home nor a view, else `break-pane` takes the agent (its process
  untouched) to a window of its own at its number (tmux's choice when that
  index is taken), named with its label; `-d` unless the agent's is the pane
  adopt runs in. An agent with no bound nvim gets one on its left
  (`splitNvim`, as a new thread's).
- **By session id:** a lost thread's is resumed (`threadRestore`); an agent
  of this session running it is adopted in place; else its transcript,
  `<claude dir>/projects/*/<id>.jsonl`, must be the only match, and no live
  Claude Code process may be in it (`~/.claude/sessions/<pid>.json`, its pid
  and `procStart` checked). Its record is made as a lost one's would be and
  opened through `threadReopen`: `start_cwd` and the thread's `worktree` its
  first `cwd` (Claude Code finds a conversation from where it started),
  `cwd` and `root` its last, `branch` its last `gitBranch`, `title` its last
  `customTitle`, else `aiTitle`, else `agentName`, else its first prompt you
  typed. The first 64 KB and last 256 KB are read, never the whole file.
- **Picker:** the newest 80 transcripts named by a session id that have a
  title and are not a thread of this session (by the records' and states'
  session ids), newest first: title, repo/branch, age, and `running` when a
  process is in it. Errors go to the status line too, since the popup
  closes.

### Renaming

`wvr rename [PANE] [NAME...]` sets the thread record's `title` (PANE: as for
adopt; else the agent this pane belongs to, its own or its nvim's), under
the numbering lock its other writers take, then relabels the windows and
reloads the boards. Without NAME it asks on the terminal (fzf, the query the
current name); an empty NAME clears it. The board's `r` on a thread's row
opens that prompt in a popup (`_popup rename PANE`, 8 lines), on an agent
that is no thread says to adopt it first; in alt+y's popover it is left for
once the popover has closed (`peekChoice`).

### Restarting

`wvr restart [--all] [--force] [PANE]` starts a thread's agent again in its
own pane, resuming its conversation, for an updated harness to take over
(PANE as for rename; `--all`, every thread record of the session; outside
tmux, the weaver session). A thread whose state is `working` or `needs-you`
is left unless `--force`, and one whose agent has not said its session id
cannot be. For each: its record (`pane` cleared), its state as a restored
thread's carries on (`writeResume`: idle, seen, without its process's
fields) and `resume` (its session id) go to `new/<skey>_<token>/`, then
`respawn-pane -k -t PANE -c DIR 'wvr _agent TOKEN KIND'` ends the agent and
runs the pane's command again, which takes them as a restored thread's pane
does and resumes (`claude --resume`, `codex resume`). DIR is where a lost
thread would resume (`Lost.Dir`). The pane id, its window, nvim and number
stay. It says what it did (restarted, left mid-turn, failed) on stdout and
on the status line, since the board's `R` and the popover run it out of
sight.

### Work items

`wvr new --pr REF` and `--issue REF` (with `--review`, read-only) start a
thread from a GitHub pull request or issue, or a Linear issue. REF is a
number (of the repository you are in), `owner/name#N` or its URL; for
`--issue` also a Linear identifier (`ENG-42`) or URL. They are read, never
written: `gh pr view` and `gh api graphql` (the unresolved review threads),
`gh issue view`, and Linear's GraphQL API (`internal/linear`: the issue and
its comments). The branch is what ties a
thread to its item:

| Item | Branch | weaver's task (`WEAVER_ITEM_TASK=1`) |
| --- | --- | --- |
| pull request | its head (`pr-N`, fetched from `pull/N/head`, for one from a fork) | `Address the review on #N`, `Review #N` |
| GitHub issue | `N-<title's slug>` | `Implement #N`, `Investigate #N` |
| Linear issue | Linear's `branchName`, else `<id>-<title's slug>` | `Implement ENG-42`, `Investigate ENG-42` |

A pull request's thread is on its head as it is now (`prHead`): each time,
`pull/N/head` (a fork's too) is fetched from the remote it is on, with what
it merges into and (in this repository) its own branch, to
`refs/weaver/pr/N`, in its own session within `WEAVER_FETCH_TIMEOUT`. Its
branch is made there (tracking the pull request's branch when it is in this
repository), or moved up to it when it is behind; one with commits the head
has not, or checked out elsewhere and behind, stays as it is. When it is not
at the head, or the head could not be fetched, a dim line says why and so
does the agent's prompt (`PullRequest.Behind`); not fetched and not here,
there is nothing to work on, and the thread is not made (never a branch of
that name from the default branch). Started in an existing checkout, it must
be on the pull request's branch.

A pull request's repository is found among the checkouts weaver knows (yours,
recent threads' and agents', `~/code/<name>` and the like, zoxide's of the
same name) by the GitHub URL of a remote as configured; none known, the form
asks (`^r`). A branch that already has a running thread in this session goes
to that thread instead. A pull request's branch gets `git config
branch.<branch>.weaverbase <remote>/<base>`, so reviews compare with what it
merges into.

The form's task starts empty (weaver's, above, with `WEAVER_ITEM_TASK=1`),
and an item's thread may start without one: its agent then runs with no
prompt. Either way the thread is named for the item (`ThreadItem.Name`:
`#45 Add a flag`, `ENG-42 Fix it`; read-only, after `review :: `, `formName`)
unless its `name` row (`--name`) says
otherwise, and keeps that name (its record's `title`) over the agent's
titles; its branch still comes from the task, else the item. With a task, the
agent is sent the task, a line on what to do in this worktree, and the
item's text (description, unresolved review comments with file:line,
comments, the latest kept within 48 KB) between two marker lines carrying a
random nonce, said to be untrusted: written by anyone who can comment.
Review comments that could not be read (gh failed, or GitHub refused the
query) are said so in that line, with how to read them, never left out in
silence (the item's `review_unread`). `--review` makes the thread's `mode`
`review`: its agent runs Claude Code in plan mode, Codex in a read-only
sandbox, each time it starts; a pull request's review starts its nvim with
`WEAVER_OPEN_REVIEW=branch`, which `weaver.lua` takes (once, then unsets) to
open `:AgentReview branch` after VimEnter.

### Commands for the dashboard

- `wvr new [--from PANE]`: with no task and a terminal, the form. Exit 0
  started (you are on the new tab unless `--no-focus`), 130 cancelled (nothing
  made), 1 error (one line on stderr). `--from` starts in that agent's
  repository.
- `wvr close [--yes] [--remove-worktree] [PANE]`: PANE is an agent or its
  nvim (default: the focused one). It says what goes and asks `close it?
  [y/N]` (`--yes` skips only that); then, for a worktree it may remove, `remove
  its worktree … too? [y/N]` (or `--remove-worktree`). Exit 0 closed, 130
  declined, 1 error. When the tab would close with the pane it runs in, the
  rest happens in a detached process (log: `close.log`).

`wvr close` removes a worktree only when the thread record says weaver made
it, it is a linked worktree (not the main checkout), not locked, `git status
--porcelain --untracked-files=all` is empty (whatever status is set to show),
HEAD is an ancestor of `origin/HEAD`, `upstream/HEAD`, `main` or `master`, or
of its upstream with nothing unpushed, and nothing else uses it
(`worktreeUsers`, which the sweep asks too: a thread of any session working
there, a lost one, a pane in it on weaver's server or yours, or on Rex the
session's; not the thread being closed, whose panes are skipped only where
they are: a pane of the same number on another server, or on tmux when the
session is Rex's, is another pane). It checks again after the agent has exited, and never forces:
Worktrunk (`wt remove --no-delete-branch --foreground`) and git refuse a dirty
worktree themselves. Its ignored files, which removing it would take (an
`.env`, local settings; not whole ignored directories a build makes again,
nor `.DS_Store`), are copied first to `$STATE_DIR/trash/<name>-<when>/`,
kept a month, and what says it was removed says where. The branch always
stays.

## Lost threads

A thread lasts until you close it. When its agent dies because its session
went (its tmux server crashed or was killed, or you quit the session), its
thread is kept as lost rather than forgotten, so it can be resumed.

- **When:** where an agent's files would be forgotten for a dead pid
  (`AgentRows`, `GC`), `retire` keeps them as lost when the agent has a
  thread record (whether or not its conversation is known), its `end_reason` is not `prompt_input_exit`,
  `logout` or `clear` (ended on purpose), and its session has gone since its
  last event: the machine has started since its last event `ts`
  (`kern.boottime`), or no tmux server (weaver's, or the caller's) has that
  life of the session any more: none of its name, or one on another server
  (the state's `server`), or one made again on the same server (another
  `tmux_session`; for older states without one, one created after `ts`;
  without `server` either, one created after `ts`). A Rex session's has gone
  when the machine has started since, or `rex.Lost` says so; while Rex does
  not answer, neither: its files stay as they are for a later look (one taken
  for lost while it runs would be resumed twice, one taken for not lost
  forgotten), and `GC` neither retires nor loses anything of a Rex session
  it cannot ask about, looking at each session's panes once a run. Anything
  else is forgotten as before; a pane closed while its session runs on is
  not lost. The hook does
  the same check before a new agent's state replaces a dead one's in a reused
  pane id (a new server numbers panes from 0 again), and only ever keeps
  there, never forgets. Files with no pid to test are an earlier server's
  when the running session of their name was created after them: `GC`
  retires a state without a pid created before its last event `ts`, and
  keeps as lost a thread record whose agent never reported (no state)
  written before the session was made, as the pane's first event does
  (`LoseStaleThread`), so a new pane of that number never takes it for its
  own, and the thread is never dropped. A thread record learns its
  conversation at its agent's SessionStart (`session_id`: Codex says its own
  then, Claude Code's changes with /clear), so it can be resumed whether or
  not its agent's state survives.
- **Record:** `$STATE_DIR/lost/<skey>_<session_id>.json` (a thread whose
  conversation was never known: `<skey>_thread-<created>-<num>.json`), one object:
  `lost` (epoch s), `skey` (the session key it was lost from, `tmux-` first
  for a tmux session; records before 2026-10-15 call it `session`), `state`
  (the state file) and `thread` (the thread record). The pane's files go.
  Readers take its directory and key through `lostDir` and its `skey`, where
  an empty field counts as none.
- **Resume:** `wvr resume [--all] [WHICH]` (WHICH: a session id or its first
  4+ characters, a thread name, or a number; alone it lists them). Each is
  claimed by moving its record into `new/<skey>_<token>/` (so two resumes never
  take one), then opened like a new thread's tab in `lost_dir`: `start_cwd`,
  else the thread's worktree, else `root`, else `cwd`. (`start_cwd` moves with
  the worktree tools, so an agent that entered a worktree resumes there.) `thread.json` (its record with
  `cmd` for now's agent command and its number when free, else the lowest
  free), `state.json` (its state as a resumed agent starts it,
  `agent.Record.Resumed`: the conversation's fields, idle and seen, in this
  session's life) and `resume` (the session id) in place of `task`. The
  agent's pane takes the record and the state for its pane id (first
  retiring files under that pane id of another life of the session; its own
  life's, as when it restarts in its pane, stay, its nvim still bound),
  then execs `claude --resume <id>` (`codex resume <id>`; no conversation
  known: the agent afresh, never `--continue` or `resume --last`, which
  would guess another's); the nvim pane binds as for a new thread. A
  conversation Claude Code runs elsewhere is refused, not resumed beside it. The state
  carries on: the first hook (SessionStart, source `resume`) keeps its turns,
  title and last reply, and its snapshot chain and activity log continue.
  A record whose directory is gone stays lost.
- **The weaver session:** as it starts, its home (`wvr home`, behind the
  board as `wvr _boot`) runs `GC`, then resumes every thread lost by a
  session of its name (and by the zellij session of that name, from before
  weaver ran on tmux only), in number order, without leaving home
  (`WEAVER_RESTORE=0`: listed instead), then sweeps worktrees (below).
- **Sweeping:** `GC` (also on every SessionStart hook) ends weaver's nvims
  whose parent has gone (`--listen` in the socket dir, parent pid 1 or a
  systemd subreaper: a server that crashed or was killed left them, with
  their language servers), ends what agents left running in panes that
  have gone (`SweepLeftovers`, below), and removes sockets in the socket
  dir nothing accepts on, a day after they were made.
- **Leftovers:** in a process tree launchd adopted (parent 1, yours), each
  process a weaver pane started (`WEAVER_TMUX_SESSION`, `TMUX` and
  `TMUX_PANE` in its own environment, read from `kern.procargs2`, or, where
  macOS hides it, its own programs' and `/bin/sh`'s, its parent's; never
  another process's in the tree), whose pane has gone (or whose server
  has: another `#{pid}`, or none), and that runs from the checkout it was
  started in (`PWD`'s top level: its program, argv[0] or the script
  argv[1], under it, as run; a virtualenv's python is a link elsewhere),
  with all under it (`condemned`). A process whose own environment says no
  pane is never one (CI's runner, whose test script runs from a checkout
  while a hook of a pane runs under it). A server put in the background goes; a daemon a tool
  started on the way (Codex's app server, gpg-agent) runs from elsewhere and
  stays, even when a gone thread's command runs under it. Each is sent TERM, then
  KILL after 2 s, and said in `$STATE_DIR/gc.log`; GC says how many on the
  status line. `wvr close` has `wvr _leftovers SHELL...` do it once the
  panes' shells have gone, in a process of its own, so closing never waits.
  `wvr gc --dry-run` lists them, with their memory as top counts it, and
  ends nothing. `$STATE_DIR/worktrees` lists the
  worktrees weaver made (`<dir> TAB <repo> TAB <unix time>`; threads' from
  before the list join it as they are seen); `SweepWorktrees` (at `_boot`,
  and `wvr gc`) removes one an hour or more old that nothing uses
  (`worktreeUsers`, as close asks), when `worktreeRemovable` holds
  (clean, its HEAD merged into a default branch or pushed), its ignored
  files kept first as close keeps them, with `wt remove
  --no-delete-branch` or `git worktree remove`, and says so on the status
  line. Close's `keep` takes a worktree off the list.
- **Dashboard:** lost threads (of every session) last, under `LOST`, glyph
  `[-]`, without a number or a 1-9 key and not counted in the title; their item
  id is `l<session id>` and field 2 the session id. Enter resumes one
  (`wvr resume`), x forgets it (`wvr resume --forget`), and the preview
  shows its card (`wvr _preview <session id>`).
- **Quit:** `ctrl+q` kills the session, and the threads come back as above.
  While an agent is mid-turn (the session option `@weaver-busy`, needs-you
  plus working, set with the counts), tmux asks first (`confirm-before`).
  `wvr quit [--yes]` kills the session you are in (outside tmux,
  `$WEAVER_SESSION`) after a card: the threads kept, the turns under way, and
  the agents' nvims' unsaved buffers (each nvim asked over its socket, as
  `wvr close` does). The footer shows `^q QUIT`.

## Dashboard

The dashboard is fzf: `wvr home` in the weaver session's home window, at
full size, and `wvr ls` (or `wvr` inside any other tmux session) in the
pane it runs in. `PushStatus`, run by every hook that changes a state,
refreshes it through its `--listen` socket (`transform(_refresh)`, the views'
`_items NAME refresh`).

A refresh reloads only rows that changed: fzf runs the preview again on any
reload, `--track` or not, from its top (`+SCROLL` or line 1; it keeps no
offset and shows none), so a card you were reading would jump back for a
change you cannot see. The list's rows command keeps what it printed beside
the socket (`.rows`, `keepRows`); a refresh prints the rows again only when
they differ (`rowsChanged`), else just the chrome, which moves nothing.
Rows that do change (an age, what an agent runs) still would, so a card you
scroll holds the list: the wheel and `shift+↑↓` over it set the input's
label to `scrolled` (no list has an input border, so it shows nowhere; a
transform reads it as `$FZF_INPUT_LABEL`), and a refresh then does nothing
while the list's pane is on a client's screen (`cardHold`). The hold ends
as the cursor moves (the focus event clears the mark and refreshes), as the
card is shown from its top again (`ctrl+r`, `s`, `?`), or at the first
refresh while the list is off screen.

fzf draws one sharp border in faint grey: `┌─ [ WEAVER :: THREADS ] 5 ──── [!] 2  [x] 1  [/] 1 ─┐`, the
title and the status bar's counts in its label (home leaves the counts to the
status line, below it). Inside, from the top: a blank
line (the search input takes its place, so nothing moves), the agents under a
heading per group, groups apart by one blank line, fzf's footer with the keys
that work now, then the card. The card goes beside the list when the pane can
be 150 columns wide (74 of them), else below it, where it gets what the list
leaves: under 6 lines it is hidden, and `s`, `tab` or `?` show it over the
list. When even the list would not fit, the gaps between groups go first, so
a heading never shows without its rows. With no agents, the board says how
to start one, with nothing to select.

| Group | Glyph | Who | Order |
| --- | --- | --- | --- |
| NEEDS YOU | `[!]` red | needs you | longest waiting first |
| DONE | `[x]` green | done, not yet seen | finished longest ago first |
| WORKING | `[/]` grey | working; a thread whose agent has not reported, for its first 2 minutes | longest working first |
| IDLE | `[x]` `[ ]` faint, `[-]` grey | done and seen, idle, interrupted, exited (its pane kept: R, x), not reporting | most recent first |
| REVIEWS | as its state | a read-only thread's agent (`mode` `review`), in any state | by state as above, then as that group orders |

The group is what the agent asks of you (`session.Need`: needs you, done and
unseen, working or starting, nothing), from its row (`AgentRows`: its
hooks' state refined by Claude Code's own session file). The status bar's
counts, ctrl+q's question (`@weaver-busy`: needs you or working), alt+1,
the card, close's card, quit's card, `wvr restart`'s mid-turn guard and
`wvr send` all take it from there, so none says otherwise. So the headings'
counts are the status bar's, and the order is the one alt+1
visits, but for read-only threads: REVIEWS holds them whatever their state
(the title's counts, by state, still include them). A row: its number (1-9), glyph, name (`Row.Called`, as everything that
names it: the name you gave the thread, else Claude Code's title, else the
thread's task or item, else the last prompt; at most 40 columns), one fact (what it
waits on; for a finished turn, the files it changed and its last reply;
the command it runs (`running npm test`), background tasks, subagents, task
progress or the file being edited; the
reason it is not reporting), repo/branch (from 96 columns; `×N` when N agents
share the worktree) and its age in that state. Columns are no wider than
their longest entry, so repo/branch and age stay close at any width.

Rows cost no git command: repo and branch come from the checkout's `.git`
file, `commondir` and `HEAD`; a finished turn's file count from the end
snapshot at the tip of `refs/worktree/weaver/sessions/<sid>` (read as a file),
asked of git once per commit (`git show --name-only` with its Weaver-Turn and
Weaver-Point trailers) and kept in `$STATE_DIR/turns/`. Only a tip that is the
`end` of the agent's current turn counts.

### Items

`wvr _rows` prints one item per line, TAB-separated: `id pane tab prev next label display`. `id` is `a<pane>` for an
agent, `l<session id>` a lost thread, `h<group>` a heading and `g<group>` the
gap above it (an empty board has no items: its message is fzf's header); fzf tracks the selection by it (`--track --id-nth 1`). A heading
or gap has the list positions of the agents before and after it in `prev` and
`next`. `label` (`name · repo/branch · turn N`) heads the card. `display` has
a TAB after the glyph; fzf shows it with `--tabstop 1` and searches only what
follows (`--nth 2`), which headings and gaps leave empty. The first nine
agents are numbered and `<sock>.keys` holds, line by line, the action that
picks each (`pos(N)+print(jump)+accept`). It also leaves next to the socket
what fzf shows around the list: `.label` (the border's label), `.title` (the
header: what to do on an empty board), `.room` and `.tight` (the card's
`--preview-window`, and whether it is hidden for want of room). On each load,
`wvr _chrome` turns them into fzf actions.

### Keys

Browsing hides fzf's input (`--no-input`), so letters are keys: `enter` and
`1`-`9` jump, `j k g G` and the arrows move, `n` new thread, `x` close, `a`
adopt (`wvr _popup adopt <pane>`: an agent row that is no thread is adopted
where it runs, said on the status line; anything else, the empty board
included, opens the picker of conversations in the form's popup), `e`
(`ctrl-e`) jump and open nvim, `s` card or screen, `tab` / `shift-tab` the
next / previous view (`wvr go next|prev`, round home and the views as a
tab bar; a view's the same), `?` keys,
`ctrl-r` reload, `q` and `esc` close. `/` shows the input and unbinds the
letters; `esc` clears the query, hides it and binds them again. The footer is
recomputed (`wvr _footer`) on every load and mode change, from the width
and the mode, `?` always last.

A heading or gap never keeps the cursor: on `focus`, a transform sends it on
to `next` (to `prev` when the move was upward, or when there is no `next`).
That covers every way of moving (keys, page, first/last, mouse, a reload
whose agent went away) in one place, for one `/bin/dash` spawn per move
(about 1 ms).

Outside home, `n` and `e` end fzf with a key (`print`), and `Ls` acts: `e`
jumps and runs `wvr edit`; `n` runs `wvr new --from <pane>` on the
pane's terminal and reopens the dashboard when it is cancelled (130) or
fails. Home keeps running: `e` and enter act in the background
(`execute-silent`), as does enter on a lost thread (`wvr resume`, its
failure said on the status line), and `n` and `x` open the dialogs
everything else opens (`wvr _popup new --from <pane>`, `_popup close
<pane>`, `_popup forget <thread>`: the form 96 x 20, a card 80 x 12, over
the board), whose status push reloads it.

### Transitions

Changing view keeps you oriented (where you are, how you got there, how
to get back) and changes one thing at a time:

- **One dialog, one place:** the form and the cards are popups over where
  you are, the same size whichever key, click or menu asks (`popup.go`);
  never the board replaced by a full-screen one, nor fzf's `execute`,
  which shows the pane's own screen between.
- **Back where you were:** going home from a thread (THREADS, a menu) puts
  the boards' cursor on its row (`_pos`, posted to each board), and alt+y's
  popover over a thread opens with its cursor there;
  closing the thread you are in lands home, not on whichever window tmux
  showed before.
- **No shifts:** search takes the header's line on the board and in the
  views alike, so the list does not move; lists reload with
  `reload-sync`, the old list showing until the new one is whole, never an
  empty one between.
- **Toggles say where they go:** `s` reads CARD while the screen shows, `?`
  CARD while the help does.
- **Room:** a dialog takes a share of the terminal's width within bounds
  and the height its content needs (`popupSize`: the form 62% wide, 96 to
  160, 22 tall; a card half as wide, 80 to 120, 14 tall), so its keys stay
  near what they act on; alt+N's goes through `wvr _popup new` too. With
  room, the form and pickers have a margin (`--padding 1,2`). Rows stay
  dense: the form's are grouped by what they are about (start; name, from
  and mode; repo, where, branch and base; agent), a blank line (`gapN` rows,
  which the cursor passes over) between groups only. The empty board
  is a welcome, centred a third of the way down, its key lines buttons
  (`click-header`, `wvr _click title`). weaver's fzf runs without
  `$FZF_DEFAULT_OPTS` (`fzfCmd`), so your settings change neither its look
  nor its keys.
- **The form holds still:** a choice leaves the cursor where it was (on the
  row acted on, or the one its key was pressed on: `.at`, then `_form land`
  on the reload's load), never moved on to start; its rows are always the
  same (where a checkout is picked, branch and base stay, saying why they
  do not apply); its header keeps its line when it says nothing, so a
  message never pushes the rows; a picker keeps the form's grid (its
  prompt ending where the labels do, its rows under the values, the same
  line between) and opens on the value chosen now.

### Mouse

Whatever shows a key is its button, and every mouse action has its key.

- **Footers:** each is a list of hints (`choice`: key, what it does),
  drawn by `hintLine`. fzf's `click-footer` runs `wvr _click board`, `_click
  form <row>`, `_click pick` or `_items NAME click`, which finds the hint
  at `$FZF_CLICK_FOOTER_COLUMN` in the same list (`hintAt`) and prints its
  action (`hintAct`): `trigger(<its fzf key>)`, `^q` as `tmux send-keys -K
  C-q` (tmux's binding, which asks mid-turn), nothing for `1-9` and `↑↓`.
- **Status line:** `MouseUp1Status` (on the release: a second click soon
  after the first is tmux's double click, never its `MouseDown`) runs `wvr
  _click status <range>`: `home` goes home (`toHome`, not alt+y's cycle),
  `next` is `wvr next`, a view's name opens it.
- **Right click:** `MouseDown3Pane`, on a program that takes the mouse,
  sets the pane option `@weaver-mouse` to `<client> <x> <y>` (the click,
  in the client's cells from 0) and forwards the click; the board's and
  the views' fzf bind `right-click` (the cursor on the clicked row first)
  to `wvr _menu board <kind>` and `wvr _items NAME menu <kind>`, which show
  that row's menu there. On a program that does not, `wvr _menu pane
  <client> <x> <y> <pane>`; on the status line, `wvr _menu status <client>
  <x>`, over it. The menus are `display-menu -MO` (they take the mouse,
  and the right click's release off them leaves them open) under the
  click, or over it with no room below. A row's items send the row's keys
  to its list (searching, only those that do not type); the others run
  weaver's commands (`nvim`, `close thread` in an 80 x 12 popup, the
  views, `next`, alt+N and ctrl+q as `send-keys -K`). tmux's own menus,
  whose Kill and Respawn would end an agent behind weaver's back, stay on
  Alt and a right click.
- **Scrolling back:** `pane-scrollbars modal` (tmux 3.6 and on): a
  scrollbar while a pane is in copy mode.

### Resume card

`wvr _preview PANE LABEL`: the label in bold, then the state and its age,
then aligned fields (asked, waits, background, said, tasks, subagents, files,
git), each wrapped under itself, at most a few lines. `git` is the worktree's
`git status` in one line: branch, ahead/behind, changed and new files. A
thread whose agent has not reported shows its record (task, worktree).
`s`/`tab` swap in the agent's screen (`tmux capture-pane`) under
`screen · name · repo/branch · turn`, as many lines as fit; `?` the keys.

## The weaver session

`wvr` outside tmux (`Start`) runs `tmux -L ${WEAVER_TMUX_SOCKET:-weaver} -f
config/tmux.conf`, without an outer tmux's or zellij's variables:
`new-session -s weaver -n home 'weaver home'`, or `source-file` and
`attach-session` when it runs. A session made that way has
`$STATE_DIR/<skey>.layout` saying `weaver`; thread labels and numbered
placement act only there. The server starts from `/` (the home window keeps
your directory, `-c`), so a worktree removed under it can never be its
directory. `$WEAVER_TMUX_SOCKET` is a name (`-L`) or a socket's path (`-S`,
which the tests use). Inside tmux, weaver talks to the server `$TMUX` names,
else weaver's.

- **Identity:** `MyPane` is the pane weaver runs in (`$TMUX_PANE` without its
  `%`; empty in a tmux popup or `run-shell`, which are not panes). `Session`
  is `$WEAVER_TMUX_SESSION`, which weaver's sessions set for their panes
  (`new-session -e`, `set-environment`, and `-e` on every window and split it
  makes), else `tmux display-message -p '#S'`; the session's key (`SKey`) is
  `tmux-<session>`.
- **Panes:** `Panes` is one `list-panes -s` with its fields split on US: a
  window `@N` as `N` (its index too), a pane `%N` as `N`.
- **Numbers:** a thread's number is its place: `num` in its thread record,
  the lowest free among the session's thread records (those being opened
  included, for at most 30 s), under `$STATE_DIR/<skey>.num.lock`. Its window
  moves to the index of its number (home is 0), when that is free. Wherever a
  row is numbered (the home board, a dashboard) the number is the thread's
  `num` when any agent has one, else the row's place, and the digits go by it.
- **Home:** `wvr home`, in window 0, is the dashboard at full size that
  never ends: enter, digits and `e` go to a thread and leave it running, `n`
  runs `wvr new` inside it (also on an empty board), `q` and `esc` go back
  to the window you came from, as alt+y does. It outlives installs of weaver
  (`installs.go`), so it writes `<skey>.fzf.<pid>.born` as it starts: its
  time is when, and it holds the real path of the binary it runs. It starts
  again in the weaver `wvr` resolves to on `$PATH` (else its own), as tmux's
  keys and the hooks run it, once that is another file than the one in
  `.born` (an upgrade; Homebrew's and `install.sh`'s `~/.local/bin/wvr`
  swapped, either way) or one changed since: `_chrome`, on the first reload
  after, aborts the board, which then execs that weaver's board (`LS_HOME=1
  wvr ls`) in the same pane and process. The board's own process asks too,
  every 5 s, and aborts its fzf through its socket: its keys cannot once the
  binary they run by has gone. Its old keys never meet rows they do not
  know. The PR and
  issue Inbox, when it comes, is a second window beside home, out of the
  thread numbering.
- **alt+y:** `wvr go home` (run in the pane's directory). From a thread, a
  popover over it (`peek`): `display-popup -B -E` at 90% of the client,
  running `wvr _peek FILE AGENT`, whose pages are the board (`ls` with its
  socket named `.fzf.p<pid>`, so hooks reload it, and its cursor on AGENT's
  row) and each view that is set up (`itemsView`), `tab` and `shift-tab`
  going round them. A popup cannot open over a popup, so a key there that
  would open a dialog or go elsewhere ends the popover with its choice
  (`peekChoice`, `peekItemChoice`) written to FILE, one wvr command a line,
  its words tab-separated; `go home`, which waits for the popover to close,
  then runs them: a jump (`_goto`), `edit`, `resume`, `_popup new|close|
  forget|adopt`, `_items NAME from REF [--review]`, or `next` (alt+1).
  `esc`, `q` and alt+y close it with nothing to run; right clicks do
  nothing in it. Without a client to show it on, alt+y goes home as from
  anywhere else. From home, on to each view in turn, and from the last back
  to the thread's window home was left from (`$STATE_DIR/<skey>.last`, or
  home when it has gone); from anywhere else home, made again if it was
  closed.
- **Views:** see [Views](#views).
- **Threads:** `new-window -n LABEL -c DIR 'weaver _agent TOK KIND'`, then
  `split-window -d -h -b -l NVIM_COLS 'weaver nvim --fresh AGENT'` (argv, no
  shell), the nvim pane titled `nvim: <dir>`, the agent active. `alt+v`'s nvim is a
  `split-window -h -b -l 80%` on the agent's left.
- **Text:** tmux reads a window's or pane's name, a start directory and a
  message as a format (`#H`, `#{...}`, `#[...]`). What weaver did not write
  itself (an item's title, an agent's, a branch, a path, an error) goes
  through `tmux.Text` (each `#` doubled) and `Env.Say`, so it shows as
  written and never draws a style or a button on the status line.
- **Focus:** `select-window` and `select-pane` work from anywhere, and the
  focused pane (in a popup or `run-shell`) is the session's active pane.
  weaver's keys pass `#{pane_id}` to `run-shell` as well.
- **Status line:** tmux's own, one line: on the left the strip (below), then
  on a thread's window, after a faint rule, the window's name, which in the
  weaver session `PushStatus` keeps as each thread's label
  (`<num> <title, else task, at most 30> · <repo>/<branch>`, grey;
  renamed only when it is not its label, since window ids start again with
  each server); on the right the session's `@weaver-counts` (`CountsFmt`),
  set when the counts change or as the session starts, so nothing polls
  (`status-interval` 0), then `[CPY]` in copy mode, `[PFX]` while the prefix
  is down, else `[NRM]`. `wvr status --tmux SESSION` prints the counts.
- **Seen as you look:** when an agent finishes, the hook's background run
  flags its window (`@weaver-unseen`). tmux's `pane-focus-in` hook, on a
  flagged window, runs `wvr _seen <window>` 1.5 s later, which marks its
  done agents seen only if the window is still the session's current one:
  passing through with alt+j/k leaves them new. (A thread that finishes while
  you are on it, or on its nvim, is seen at once: the counts see the clients' panes.)
- **alt+1** says where it landed in tmux's message line: `[2/3] 02 name ·
  needs you · what it waits on`.
- **Notes to the agent:** `weaver.notes` keeps your notes (file relative to
  its repository, lines, the version a review shows them in, the lines' text,
  your note) and `:AgentSend` pipes them, as one message, to `$WEAVER_BIN send
  $WEAVER_PANE`. `wvr send` checks the agent's state as the board has it:
  only idle, done or interrupted take it (a dialog could take pasted text as
  an answer), unless `--force`. Then `load-buffer`, `paste-buffer -p -d`
  (bracketed, so its newlines do not submit) and `Enter`.
- **Resume:** a lost thread whose directory has gone resumes in its
  repository's main checkout (`.thread.repo`), from which Claude Code finds
  the session in any of its worktrees (and since 2.1.223, in any project).
- **Resizing:** tmux gives a narrower window's columns to the rightmost pane,
  the agent, so a `window-resized` hook runs `wvr _fit <window>`, which
  sets each thread agent there back to its width (one `resize-pane -x`), only
  while nvim and the agent are the window's only panes. Below 104 columns
  nvim keeps 60 and the agent gets the rest.
- **Look:** popups, menus and copy mode in ink and greys, and one faint
  divider between panes (`pane-border-indicators off`); tmux 3.8's theme
  colours (`dark-theme-*`, server options) too, set inside `%if
  "#{dark-theme-black}"`, which an older tmux, without them, reads as false.
  `extended-keys-format csi-u` (3.5+, guarded the same way) gives apps keys
  like Shift+Enter in the form they read.
- **Keys:** `config/tmux.conf` binds them without a prefix (`bind -n`). Those
  that run weaver use `run-shell -b` with their output discarded (tmux shows a
  run-shell's output over the pane), passing `#{pane_id}`; alt+N is a
  `display-popup -B -E` 96 x 18, tall enough for the form's pickers, since a
  popup cannot be resized. Without tmux's border there, the form draws its
  own (fzf's), and what follows it in the popup, the thread being made, is
  drawn in a frame like it (`framed`): stderr goes through a pipe for the
  while, so Worktrunk's and git's lines land inside too, cut to its width,
  and a failure's buttons sit on a row of it (`frame.choose`). Pane splits arrive as `ESC [ 100 ; 9 ~` and
  `ESC [ 100 ; 10 ~`, sent by whatever keys the terminal binds to them
  (Ghostty's `csi:100;9~`), caught as `user-keys`.
- **Config:** `config/tmux.conf`, then `~/.config/weaver/tmux.conf`; read with
  `-f` as the server starts and again (`source-file`) each time `wvr`
  starts or attaches a session, so an install reaches a running server. Every
  setting may be read twice: array options take explicit indices
  (`terminal-features[90]`).

## Multiplexers

Weaver acts on its session through `mux.Mux` (`internal/mux`), which
`Env.Mux` returns: `tmux.Session` (`internal/tmux`), or with
`$WEAVER_MUX=rex`, or in a Rex session weaver made, `rex.Session`
(`internal/rex`). Inside a tmux pane (`$TMUX`), tmux always. A method that
steers what you see (focus, names, widths, marks) is best-effort and says
nothing of failing. What only tmux runs (its menus, its hooks:
`_seen`, `_fit`, `_menu`, `_click`) and the sweeps of tmux processes
(`gc`, leftovers) use tmux directly (`Env.Tmux`, `Env.TmuxSession`).

- **Ids:** `mux.PaneID` and `mux.WindowID` are what weaver's files keep,
  numbers: tmux's `%3` and `@12` are `3` and `12` (`tmux.Target`,
  `tmux.WindowTarget` give them back). Rex's blocks and windows are UUIDs,
  so `rex` numbers them: `$STATE_DIR/rex/n/<num>` is the block or window a
  number names, `u/<id>` (`:` as `_`) the number of an id, given under
  `rex/lock`, never twice; `Respawn` points the pane's number at the block
  that takes its place, holding `rex/lock` from before that block is made
  until its number names it, so the program it starts, asking its number
  however soon, gets the pane's. Beside them: `cmd/<num>` the argv weaver started a
  block with (Rex does not say; `Pane.Cmd`), `want/<num>` the place a
  window was given (home 0, a thread its number, a view 900 and on: Rex
  orders windows, it does not index them, so `Place` moves a window before
  the first placed further on), `view/<num>` its view, and
  `sessions/<id>` the name weaver gave a session it made, and when.
- **Keys:** files are keyed `tmux-<name>` or `rex-<name>`; a Rex session
  weaver did not make is named by its id.
- **Where:** on Rex, `agent.Where` is the Rex server's pid and the
  session's id. `serverPID` reads the pid from Rex's runtime file for
  `$REX_SERVER`, `servers/<SHA-256 of the endpoint, hex>/runtime.json` in
  `$REX_RUNTIME_DIR`, else `~/Library/Application Support/rex` (its endpoint
  and pid checked), so hooks make no call; without one it asks the server
  (`server.status`). `rex.Lost` is a session no longer on the server
  running; while Rex does not answer, weaver takes nothing for lost (see
  [Lost threads](#lost-threads)).
- **Rex:** each look at the session is one `rex do` (its view, and each
  terminal's size, with `Panes` its directory and processes too); every
  other method one `rex api call`. A pane's environment goes through
  `env(1)`, Rex setting none: `WEAVER_MUX=rex`, and `WEAVER_WVR`, the
  weaver that started it, which home follows ahead of PATH (Rex runs a
  command after the login shell's startup). `Spawn.Keep` makes a pane stay
  when its program exits (Rex is told as it is made; tmux by
  `KeepExited`). A popup is a floating layer, its command `rex wait` on
  it, made only when an app is attached to the session: otherwise `Popup`
  says false, makes nothing, and gives a command whose `Start` fails with
  why. `BreakOut` opens a window with a stand-in, moves the pane beside it
  and closes the stand-in. `SplitLeft` gives pid 0, not known yet (Rex runs
  a command through the login shell, whose pid it would be): `wvr nvim`
  records its own pid as it starts. Rex has no message line: `Say` and
  `SayToSession` show their text in a small floating layer at the foot of
  the active window that takes no focus (`focus: false`) and closes on its
  own after its time (what the session shows is its active window, so both
  are the same). `Looking` claims no pane and says it cannot tell (nothing
  in Rex's API says which session an app shows, and the app stays attached
  to every session it has shown), so on Rex an agent is marked seen only by
  weaver's own moves to it (`_goto`, alt+1 and the like). Rex has no
  status line or window marks: `Publish`, `Strip` and `MarkUnseen` do
  nothing there.
- **Start:** `wvr` with `$WEAVER_MUX=rex` makes the Rex session (home its
  first window, `want` 0) unless one weaver made of that name runs, asks
  the app to show it (`session.select`, with Remote Control on), and
  writes `config/rex.lua`, run by its own path, to
  `~/.config/rex/weaver.lua` for init.lua to `require`.

## Setting itself up

`wvr` outside tmux (`Start`), when `$WEAVER_SELF_SETUP` is not `0` and it is
the `wvr` on PATH (`os.SameFile` with `exec.LookPath`; a build run from a
checkout never takes the hooks), runs `SelfSetup` before tmux: for Claude
Code (`$CLAUDE_CONFIG_DIR`, else `~/.claude`) and Codex (`$CODEX_HOME`, else
`~/.codex`), when that directory exists, the hooks are merged as `wvr setup`
merges them (`mergeHooks`, a backup first when the file was there), and one
line on stderr says when a file changed. When
`$XDG_DATA_HOME/weaver/plugins/diffview-plus.nvim/.weaver-rev` is not
`diffviewRev`, a detached `wvr _plugins` fetches that commit (`git fetch
--depth 1`) beside it and swaps it in. `wvr nvim` passes
`WEAVER_PLUGINS=$XDG_DATA_HOME/weaver/plugins`; `weaver.lua` appends
diffview-plus there to the runtimepath (and sources its `plugin/`) when no
`lua/diffview/init.lua` is on it and lazy.nvim knows no `diffview*` plugin.
diffview-plus is GPL-3.0, so weaver fetches it rather than carry it.

## Views

Windows of the weaver session beside home, out of the thread numbering
(GITHUB at index 900, LINEAR at 901), each weaver's own list of a service's
work items (`itemsview.go`, one view over a source per service). Every view is in the session but those hidden from their setup
screen (`$STATE_DIR/hidden-views`; `wvr view NAME` shows one again);
`$WEAVER_VIEWS` names them instead (`none`, or `github linear`).

A view is ready when it is signed in: GITHUB when gh is installed and has a
token (`gh auth token`, which has no network round trip), LINEAR when weaver
has a Linear API key (`$LINEAR_API_KEY`, else the Keychain item
`weaver.linear` that `wvr linear login` keeps, after Linear has taken it).
Each is asked at most once a minute, the answer in `$STATE_DIR/github-auth`
and `$STATE_DIR/linear-auth`. One not ready shows in the strip with a faint
dot, and its window runs `wvr _viewsetup NAME`: what it needs, and a key
that runs the fix (`brew install gh`, `gh auth login`, `wvr linear login`);
once ready, the window becomes the view. The home board's start-up
(`_boot`) starts the ready ones detached, so the first alt+y to one finds it
ready; one that quits is started again on the next. Each window has the
window option `@weaver-view` (its name).

| View | Window | Runs | Where |
| --- | --- | --- | --- |
| `github` | `GITHUB` | `wvr _items github`: what `internal/github` finds | `$HOME` |
| `linear` | `LINEAR` | `wvr _items linear`: what `internal/linear` finds | `$HOME` |

Each source has three sections: Queue (yours to do), Mine (yours, with
someone else) and Watch (what you follow).

- GITHUB: pull requests and issues, each section a search through `gh api
  graphql` (`internal/github`; the searches and the fields they read are
  gh-dash's, MIT, credited there), kept in `$STATE_DIR/github.json`. Which
  kind it shows (`s`) is kept in `$STATE_DIR/<skey>.github-kind`. Pull
  requests have a fourth section, Team: Queue is reviews asked of you
  (`user-review-requested:@me`, drafts too) and, as a second search in the
  same request merged into it (GitHub's search has no OR), pull requests
  assigned to you, at most 100, most recently updated first; Team is
  reviews asked only of your teams (`review-requested:@me
  -user-review-requested:@me draft:false`), at most 50. Every other section
  shows at most 20.
- LINEAR: issues, each section an `issues(filter:)` query of Linear's
  GraphQL API, filtered on `isMe` so no request for the viewer comes first
  (Queue: assigned, unstarted or started; Backlog: assigned, triage or
  backlog; Mine: created, assigned to someone else or no one, open; Watch:
  subscribed, neither, open: a partition, every open issue that involves
  you in one, `lists` by role and state, which a test checks), with linear-tui's
  issue fields (MIT, credited there), kept in `$STATE_DIR/linear.json`, at
  most 30, and whether there are more (its `pageInfo`): Linear counts no
  list, so a heading says `QUEUE · 30+`, where GitHub's says `20 of 57`.

A view (fzf on `$SOCK_DIR/<skey>.view.<name>.<pid>.sock`) shows what its
source said last at once, asks again behind it when that is over 2 minutes
old (once its fzf listens: `start` runs `_items NAME fetch-bg`, so an
answer has a list to reload) and every 5 minutes after (`^r` now), and
refreshes when a thread changes
(the status push, and the 5-minute ask: [Dashboard](#dashboard)) or is made. A row is `id ref prev next display search`:
id its kind and its section's key (`h` a heading, `g` the gap before one,
`i` an item, `m` the line after a section's items when it has more); ref
the item's, else `-` and the row's id. id and ref are what fzf keeps the
cursor on across a reload (`--track --id-nth 1,2`), so they are unique: an
item in two sections (SEARCH and QUEUE) is two rows, and fzf goes to the
first row with the key it had, which moved the cursor up to the other.
None is empty (a row of none left the view empty and deaf to its keys).
prev and next are the rows around a heading or gap the cursor steps to. search is a second line for search, what an
item is searched by: its number (`#45`, `ENG-42`), its whole title and its
terms (author, repository, head branch, labels; Linear's team, people and
branch), and not its thread's number or cell, whose digits and brackets
matched anything, on the item's first row only, so a search lists it
once; the other rows' is empty. A query switches fzf to it
(`change-with-nth(6)`, back to 5 when empty) and ranks the matches
(`--tiebreak index`: ties in the rows' order), so the headings go and
nothing cut to fit is missed. A query that reads as a key (`45`, `#45`,
`o/r#45`, `ENG-42`; `_items NAME query`, on each change) is searched for
exactly instead, through fzf's `search` action, the query shown as typed:
the keys of the loaded items it names at the start of a search line
(`^#45\ `, a longer match than any other, so first), or it anywhere
(`'45`). The list stays, so typing is never held up by a reload.

A section with more than it shows (GitHub: its search's `pageInfo`;
Linear: its list's) keeps where its next page starts, and ends in its `m`
row (`⏎ more · 37 left`). `m` on any of its rows, or enter on that one,
runs `_items NAME more ID [N]`, which, when the section has more,
starts `_items NAME page ID [N]` detached: that says `asking…` in
the asking view's header, reads the page after (`github.Ask`,
`linear.Ask`), puts its items the section has not got after its own under
the cache's lock (`page.List.More`: only if the section still ends at the
cursor it read from, so a refresh between wins), and reloads the views;
the asking view puts its cursor at N (where the `m` row was: the first
item read) once its list has loaded (`<sock>.pos`, which its `load`
event, `_items NAME loaded`, takes: till then fzf keeps the cursor on the
row it was on). A section's total and age stay its first page's. A
refresh keeps what was read: it asks each section's first page (the
search's too) for as many items as the section holds, at least its own
page, at most one request's (GitHub's 100, Linear's 250: `again`, the
view's `held`), so a page read stays unless the section held more than
that.

`f` opens a picker (`_items NAME filter`, run by fzf's `execute` in the
view's pane) of searches of your own, each its source's (`viewFilter`):
GitHub's assigned to me, review asked of me, review asked of my teams,
opened by me and mentions me (`github.Filters`), then what is open in
each repository weaver knows (its checkouts' in `$STATE_DIR/repos`, then
the cached items'), then with each label it has seen, within the
repositories it saw it in (a label alone would search all of GitHub);
Linear's assigned to me, created by me and subscribed to
(`linear.Filters`), then each team it has seen. The one picked is the
cache's `search` (GitHub's, of both kinds; a review asked finds no
issues), not read yet: the view's first section, `SEARCH · mentions me ·
asking…`. The picker leaves `<sock>.find`, and once the view has loaded
that, its load event (`_items NAME loaded`) starts `_items NAME find`,
which reads the search's first page, keeps it if it is still the search,
and reloads the views, the asking one's cursor on the first found (a
reload of the answer before the view's own would be lost under it). A
refresh asks the search again with the sections, and it pages as they do
(its key `s`); its heading says what it found of the other kind (`issues:
1 (s)`). Picked again, or `clear` at the picker's top, or `esc` in the view
while it has one (`_items NAME esc`, which ends it before going back), ends
it.

`ctrl+s`, searching, asks the service for the query (`_items NAME ask
{q}`), as the view's search, as a filter's: an item named (`o/r#45`) read
as it is, and a number alone (`#45`) that number in each of the first 10
repositories weaver knows, one request for them all
(`repository.issueOrPullRequest`; GitHub's search does not find an item by
its number; one GitHub does not have is none, not a failure, though gh
fails for it: `Section.Refs`); GitHub's search syntax as typed when it has
a qualifier; else the words within the owners of the repositories weaver
knows (`user:o`; none known, what involves you), since GitHub's search is
of all of GitHub (`github.Words`); Linear's search, `searchIssues`, which
counts what it finds (`linear.Words`). enter does it when nothing loaded
matches (an action of its own: fzf skips one that names a field when
nothing matches), and `f` for words no filter matches. `ask` sends the
view back to its rows through its socket, as a key would: actions a
transform prints leave the query once the input hides.

A whole id typed in `/` that no item loaded has (`itemSource.isID`:
`o/r#45` or `#45`, `ENG-42`) is asked for without a key: the query's
transform starts `_items NAME lookup {q}` behind the view, which waits
600 ms (`lookupPause`) and asks only if the view's query (fzf's `GET`,
`session.FzfQuery`) is still that id, so typing `ENG-42` asks once, for the
one it ends on. It then does what `ask` does; the `/` filter would hide
the answer otherwise.

Like home, it outlives installs of weaver:
it writes `<sock>.born` as it starts, and once `wvr` resolves to a newer
weaver (as home's), the first reload after (`_items NAME chrome`) or the
view's own check aborts its fzf, and the view execs that weaver's `_items
NAME` in the same pane and process, which asks as that weaver asks.

A section's requests go at once, and each section comes back with how
asking for it went (`internal/page`'s `List`: its items, when they were
read, why asking failed). The cache takes a refresh under its lock, as it
is then: a section that failed keeps what it had, found by its search
(Linear: its filter) wherever it moved, as of when that was read, with the
error. The header's age is the oldest section's, and it names the sections
that failed and why (every one: just why); a failed section's heading says
`failed, as of 1h ago`, and one with nothing from before shows too. A cache
an older weaver wrote (one time, one error for all) reads as of that time.

A row shows the thread the item has: its item's
ref, else its branch, as the item's thread would name it
(`items.Item.BranchName`: a pull request's head, `pr-N` for one from a
fork; a GitHub issue's `N-…`; Linear's branch name). The card is the
thread first (the one it has, or
the branch enter starts one on), the item's fields, and its description,
rendered from markdown by `internal/markdown` (goldmark's parser, with
GitHub's tables, strikethrough, task lists and bare links; no HTML or
link targets, and none of the text's own control sequences, escape
sequences whole, since anyone wrote it), set as the vault's
Micrographics theme sets a note: a
section (h2) under a faint rule with its index (`01  SUMMARY`), h3 bold,
h4 to h6 grey capitals; prose in ink (the theme's reading text, the card's
point), strong in bold, code in ink between faint backticks (in a
terminal all is monospace, so the token marks it, as the theme shows a
link's); links `[ SRC: text ]` (the token marks them; no underline) and
hyperlinks (OSC 8, which
fzf's preview and tmux pass on: Cmd and a click opens one in Ghostty), as
is the card's title, to the item; images `[ IMG: alt ]`;
code in crop corners with its language at the top right; a GitHub alert
(`> [!WARNING]`) under an ink stripe, its kind a `[ LABEL ]` (red for one
to heed); task boxes as cells (`[x]`, `[ ]`); a quote on a rail in grey
italics; tables with a faint capitals header over a rule, columns apart
by space; rules the card's width. Not glamour, whose highlighter
registers every lexer as it loads, which costs every hook 6 ms.

- **alt+N / alt+m** in a view: a thread from the item under the cursor, or a
  read-only review of it. tmux passes the key to the view, whose fzf (also
  enter) runs `wvr _items NAME from REF [--review]`, which runs `wvr _from
  github pr|issue <owner/name> <number>` or `wvr _from linear <ID>`. `_from`
  opens `wvr new --pr/--issue … [--review]` in a popup (96 x 20) from a
  detached process, so the view goes on; a Linear issue's thread starts in
  the repository used last (`$STATE_DIR/repos`).
- **Strip:** the session option `@weaver-strip`, set as the views start, is
  `THREADS` and each view's label, the current one in ink; the status line
  shows it on every window. Each is a tmux user range
  (`#[range=user|home]`, `|github`, `|linear`), as `@weaver-counts`' counts
  are (`|next` for needs you and done, `|home` for working).
- **alt+j / alt+k** step past the views (`next-window` again while
  `@weaver-view` is set), so they cycle threads and home only.

