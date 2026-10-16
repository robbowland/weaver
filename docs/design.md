# weaver design: contracts

The engine (the `wvr` binary, `cmd/wvr` and `internal/wv`) and the
Neovim side (`nvim/`) meet at the contracts below. Change one only with both
sides agreeing, and record why in [decisions.md](decisions.md).

## Files

| Path | What |
| --- | --- |
| `$STATE_DIR` | `${XDG_STATE_HOME:-~/.local/state}/weaver` |
| `$STATE_DIR/<skey>_<pane>.json` | state of the agent in tmux pane `%<pane>` of session `<skey>` (`tmux-` and the session name with `/` as `_`); one JSON object on one line |
| `$STATE_DIR/<skey>_<pane>.nvim` | line 1: the pane id of the agent's nvim; line 2: that nvim's pid |
| `$STATE_DIR/log/<session_id>.jsonl` | the agent session's activity log |
| `$STATE_DIR/<skey>.status` | the session's counts for its status line: `needs-you done-unseen working` |
| `$STATE_DIR/<skey>_<pane>.prompt` | the current turn's prompt, for its snapshots |
| `/tmp/weaver-<uid>/<skey>_<pane>.sock` | the agent's nvim `--listen` socket |
| `$STATE_DIR/<skey>_<pane>.thread` | the thread record of the agent in pane `<pane>`, when `wvr new` started it (below) |
| `$STATE_DIR/new/<skey>_<token>/` | a thread window being opened: `task` (or `resume`) and `thread.json` until the agent's pane takes them |
| `$STATE_DIR/repos` | repositories threads were started in, most recent first |
| `$STATE_DIR/fetch/<repo path, / as %>.{pid,rc,err}` | a repository's background fetch for a new branch's base: its pid while it runs, then git's status (or `none`: no remote) and its errors |
| `$STATE_DIR/close.log` | what a `wvr close` finishing in the background said |
| `$STATE_DIR/gh-dash/gh-dash/config.yml` | gh-dash's config for the GITHUB view, weaver's whole (see [Views](#views)), written as the view starts |
| `/tmp/weaver-<uid>/<skey>.fzf.<pid>.sock` | an open dashboard's fzf `--listen` socket (the hooks reload it); beside it `.keys` (the fzf actions for 1-9) and `.view` (`screen` or `help` while the preview shows them) |
| `$STATE_DIR/turns/<commit>` | `<turn> <point> <files>` of a snapshot commit, for the dashboard's file counts; pruned after 30 days |
| `$STATE_DIR/lost/<skey>_<session_id>.json` | a thread lost with its session (see [Lost threads](#lost-threads)); pruned after 30 days |

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
| `server` | int | tmux only: the pid of the tmux server the agent runs on (from `$TMUX`); a server started again is another, however soon, so its threads count as lost (absent in older states: then a session made after `ts`) |
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
| `focus` | `root`, `file` (may be `""`) | `wvr edit` (Alt v, Alt V, dashboard ctrl-e): go there now unless that would take over typing, a diff review or unsaved changes |

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
`/dev/tty` when the agent starts needing you or finishes, for agents that
cannot ring through their hook output.

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
| `tool`, `file`, `command`, `paths` | `tool_done` | with `file`, an edit; without, its changes are found as for a Bash command (in `cwd`, the root and `paths`) |
| `tool`, `error` | `tool_failed` | |
| `message` | `stop` | its last reply |
| `error` | `error` | ends the turn, needs you |
| `subagent`, `subagent_type` | `subagent_start`, `subagent_stop` | |

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
(`--continue` when unknown), `codex resume <id>` (`--last`). A thread closed
before its nvim is recorded as bound is closed whole all the same: `close`
finds that nvim as the pane started for its agent.

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
| `item` | object | the work item it started from: `kind` (`pr`, `issue`, `linear`), `ref` (`owner/name#45`, `ENG-42`), `url`, `title`; absent without one |
| `mode` | string | `review`: read-only (its `cmd` carries the flags); absent otherwise |

The window's current name is tmux's; in the weaver session weaver labels it
(`threadTabLabels`).

A task's slug: its first line in lowercase ASCII letters and digits, every
other run of characters one `-`, at most 48 characters, cut at a `-`.

### Work items

`wvr new --pr REF` and `--issue REF` (with `--review`, read-only) start a
thread from a GitHub pull request or issue, or a Linear issue. REF is a
number (of the repository you are in), `owner/name#N` or its URL; for
`--issue` also a Linear identifier (`ENG-42`) or URL. They are read, never
written: `gh pr view` and `gh api graphql` (the unresolved review threads),
`gh issue view`, `linear-tui issue show --json`. The branch is what ties a
thread to its item:

| Item | Branch | Default task |
| --- | --- | --- |
| pull request | its head (`pr-N`, fetched from `pull/N/head`, for one from a fork) | `Address the review on #N`, `Review #N` |
| GitHub issue | `N-<title's slug>` | `Implement #N`, `Investigate #N` |
| Linear issue | Linear's `branch_name`, else `<id>-<title's slug>` | `Implement ENG-42`, `Investigate ENG-42` |

A pull request's repository is found among the checkouts weaver knows (yours,
recent threads' and agents', `~/code/<name>` and the like, zoxide's of the
same name) by the GitHub URL of a remote as configured; none known, the form
asks (`^r`). A branch that already has a running thread in this session goes
to that thread instead. A pull request's branch gets `git config
branch.<branch>.weaverbase <remote>/<base>`, so reviews compare with what it
merges into.

The agent is sent the task, a line on what to do in this worktree, and the
item's text (description, unresolved review comments with file:line,
comments, the latest kept within 48 KB) between two marker lines carrying a
random nonce, said to be untrusted: written by anyone who can comment.
`--review` runs Claude Code with `--permission-mode plan`, Codex with
`--sandbox read-only`; a pull request's review starts its nvim with
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
--porcelain` is empty, HEAD is an ancestor of `origin/HEAD`, `upstream/HEAD`,
`main` or `master`, or of its upstream with nothing unpushed, and no other pane
is in it. It checks again after the agent has exited, and never forces:
Worktrunk (`wt remove --no-delete-branch --foreground`) and git refuse a dirty
worktree themselves. The branch always stays.

## Lost threads

A thread lasts until you close it. When its agent dies because its session
went (its tmux server crashed or was killed, or you quit the session), its
thread is kept as lost rather than forgotten, so it can be resumed.

- **When:** where an agent's files would be forgotten for a dead pid
  (`AgentRows`, `GC`), `retire` keeps them as lost when the agent has a
  thread record and a session id, its `end_reason` is not `prompt_input_exit`,
  `logout` or `clear` (ended on purpose), and its session has gone since its
  last event: the tmux server no longer has a session of its name, or has one
  on another server (the state's `server`, its pid) or, for older states,
  one created after the agent's last event `ts`. Anything else is forgotten as
  before; a pane closed while its session runs on is not lost. The hook does
  the same check before a new agent's state replaces a dead one's in a reused
  pane id (a new server numbers panes from 0 again), and only ever keeps
  there, never forgets.
- **Record:** `$STATE_DIR/lost/<skey>_<session_id>.json`, one object:
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
  free), `state.json` (its state without pid, as `idle` and seen) and
  `resume` (the session id) in place of `task`. The agent's pane takes the
  record and the state for its pane id, then execs `claude --resume <id>`
  (`codex resume <id>`); the nvim pane binds as for a new thread. The state
  carries on: the first hook (SessionStart, source `resume`) keeps its turns,
  title and last reply, and its snapshot chain and activity log continue.
  A record whose directory is gone stays lost.
- **The weaver session:** as it starts, its home (`wvr home`, behind the
  board as `wvr _boot`) runs `GC`, then resumes every thread lost by a
  session of its name (and by the zellij session of that name, from before
  weaver ran on tmux only), in number order, without leaving home
  (`WEAVER_RESTORE=0`: listed instead).
- **Dashboard:** lost threads (of every session) last, under `LOST`, glyph
  `[-]`, without a number or a 1-9 key and not counted in the title; their item
  id is `l<session id>` and field 2 the session id. Enter resumes one
  (`wvr resume`), x forgets it (`wvr resume --forget`), and the preview
  shows its card (`wvr _preview <session id>`).
- **Quit:** `Ctrl q` kills the session, and the threads come back as above.
  `wvr quit [--yes]` kills the session you are in (outside tmux,
  `$WEAVER_SESSION`) after a card: the threads kept, the turns under way, and
  the agents' nvims' unsaved buffers (each nvim asked over its socket, as
  `wvr close` does). The footer shows `^q QUIT`.

## Dashboard

The dashboard is fzf: `wvr home` in the weaver session's home window, at
full size, and `wvr ls` (or `wvr` inside any other tmux session) in the
pane it runs in. `PushStatus`, run by every hook that changes a state, reloads
it through its `--listen` socket.

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
| IDLE | `[x]` `[ ]` faint, `[-]` grey | done and seen, idle, interrupted | most recent first |

So the headings' counts are the status bar's, and the order is the one Alt 1
visits. A row: its number (1-9), glyph, name (Claude Code's title, else the
thread's task, else the last prompt; at most 40 columns), one fact (what it
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
`1`-`9` jump, `j k g G` and the arrows move, `n` new thread, `x` close, `e`
(`ctrl-e`) jump and open nvim, `s` (`tab`) card or screen, `?` keys,
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
fails. Home does both inside fzf and keeps running. `x` runs `wvr close
<pane>` inside fzf (`execute`), then reloads.

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
  to the window you came from, as Alt y does. It outlives installs of weaver,
  so it touches `<skey>.fzf.<pid>.born` as it starts; the first reload after a
  newer weaver is installed runs that weaver's `_rows` and `_chrome`, and
  `_chrome`, seeing the binary newer than `.born`, aborts the board, which
  then execs the new weaver's board (`LS_HOME=1 weaver ls`) in the same pane
  and process. Its old keys never meet rows they do not know. The PR and
  issue Inbox, when it comes, is a second window beside home, out of the
  thread numbering.
- **Alt y:** `wvr go home`: from a thread to home, remembering the window
  in `$STATE_DIR/<skey>.last`; from home on to each view in turn, and from the
  last back to it (or home, when it has gone); home is made again if it was
  closed.
- **Views:** see [Views](#views).
- **Threads:** `new-window -n LABEL -c DIR 'weaver _agent TOK KIND'`, then
  `split-window -d -h -b -l NVIM_COLS 'weaver nvim --fresh AGENT'` (argv, no
  shell), the nvim pane titled `nvim: <dir>`, the agent active. `Alt v`'s nvim is a
  `split-window -h -b -l 80%` on the agent's left.
- **Focus:** `select-window` and `select-pane` work from anywhere, and the
  focused pane (in a popup or `run-shell`) is the session's active pane.
  weaver's keys pass `#{pane_id}` to `run-shell` as well.
- **Status line:** tmux's own, one line: on the left the current window's
  name, which in the weaver session `PushStatus` keeps as each thread's label
  (`<num> <title, else task, at most 30> · <repo>/<branch>`, faint grey;
  renamed only when it is not its label, since window ids start again with
  each server); on the right the session's `@weaver-counts` (`CountsFmt`),
  set when the counts change or as the session starts, so nothing polls
  (`status-interval` 0), then `[CPY]` in copy mode, `[PFX]` while the prefix
  is down, else `[NRM]`. `wvr status --tmux SESSION` prints the counts.
- **Seen as you look:** when an agent finishes, the hook's background run
  flags its window (`@weaver-unseen`). tmux's `pane-focus-in` hook, on a
  flagged window, runs `wvr _seen <window>` 1.5 s later, which marks its
  done agents seen only if the window is still the session's current one:
  passing through with Alt j/k leaves them new. (A thread that finishes while
  you are on it is seen at once: the counts see the clients' panes.)
- **Alt 1** says where it landed in tmux's message line: `[2/3] 02 name ·
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
  run-shell's output over the pane), passing `#{pane_id}`; Alt N is a
  `display-popup -B -E` 96 x 18, tall enough for the form's pickers, since a
  popup cannot be resized. Cmd+D and Cmd+Shift+D arrive as `ESC [ 100 ; 9 ~`
  and `ESC [ 100 ; 10 ~` (Ghostty's `csi:100;9~`), caught as `user-keys`.
- **Config:** `config/tmux.conf`, then `~/.config/weaver/tmux.conf`; read with
  `-f` as the server starts and again (`source-file`) each time `wvr`
  starts or attaches a session, so an install reaches a running server. Every
  setting may be read twice: array options take explicit indices
  (`terminal-features[90]`).

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
(GITHUB at index 900, LINEAR at 901), each running the best tool for its work
items where it is installed and ready (linear-tui signed in: `linear-tui auth
status`, asked at most once a minute, the answer in `$STATE_DIR/linear-auth`);
`$WEAVER_VIEWS` names them instead (`none`, or `github linear`). The home board's start-up (`_boot`) starts them detached, so
the first Alt y to one finds it ready; one that quits is started again on the
next. Each window has the window option `@weaver-view` (its name).

| View | Window | Runs | Where |
| --- | --- | --- | --- |
| `github` | `GITHUB` | `gh-dash` (else `gh dash`) in the environment below | `$HOME`, so gh-dash does not narrow itself to a repository |
| `linear` | `LINEAR` | `linear-tui` | the repository used last (`$STATE_DIR/repos`): linear-tui keeps a view per repository, and threads from it start there |

gh-dash runs on weaver's config alone: `config/gh-dash.yml`, a Go template
(`.Wvr` the binary's path, `.Fork` whether `gh-dash --version` says `-rb`, the
fork with weaver's extras), written to `$STATE_DIR/gh-dash/gh-dash/config.yml`
each time the view starts, with `XDG_CONFIG_HOME=$STATE_DIR/gh-dash` (gh-dash
reads its global config there, then `GH_DASH_CONFIG` on top, which is set
empty) and `GH_CONFIG_DIR` where gh keeps its own (its sign-in). Your gh-dash
config is for gh-dash outside weaver. What weaver does itself is not offered
twice: `C` (checkout) and `d` (diff) run weaver's commands, as Alt N and Alt R
do; the fork shows only PRs and issues (`defaults.views`), not the
notifications or a repository's branches.

- **Alt N / Alt R** in a view: a thread from the item under the cursor, or a
  read-only review of it. In GITHUB tmux passes the key to gh-dash, whose
  binding (also on `C`, and `d` for a review) runs `wvr _from github pr|issue
  <RepoName> <Number> [--review]`.
  In LINEAR tmux runs `wvr _from linear [--review]`, which reads the
  issue from `linear-tui context --json --workspace <the view's directory>`:
  the snapshot's `issue` on `issue_detail`, else `rows[selected_row]` when it
  is an issue (version 1 only). `_from` opens `wvr new --pr/--issue … [--review]`
  in a popup (96 x 20) from a detached process, so the view goes on.
- **Strip:** the session option `@weaver-strip`, set as the views start, is
  `THREADS` and each view's label, the current one in ink; the status line
  shows it on home and in a view.
- **Alt j / Alt k** step past the views (`next-window` again while
  `@weaver-view` is set), so they cycle threads and home only.

