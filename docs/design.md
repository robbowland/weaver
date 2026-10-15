# weaver design: contracts and decisions

The engine (the `weaver` binary, `cmd/weaver` and `internal/wv`) and the
Neovim side (`nvim/`) meet at the contracts below. Change one only with both sides agreeing, and log the change.

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
| `$STATE_DIR/<skey>_<pane>.thread` | the thread record of the agent in pane `<pane>`, when `weaver new` started it (below) |
| `$STATE_DIR/new/<skey>_<token>/` | a thread tab being opened: `task` and `thread.json` until the agent's pane takes them, then `pane` (the agent's pane id) until the nvim beside it reads it |
| `$STATE_DIR/repos` | repositories threads were started in, most recent first |
| `$STATE_DIR/fetch/<repo path, / as %>.{pid,rc,err}` | a repository's background fetch for a new branch's base: its pid while it runs, then git's status (or `none`: no remote) and its errors |
| `$STATE_DIR/close.log` | what a `weaver close` finishing in the background said |
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
| `state` | `prompt`?, `waiting`?, `title`? | any state change |
| `snapshot` | `root`, `ref`, `commit`, `point`, `turn` | a snapshot commit landed |
| `focus` | `root`, `file` (may be `""`) | `weaver edit` (Alt v, Alt V, dashboard ctrl-e): go there now unless that would take over typing, a diff review or unsaved changes |

nvim debounces review refreshes itself (250 ms); there is no separate poke.
`AgentFollow(root, file)` and `AgentChecktime()` remain as aliases for one
release.

`weaver nvim <pane>` sets `WEAVER_PANE`, `WEAVER_STATE` (state file) and
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

`weaver snapshot --reviewed [dir]` snapshots `dir` (default: cwd) onto
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

`weaver event [--bell]` reads one event as JSON on stdin; the pane is
`$TMUX_PANE` unless the event names one. Adapters translate an agent's own
hook input into it (`weaver adapt codex`). `--bell` rings the terminal bell on
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

`weaver new` makes the worktree (Worktrunk's `wt switch [--create] <branch>
--no-cd --format json`, whose `path` and `action` say where and whether it was
made; else `git worktree add` at `<repo>/../<repo>.<branch with / as ->`), then
one window: `new-window -n <label> -c <dir> 'weaver nvim --thread <token>'`,
then `split-window -h -l <agent's columns> 'weaver _agent <token> <agent>'`
(argv, no shell): nvim on the left, the agent on the right at
`$WEAVER_AGENT_WIDTH` (default `20%`, at least 44 columns while nvim keeps 60)
with the focus. In the weaver session the window moves to the index of its
number and is named with its label from the start.

A new branch starts, as Rob's `wnew` does, from the remote's default branch
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

Neither pane knows the other's id when it starts, so they meet
in `$STATE_DIR/new/<skey>_<token>/`: the agent's pane moves `thread.json` to
`<skey>_<its pane>.thread`, deletes `task` (read first), writes `pane`, then
execs the agent with the task as its prompt (`claude "<task>"`, `codex
"<task>"`); the nvim's pane waits up to 10 s for `pane`, removes the directory
and goes on as `weaver nvim <agent pane>`. So the agent's `.nvim` file and
socket exist before its first hook. A thread restored after its session went
leaves `resume` (the session id) there instead of `task`, and its agent's pane
resumes: `claude --resume <session_id>` (`--continue` when unknown), `codex
resume <id>` (`--last`). A thread closed before its nvim has read `pane` is
closed whole all the same: `close` finds that nvim by the token both panes were
started with.

### Thread record

`$STATE_DIR/<skey>_<pane>.thread`, one JSON object on one line, removed with
the agent's other files. Readers ignore fields they don't know.

| Field | Type | Meaning |
| --- | --- | --- |
| `name` | string | the tab's name as `weaver new` set it: the branch, or the task's slug for a thread in the current checkout |
| `task` | string | what you asked, in full |
| `repo` | string | the repository's main checkout; `""` when not in one |
| `branch` | string | the thread's branch (`""` on a detached HEAD) |
| `worktree` | string | the directory the thread runs in (the checkout itself when `here`) |
| `here` | bool | started in the current checkout, not a new worktree |
| `worktree_created` | bool | `weaver new` made this worktree (only then may `weaver close` offer to remove it) |
| `agent` | string | `claude` or `codex` |
| `cmd` | string | the command its agent runs (`$WEAVER_CLAUDE`, `$WEAVER_CODEX`, else the agent's name) |
| `num` | int | its number in the session (below) |
| `created` | int | epoch s |
| `base` | string | where a branch it made started (`origin/main`, `main`, `@`, …); `""` when it made none |
| `pane` | int | the agent's pane |

The window's current name is tmux's; in the weaver session weaver labels it
(`threadTabLabels`).

A task's slug: its first line in lowercase ASCII letters and digits, every
other run of characters one `-`, at most 48 characters, cut at a `-`.

### Commands for the dashboard

- `weaver new [--from PANE]`: with no task and a terminal, the form. Exit 0
  started (you are on the new tab unless `--no-focus`), 130 cancelled (nothing
  made), 1 error (one line on stderr). `--from` starts in that agent's
  repository.
- `weaver close [--yes] [--remove-worktree] [PANE]`: PANE is an agent or its
  nvim (default: the focused one). It says what goes and asks `close it?
  [y/N]` (`--yes` skips only that); then, for a worktree it may remove, `remove
  its worktree … too? [y/N]` (or `--remove-worktree`). Exit 0 closed, 130
  declined, 1 error. When the tab would close with the pane it runs in, the
  rest happens in a detached process (log: `close.log`).

`weaver close` removes a worktree only when the thread record says weaver made
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
- **Resume:** `weaver resume [--all] [WHICH]` (WHICH: a session id or its first
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
- **The weaver session:** as it starts, its home (`weaver home`, behind the
  board as `weaver _boot`) runs `GC`, then resumes every thread lost by a
  session of its name (and by the zellij session of that name, from before
  weaver ran on tmux only), in number order, without leaving home
  (`WEAVER_RESTORE=0`: listed instead).
- **Dashboard:** lost threads (of every session) last, under `LOST`, glyph
  `[-]`, without a number or a 1-9 key and not counted in the title; their item
  id is `l<session id>` and field 2 the session id. Enter resumes one
  (`weaver resume`), x forgets it (`weaver resume --forget`), and the preview
  shows its card (`weaver _preview <session id>`).
- **Quit:** `Ctrl q` kills the session, and the threads come back as above.
  `weaver quit [--yes]` kills the session you are in (outside tmux,
  `$WEAVER_SESSION`) after a card: the threads kept, the turns under way, and
  the agents' nvims' unsaved buffers (each nvim asked over its socket, as
  `weaver close` does). The footer shows `^q QUIT`.

## Dashboard

The dashboard is fzf: `weaver home` in the weaver session's home window, at
full size, and `weaver ls` (or `weaver` inside any other tmux session) in the
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
background tasks, subagents, task progress or the file being edited; the
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

`weaver _rows` prints one item per line, TAB-separated: `id pane tab prev next label display`. `id` is `a<pane>` for an
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
`weaver _chrome` turns them into fzf actions.

### Keys

Browsing hides fzf's input (`--no-input`), so letters are keys: `enter` and
`1`-`9` jump, `j k g G` and the arrows move, `n` new thread, `x` close, `e`
(`ctrl-e`) jump and open nvim, `s` (`tab`) card or screen, `?` keys,
`ctrl-r` reload, `q` and `esc` close. `/` shows the input and unbinds the
letters; `esc` clears the query, hides it and binds them again. The footer is
recomputed (`weaver _footer`) on every load and mode change, from the width
and the mode, `?` always last.

A heading or gap never keeps the cursor: on `focus`, a transform sends it on
to `next` (to `prev` when the move was upward, or when there is no `next`).
That covers every way of moving (keys, page, first/last, mouse, a reload
whose agent went away) in one place, for one `/bin/dash` spawn per move
(about 1 ms).

Outside home, `n` and `e` end fzf with a key (`print`), and `Ls` acts: `e`
jumps and runs `weaver edit`; `n` runs `weaver new --from <pane>` on the
pane's terminal and reopens the dashboard when it is cancelled (130) or
fails. Home does both inside fzf and keeps running. `x` runs `weaver close
<pane>` inside fzf (`execute`), then reloads.

### Resume card

`weaver _preview PANE LABEL`: the label in bold, then the state and its age,
then aligned fields (asked, waits, background, said, tasks, subagents, files,
git), each wrapped under itself, at most a few lines. `git` is the worktree's
`git status` in one line: branch, ahead/behind, changed and new files. A
thread whose agent has not reported shows its record (task, worktree).
`s`/`tab` swap in the agent's screen (`tmux capture-pane`) under
`screen · name · repo/branch · turn`, as many lines as fit; `?` the keys.

## The weaver session

`weaver` outside tmux (`Start`) runs `tmux -L ${WEAVER_TMUX_SOCKET:-weaver} -f
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
- **Home:** `weaver home`, in window 0, is the dashboard at full size that
  never ends: enter, digits and `e` go to a thread and leave it running, `n`
  runs `weaver new` inside it (also on an empty board), `q` and `esc` go back
  to the window you came from, as Alt y does. It outlives installs of weaver,
  so it touches `<skey>.fzf.<pid>.born` as it starts; the first reload after a
  newer weaver is installed runs that weaver's `_rows` and `_chrome`, and
  `_chrome`, seeing the binary newer than `.born`, aborts the board, which
  then execs the new weaver's board (`LS_HOME=1 weaver ls`) in the same pane
  and process. Its old keys never meet rows they do not know. The PR and
  issue Inbox, when it comes, is a second window beside home, out of the
  thread numbering.
- **Alt y:** `weaver go home`: from a thread to home, remembering the window
  in `$STATE_DIR/<skey>.last`; from home back to it (home is made again if it
  was closed).
- **Threads:** `new-window -n LABEL -c DIR 'weaver nvim --thread TOK'`, then
  `split-window -h -l AGENT_COLS 'weaver _agent TOK KIND'` (argv, no shell),
  the nvim pane titled `nvim: <dir>`, the agent active. `Alt v`'s nvim is a
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
  is down, else `[NRM]`. `weaver status --tmux SESSION` prints the counts.
- **Seen as you look:** when an agent finishes, the hook's background run
  flags its window (`@weaver-unseen`). tmux's `pane-focus-in` hook, on a
  flagged window, runs `weaver _seen <window>` 1.5 s later, which marks its
  done agents seen only if the window is still the session's current one:
  passing through with Alt j/k leaves them new. (A thread that finishes while
  you are on it is seen at once: the counts see the clients' panes.)
- **Alt 1** says where it landed in tmux's message line: `[2/3] 02 name ·
  needs you · what it waits on`.
- **Notes to the agent:** `weaver.notes` keeps your notes (file relative to
  its repository, lines, the version a review shows them in, the lines' text,
  your note) and `:AgentSend` pipes them, as one message, to `$WEAVER_BIN send
  $WEAVER_PANE`. `weaver send` checks the agent's state as the board has it:
  only idle, done or interrupted take it (a dialog could take pasted text as
  an answer), unless `--force`. Then `load-buffer`, `paste-buffer -p -d`
  (bracketed, so its newlines do not submit) and `Enter`.
- **Resume:** a lost thread whose directory has gone resumes in its
  repository's main checkout (`.thread.repo`), from which Claude Code finds
  the session in any of its worktrees (and since 2.1.223, in any project).
- **Resizing:** tmux gives a narrower window's columns to the rightmost pane,
  the agent, so a `window-resized` hook runs `weaver _fit <window>`, which
  sets each thread agent there back to its width (one `resize-pane -x`), only
  while nvim and the agent are the window's only panes. Below 104 columns
  nvim keeps 60 and the agent gets the rest.
- **Look:** popups, menus and copy mode in Micrographics, and one faint
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
  `-f` as the server starts and again (`source-file`) each time `weaver`
  starts or attaches a session, so an install reaches a running server. Every
  setting may be read twice: array options take explicit indices
  (`terminal-features[90]`).

## Decisions

- 2026-10-13: WorktreeCreate and WorktreeRemove hooks are not registered.
  In Claude Code 2.1.296 they replace git worktree creation and removal (the
  hook must print the new path), so registering them would break `--worktree`
  and EnterWorktree. Roots come from CwdChanged, the worktree tools and
  SessionStart's cwd instead.
- 2026-10-13: The agent's pid comes from `CLAUDE_PID`, which Claude Code sets
  for hooks. A hook whose `CLAUDE_PID` differs from the pane's recorded,
  still-running agent is a nested session (`claude -p`, an SDK script) and is
  ignored.
- 2026-10-13: Snapshot refs follow REVIEW's layout: the chain under
  `sessions/<sid>` and one `reviewed` ref per worktree. The first snapshot is
  a root commit, so a chain is self-contained.
- 2026-10-13: Snapshots are forked from the synchronous hook rather than run
  from a separate async hook entry, because an async entry would race the
  synchronous one for the turn number.
- 2026-10-14: Polite following. An `edit` or `root` event is followed only
  when you have been idle for `g:weaver_follow_idle_ms` (default 3000;
  `$WEAVER_FOLLOW_IDLE_MS` in tests) in normal mode, in a normal file window,
  with nothing half typed, and no diff review in the tab; otherwise it waits
  as the pending target (winbar `→ file`) and a 250 ms poll follows it once
  those hold. Everything is planned before any tab switch; a window with
  unsaved changes, a terminal, quickfix or git UI is never replaced (a
  terminal-only tab gets a split); a tab that is not current is updated
  without switching to it. `focus` skips only the idle wait and the
  file-window rule.
- 2026-10-14: Quiet by default: routine "following"/"reloaded" messages are
  gone (they covered the winbar under noice); only warnings remain.
- 2026-10-14: One review view. `nvim/lua/weaver/diffview.lua` (no agent
  knowledge) tags the view's tab with `diffview_scope` (identity) and
  `diffview_context` (label); any tagged view is "the" view, so Rob's own
  diff keys (which call the same module) and weaver's scopes never stack.
  Comparisons are a closed variant (`index`, `worktree`, `branch`,
  `commits`); whether a view needs the private index is derived from it.
- 2026-10-14: Untracked files in reviews. diffview hides untracked files
  whenever the left side is a commit, and `git diff <commit>` calls a file
  that is in the commit but untracked DELETED. Such views run git with
  `GIT_INDEX_FILE` pointing at `$(git rev-parse --git-path weaver)/view-index`,
  a copy of the real index with untracked files intent-to-add, rebuilt before
  each listing (skipped when neither the index nor the untracked set changed).
- 2026-10-14: The agent's nvim sets 'autoread' off: with it on, nvim reloads
  an unchanged buffer without asking FileChangedShell, so the changed line
  could not be found. weaver's FileChangedShell makes the same choice.
- 2026-10-14: A review is closed from the tab you came from: closing the
  current tab fails with E445 while noice has a float open in it.
- 2026-10-14: Neovim tests sit at the public boundary: a child nvim driven
  over RPC (real typed keys and modes), events through `WeaverEvent`, and
  assertions on buffers, tabs, the winbar, messages, quickfix and diffview's
  file panel. No test reaches into module internals.
- 2026-10-14: Shapes over flags (designing-with-types audit). Hook events
  are parsed once at the boundary (`parse_event`: the contract's fields with
  the right types, seq a number); following works on Targets (`root` and/or
  `file`, never neither) and Plans (`new_tab` | `tab` with an optional show
  `in`/`beside` | `blocked`); `:AgentWhy` returns `unknown` | `unsaved` |
  `turn` | `between` | `before`, each described exhaustively; who asked is a
  literal (`agent` | `you`), not a force flag.
- 2026-10-14: The bar shows a finished turn's size and the review key
  (`2 files ␣at`), from the end snapshot (one `git diff --shortstat`), and
  task progress while the agent works (`tasks` in the state file).
- 2026-10-13: Edits and every other tool-call event go to the async hook
  (`async: true`), so following costs the agent nothing; the synchronous hook
  keeps the events whose state must be in place before the agent goes on, or
  that ring the bell. nvim, zellij, Worktrunk, git and the status counts all
  run in a background fork after the state is written.
- 2026-10-13: Hook latency, median of 15 spaced runs in a real zellij session
  (test/suites/05-latency.sh): before, 12-14 ms for an ordinary event and
  26-58 ms for an Edit that moved roots (synchronous nvim and zellij calls);
  after, 15-17 ms for an ordinary event (one jq over a richer state, one mv,
  one fork) and nothing for an Edit, whose async hook takes about 20 ms of its
  own; a tool call that can change nothing exits in 7-9 ms without jq.
  Measured savings: the hook runs in the C locale (2 ms of bash pattern
  matching), parses only the code above its dispatch (the rest is sourced in
  the background fork), and finds repository roots without git.
- 2026-10-13: Done rings the bell too (`WEAVER_BELL=needs-you` for the old
  behaviour): with Rob's alerts limited to the status bar and the tab mark,
  the tab mark is what makes a finished agent "unread", and zellij clears it
  when you look. A done agent whose pane is focused when the counts are
  computed counts as seen.
- 2026-10-14: The zjstatus counts come from zjstatus's command widget running
  `weaver status --zjstatus` every 2 s, not from `zellij pipe`. Measured with
  zellij 0.45.1 and zjstatus 0.25.0: the pipe client never returns while
  zjstatus is loaded (zjstatus never releases CLI pipes), the server logs
  "Action CliPipe did not complete within 1s timeout" for every pipe, and after
  about 100 pushes a test session's server deadlocked twice (kill-session hung,
  SIGTERM ignored; `zellij list-sessions` hung for everyone meanwhile). The
  widget's command only reads a counts file the hooks keep (no external
  command, bounded read, empty output and status 0 on any error). zjstatus
  issue #247 (a Mutex panic in command widgets under wasmi, reported on 0.22
  with zellij 0.44.1, still open, not mentioned in the 0.24 or 0.25 notes) is
  the known risk; see test/soak/zjstatus.sh. If it bites, lengthen the
  interval rather than drop the segment.
- 2026-10-14: Agents other than Claude Code report through `weaver event`
  (normalized JSON), mapped onto the Claude Code events the state machine
  already handles; an adapter per agent turns its own hook input into it.
  Codex's hooks mirror Claude Code's (without Notification, with Interrupt),
  so `weaver adapt codex` is one jq program. Codex tool names differ, so a
  tool call without a file path is treated like Bash: the files it changed are
  found with git status. install.sh adds the Codex hooks when ~/.codex exists;
  Codex runs them only after you trust them in /hooks.
- 2026-10-14: zjstatus soak (test/soak/zjstatus.sh): 10 minutes, 5 tabs, the
  widget every 2 s, 1079 hook events; zellij always answered within 3 s, no
  "ERROR IN PLUGIN", no plugin panic in the log, counts on screen at every
  check. Not covered: detaching and re-attaching a client, the trigger in
  zjstatus issue #247's report.
- 2026-10-14: Moving you to an agent. In zellij 0.45.1 `zellij action
  focus-pane-id` from the command line does not move a client between the
  panes of a tab; it leaves a phantom per-command focus that also shows up as
  a second `is_focused` pane. Going to a tab, `hide-floating-panes` and
  `focus-next-pane` do move the client. So weaver finds the pane you pressed
  a key on by hiding the floating panes (its own pane keeps running) and
  asking `list-clients` where your client landed, and moves you with
  go-to-tab plus `focus-next-pane` until `list-clients` shows the target.
  Side effect: floating panes you had open are hidden afterwards.
- 2026-10-14: Known and left: Alt v and Alt 1 leave your own floating panes
  hidden (showing them again before weaver's pane closes would put the focus
  back on a floating pane); install.sh swaps the install in with two renames,
  so a hook firing in the milliseconds between them fails (harmlessly: hooks
  never block Claude Code).
- 2026-10-14: A thread tab keeps the session's status bar. `new-tab
  --layout-string` with a bare layout drops `default_tab_template` (measured on
  zellij 0.45.1: no bar), and a layout string with its own
  `default_tab_template` but no `tab` ignores it; a `tab` block wrapped in the
  dumped `new_tab_template` keeps it, plugin configuration and all. Percentage
  sizes work only in layouts, which is why the tab is one layout rather than
  `new-pane` splits.
- 2026-10-14: The thread key is `Alt N`, beside Rob's `Alt n` (new pane). Alt
  t would have been the mnemonic, but Claude Code binds meta+t (thinking),
  meta+p (model), meta+o (fast mode) and meta+w; skhd owns Alt a/s/d/f/g/q/e/z/r.
- 2026-10-14: Closing a tab's last tiled pane closes the tab, floating panes
  included (measured), so `weaver close` asks everything first and, when it
  runs in a floating pane of the tab it closes, finishes in a detached
  process that ignores the hangup.
- 2026-10-14: `weaver close` offers to remove only worktrees `weaver new`
  made: Rob's Worktrunk notes leave agent tools' own worktrees (Claude Code's
  `--worktree`, Codex) to those tools, and hand-made ones to him. Branches are
  never deleted (Rob's `remove.delete-branch = false`), and `wt merge` is not
  used (his workflow keeps merging in git and gh-stack).
- 2026-10-14: The form is one fzf, its fields kept in a file that `weaver
  _form` updates from fzf's key bindings (`execute`, `transform-header`), so
  every field is a key away and enter starts at once; bash 3.2 has no `read
  -i` for prefilled fields. Its keys (tab, shift-tab, ctrl-r, alt-b, ctrl-e)
  avoid zellij's mode keys (Ctrl b, g, h, n, o, p, q, s, t never reach a pane)
  and fzf's line editing.
- 2026-10-14: The dashboard is grouped by state under headings (Rob's pick
  among mockups, "but more readable"). From prior art: status first and the
  last reply in the row, progressive depth in a peek
  ([Claude Code's agent view](https://claude.com/blog/agent-view-in-claude-code));
  shapes that read without colour, 1-9 to jump, `?` for help, `n` for a new
  session ([agent-deck](https://github.com/asheshgoplani/agent-deck));
  waiting before done before working, a jump closes the dashboard, the agent's
  output in a preview ([workmux](https://workmux.raine.dev/guide/dashboard/));
  sections ([gh-dash](https://github.com/dlvhdr/gh-dash)); `/` to search a
  list whose letters are keys (k9s, lazygit). And the usual TUI rules: groups
  apart by space, not lines; one border style; names left, numbers right;
  inverse only for the selection; a footer with the keys that work now.
- 2026-10-14: Seen done agents are under IDLE, not DONE, so each heading's
  count is the status bar's and DONE is what is new. Rows are in Alt 1's
  order (oldest first), not newest first as in the mockup: row 1 is where
  Alt 1 goes.
- 2026-10-14: Headings are items the cursor skips (a `focus` transform), not
  parts of multi-line items (`--read0`: the selection would invert the
  heading with its first row) nor fzf's raw mode with `down-match` (it needs
  a query that is always on, and page, first/last, mouse and reloads would
  still land on headings). Measured: one dash spawn per move, about 1 ms.
- 2026-10-14: Searching does not reload: headings and gaps have nothing in
  the searched field, so a query hides them and an empty one brings them
  back, and the selection is never lost. Two fzf 0.74.5 quirks shape `esc`:
  `clear-query` from a transform's output does nothing, and `clear-query`
  does nothing while the input is hidden; so the binding is `clear-query`
  then the transform.
- 2026-10-14: The screen is on `s` and `tab`, not `ctrl-s`: Rob's zellij
  binds Ctrl s to scroll mode outside it, so fzf never sees it.
- 2026-10-14: fzf exports `FZF_COLUMNS=0` to a `start` action, so the first
  rows are rendered before fzf starts, at the terminal's width (`stty size`)
  with the preview where fzf will put it; reloads use `FZF_COLUMNS` and
  `FZF_PREVIEW_LEFT`.
- 2026-10-14: The selection is pure invert, the row's own colours stripped
  (`fg+:#000000:strip,bg+:#ffffff`), in one `FZF_COLORS` for every fzf weaver
  opens.
- 2026-10-14: Rows run no git. Before, `weaver _rows` ran `git rev-parse` and
  `git status` per agent; measured in the test session (9 agents in three
  worktrees of a small repository, median of 11): 166 ms before, 65 ms after,
  of which `zellij action list-panes` is 30 ms. In a large repository the
  old cost grew with each worktree's `git status`; now git runs once per new
  end snapshot. The card's one `git status` runs in the preview, off the
  reload (22 ms, was 50 ms with its 30-line screen dump).
- 2026-10-14: zjstatus marks a tab whose agent rang the bell with `*`, not
  `●`, which the counts use for working.
- 2026-10-14: Rob asked for one consistent glyph family, no emoji, in the
  Micrographics design language of his Obsidian vault. In JetBrains Mono the
  old set had three sizes (`!` a thin stroke, `✓` 527 units and raised, `●`
  620), and `↵` was not in the font. A Nerd Font circle set was consistent but
  an icon legend, which the language avoids. The vault draws task states as a
  square cell with one mono mark (`x` done, `/` in progress, `-` cancelled,
  `!` in red), so weaver's states are those cells as brackets (`[!]` `[x]`
  `[/]` `[ ]` `[-]`), every one three columns, brackets faint and the mark in
  its signal colour, everywhere a state shows (board, home, counts, nvim bar,
  Worktrunk's markers, which were emoji). The board follows the vault too:
  `[ WEAVER :: THREADS ] 5` like `[ MAP :: LOCAL ] 4 LINKS`, `NEEDS YOU · 2`
  captions, two-digit numbers like its numbered sections, uppercase key
  labels (keys keep their case: `n` is not `N`), and titles in ink only where
  a thread needs you. `⏎`, which the font has, replaces `↵`.
- 2026-10-14: New branches start from the fetched remote default, as Rob's
  `wnew` does, rather than from a possibly stale local `main`. The fetch is
  started when the form opens so enter seldom waits; offline it falls back
  instead of failing.
- 2026-10-14: `Alt v` reopening a thread's nvim restores the split with
  `zellij action resize -p <agent> decrease left` steps. Measured on zellij
  0.45.1: `new-pane --direction left` puts the pane on the right anyway;
  `override-layout --layout-string … --retain-existing-terminal-panes
  --apply-only-to-active-tab` changed nothing; each resize step moves the
  border by 5% of the tab (10 of 200 columns) and costs about 12 ms. So the
  number of steps comes from the measured widths, then one more measurement
  checks it: 50% to 20% is six steps, about 100 ms on top of the ~190 ms
  reopen. Only when the agent and its nvim are the tab's only tiled panes; the
  agent is never touched otherwise. `open_left_of` now goes to the agent's
  tab first, so `weaver edit <pane>` from another tab opens nvim there.
- 2026-10-14: The dashboard fits its pane (visual QA: a fixed 90% x 85% pane
  left most of it empty). Borderless with fzf's own border, because zellij's
  frame repeated "weaver ls" and "PIN [ ]" over the dashboard's title; the
  binding opens it borderless and named, so the frame never flashes. The
  title moved to the border's label, freeing the header for a blank line
  that the search input replaces (fzf 0.74.5: `--header-first`, and
  `change-header` from a transform), so searching moves nothing. The list
  has priority over the card: at 80 x 30 the old 10-line card minimum cut
  the list mid-group.
- 2026-10-14 (visual QA): `worktree_users` interpolated `.id` inside the
  `$skip` string's own filter, so jq failed on every pane and the in-use
  guard never fired; fixed and tested, and no other filter does it. The
  close prompt is a card in the form's style; the form's keys sit in a
  fixed column at its right edge; a thread's agent gets at least
  `WEAVER_AGENT_MIN_COLS` (44) while nvim keeps 60, still as a percentage in
  the layout (a fixed-column pane keeps its columns when its neighbour
  closes). The Alt N form fits its floating pane with the shared `float_fit`
  (rows from its header, 96 columns; the pickers and `$EDITOR` get more rows
  while open) and draws its own sharp fzf border, the pane borderless.
- 2026-10-14: Rob's hybrid layout for the weaver session (sidebar and home,
  stable numbers, one status line), after workmux's sidebar
  (https://workmux.raine.dev/guide/sidebar/: a sidebar per window, j/k and
  enter in it, a status icon, name and elapsed time per tile, about 15 MB
  each). weaver's sidebars register like the dashboard, on fzf sockets the
  hooks reload; one agent_rows per change feeds all of them.
- 2026-10-14: The sidebar marks its own thread by inverting it (pure invert,
  the whole width) rather than with fzf's cursor, which an unfocused pane
  shows all the same; fzf's own line highlight is off there and the cursor is
  put back on the thread at every load.
- 2026-10-14: The status line's thread and repo/branch come from the tab name
  (zjstatus's `{tabs}`, current tab only, event-driven) rather than a command
  widget, which cannot know the current tab without asking zellij, which the
  widget never does (see the zjstatus decision above).
- 2026-10-14: Inside `--border`, fzf's FZF_COLUMNS and FZF_LINES leave out
  the border and the padding next to it (4 columns, 2 lines; measured on
  0.74.5), so the dashboard adds them back to size itself.
- 2026-10-14 (hybrid layout): the sidebar folds with a swap layout. Measured
  on zellij 0.45.1: `swap_tiled_layout` in a `new-tab --layout-string` works
  when the tab's template is named (an inline template with a plugin pane
  fails to parse), `next-swap-layout -t TAB` takes ~45 ms, keeps the panes
  and works on tabs you are not looking at; resizing in 5% steps could not
  reach a 3-column rail, and `override-layout` changed nothing. Both widths
  are percentages so the sidebar is resized, and told, whenever the window is.
- 2026-10-14: Alt j / Alt k in the weaver session are zellij's own
  GoToNextTab / GoToPreviousTab (instant, no floating pane), which is why
  thread tabs are kept in their numbers' order; home is in that cycle. Alt y
  needs a choice (home or back), so it runs `weaver go home` (~230 ms).
- 2026-10-14: the session's keys are added at the end of your own keybinds
  block, not in a second one: within a block the later binding of a key wins,
  while a second `keybinds` block loses to the first (measured).
- 2026-10-14: a fixed-column pane keeps its columns when its neighbour closes
  (measured), so the agent's and the sidebar's widths are percentages.
- 2026-10-14: After trying the weaver session, Rob turned the sidebar off: home
  on Alt y already shows every thread when he wants it. Thread tabs are nvim
  and the agent again; `WEAVER_SIDEBAR=1` brings the sidebar back.
- 2026-10-14: On an empty board fzf skips any action whose template names a
  field (`{1}`, `{2}`), `transform` included (measured on 0.74.5 with
  `--with-shell`), so home's `n` decides on `$FZF_TOTAL_COUNT` and prints the
  `{2}` it needs.
- 2026-10-15: zellij 0.44 to 0.45.1 on macOS crashes its server with heap
  corruption detected in the screen thread (zellij-org/zellij#5423, open; four
  crashes on one Mac in two days, 36 s to 56 min after starting), taking
  every agent with it. Threads now survive it: a thread whose agent died with
  its zellij is kept as lost and resumed (`weaver resume`, and the weaver
  session's home as it starts). zellij's own resurrection was the wrong tool:
  it saves each pane's running command line, which after weaver's exec is
  `claude <task>` (a new conversation sent the task again) and an nvim on the
  old pane's socket, so the weaver session turns it off. Only threads are
  kept: an agent you started by hand is resumable through `claude --resume`,
  and keeping every one would fill the board after each zellij quit.
- 2026-10-15: "Its zellij went" is decided from the server process, not the
  SessionEnd hook: at a crash every agent gets SIGHUP and reports reason
  `other`, as it does when you close its pane, and the hook's background work
  may die with the pane. The server (`zellij --server .../<session>`) either no
  longer runs or started after the agent's last event; `/exit`, `/clear` and
  logout (SessionEnd's reason, kept as `end_reason`) are never lost.
- 2026-10-15: The home board restarts itself when weaver is installed again.
  On the work Mac a board started before crash recovery was installed showed
  the new weaver's `LOST` rows (its reloads run the installed script) under the
  old weaver's keys, which knew only agent rows: enter and x did nothing.
  Bindings live in the long-running board, so the board, not each key, follows
  the install.
- 2026-10-15: Rob asked for an easy way to quit everything. zellij's `Ctrl q`
  already does, from any pane, and since lost threads come back it loses
  nothing, so weaver names it in the footer and help rather than rebinding it.
  `weaver quit` asks first and works from outside zellij, for a session left
  running behind a closed window.
- 2026-10-15: weaver can run on tmux, beside zellij, behind one `mux_*` layer.
  zellij 0.45.1 crashed its server five times in three days on the work Mac
  (once in the test suite's own session, 50 s after it started), taking every
  agent with it; Rob found the tool too brittle to rely on. tmux's server is
  mature and rarely crashes, does what zellij needed workarounds for (focus
  from the command line, absolute pane widths, window indices as thread
  numbers), and runs headless, so its tests need no client. Herdr was weighed
  and set aside: one server for every pane as in zellij, with open crashes of
  its own, and pre-1.0. zellij stays the default until the tmux session has
  been tried.
- 2026-10-15: tmux is the default. After two more zellij crashes the same
  afternoon (both in the test suite's own session, one in each of two runs of
  `65-threads`), Rob asked to productise the tmux version and move to it as
  soon as it was ready. zellij stays one setting away (`WEAVER_MUX=zellij`),
  with its sidebar and fitted floating panes, which tmux does not have.
- 2026-10-15: Cmd+D reaches tmux as `ESC [ 100 ; 9 ~`, not as the kitty
  keyboard protocol's `ESC [ 100 ; 9 u` that zellij read as Super+d: tmux
  decodes that before its user keys are matched, as Meta+d, which is Option+d
  in Ghostty (`macos-option-as-alt`) and deletes a word in a shell. Nothing
  else claims the `~` form, so `user-keys` catch it; zellij no longer splits
  on Cmd+D once Ghostty sends it.
- 2026-10-15: zellij is benched (Rob: "focus on tmux"). Its code stays as it
  is; its suites move to `test/zellij.sh` and no longer run by default.
  `test/run.sh` is the contract test, the Neovim specs and the tmux suite.
- 2026-10-15: A pass informed by prior art (workmux, agent-deck, herdr,
  claude-squad, cmux, tmux-claude-hatch) and by Claude Code's, Codex's and
  tmux's own documentation: async hooks land in any order, so a late tool
  event of a finished turn (its prompt_id) never reopens it; Claude Code
  starts on a session id weaver chose (`--session-id`, named with `-n`), so a
  thread knows its conversation before its first hook and is never resumed
  with `--continue`; the server starts from `/`; seen marks a finished agent
  as you look rather than as a hook next fires; tmux redraws on an option
  change, so no client is refreshed by hand; and popups run weaver without
  the user's shell.
- 2026-10-15: An agent's row has named fields (`ROW_FIELDS`): `JQ_ROWS`
  builds each row as an object and prints the fields in that order, and every
  reader reads them by those names (`IFS=$US read -r $ROW_FIELDS`), so a field
  added, moved or dropped is one change in one place and never a shifted
  column. jq refuses to print a row whose fields and `ROW_FIELDS` disagree.
- 2026-10-15: Speed, measured on the work laptop, where a process costs about
  5 ms to start (most of it waiting on exec, not CPU), a tmux query about 7 ms
  and a weaver call about 18 ms (bash's start and the script's parse). The
  home board shows first: sweeping old states, restoring lost threads and the
  status push run behind it (`home_boot`), the push waiting for the board's
  socket. A row build is one tmux list-panes (its JSON built in bash), one jq
  (which also reads the Claude Code sessions and the time) and one ps for
  every agent's start time. The terminal is measured once per board; the
  board's side files are written only when they change; fzf's actions run
  weaver by bash rather than through env; a load is one transform that sends
  the header and footer inline (`fzf_act` picks a delimiter the text lacks).
  Home board start: 840 ms to 540 ms in the lab; next, the hot paths
  compiled.
- 2026-10-15: weaver is one Go binary (Rob: "anything that can be should be
  compiled", a standalone installable app, no symlinks). It carries the
  shell script, the Neovim plugin and the configs (`go:embed`), unpacks them
  once per build under `$XDG_DATA_HOME/weaver/runtime/<content hash>`, and
  runs the script (by bash, with `WEAVER_SELF` the binary and
  `WEAVER_RUNTIME` the unpacked files) for every command it does not do
  itself yet; under zellij (benched) the script does everything. Compiled
  first, the dashboard: its rows (`_rows`, with the agents' refinement by
  Claude Code's session files and the retiring of agents gone), header,
  footer and chrome, the board itself (`ls`, `home`, whose start-up work runs
  behind it as `weaver _boot`) and the status line's counts; rows match the
  script's byte for byte. Measured: `_rows` 78 ms to 18 ms (one tmux query
  left, and no ps: process start times come from sysctl), `_chrome`,
  `_footer` and `_hf` about 20 ms to 4 ms. Next: the hook engine, then the
  rest of the script, then packaging (a Homebrew formula with the
  dependencies).
- 2026-10-15: The hook engine compiled (`hook`, `event`, `adapt codex`,
  `snapshot`): the event and the stored state are read in-process (no jq),
  the state machine and the state file are as the script's (the suites pass
  unchanged), and the background work runs as a detached `weaver hook --bg`
  given the run in `WEAVER_BG`. nvim's events go over its socket as
  msgpack-RPC (neovim/go-client), where the script started an `nvim
  --remote-expr` client for each; dashboards reload by an HTTP POST to fzf's
  socket from Go, where the script ran curl. Measured on the lab agent, a
  sync hook about 50 ms to 10 ms, an async one that changes nothing about
  65 ms to 13 ms. The script keeps its hook for zellij.
- 2026-10-15: Compiled too: the board's card and views (`_preview`,
  `_view`, the help and the lost card), its jumps (`_goto`), Alt y (`go
  home`), Alt 1 (`next`), tmux's hooks as you look and resize (`_seen`,
  `_fit`), `status --push`, and the home board's start (`_boot`: the sweep of
  tmux sessions' old states, `gc`'s, is Go; a restore of lost threads, which
  starts agents, is the script's `_resume`, run only when there is one).
- 2026-10-15: The new-thread form compiled (`new` without a task, `_form`,
  `_fetch`): its header asks git two things at once (`git remote`, and one
  `git for-each-ref` for the branch, the remotes' HEADs and main or master)
  where the script asked one at a time; the background fetch is a detached
  `weaver _fetch` (no perl). The thread itself is still the script's: the
  form hands it the choices as `weaver new` flags. n on the board to the
  form's first paint, in the lab: about 230 ms to 120 ms, most of the rest
  fzf starting.
- 2026-10-15: Compiled: `weaver` itself (attach to or start the session:
  one tmux query, then tmux in place of weaver), `edit` (Alt v) and the
  agent's `nvim` (its liveness check over RPC, then nvim in place of
  weaver).
- 2026-10-15: Compiled: threads (`new` with every option, the thread's
  worktree with Worktrunk or git, its window, `_agent`), `resume` and the
  session's restore, `close` (its card, the worktree's removal) and
  `_close`, `send` and `quit`. On tmux the script now does only what the Go
  binary hands it: `help`, plain `status`, an explicit `gc` (zellij's states
  too), and zellij's own commands (sidebar, layout); under zellij, all of it.
- 2026-10-15: A thread closed before its nvim has bound itself (nvim waits
  for the agent to say which pane it is, so `.nvim` comes a moment after
  `.thread`) is closed whole: `close` finds that nvim as the pane in the
  agent's window started as `weaver nvim --thread TOK` (the agent's own
  start command names TOK), closes it with the agent, and removes the
  thread's record in `new/`. Before, its nvim counted as a stranger in the
  window, so the window and the worktree stayed, and killing it before it
  read its agent's pane left that record holding the thread's number for a
  day (until gc); `threadNum` now ignores a record there older than 30 s,
  since a live one waits at most 10. Found by `test/tmux.sh` on the
  personal Mac, which is fast enough to close before nvim binds.
- 2026-10-15: Visual QA on the personal Mac, from a rig that starts `weaver`
  on a throwaway tmux server inside a hidden terminal of a set size, with
  Rob's own Neovim config and fake agents. A thread's window is named with
  its label (`01 task · repo/branch`) as it opens, not when its agent first
  reports; the new-thread form and its pickers take the board's label
  grammar (`[ WEAVER :: NEW THREAD ]`); the help's headings are uppercase
  like the board's, and it says q and esc go back on home. Old snapshot
  chains and private indexes are pruned (see Snapshots).
- 2026-10-15: Neovim configs load weaver's plugin from
  `$XDG_DATA_HOME/weaver/nvim` (lazy.nvim's `dir`), which only the old
  install wrote: the Go binary unpacked its plugin under `runtime/<id>` and
  left that copy stale, and `weaver.lua` appended its own directory to the
  runtimepath, so `require` found the stale modules first, in every nvim,
  a thread's included. Now the binary keeps `weaver/nvim` a copy of its own
  plugin (marked `.weaver-runtime` with the build's id, swapped in whole when
  the build changes; `install.sh` runs `weaver _runtime` to do it at once),
  `weaver.lua` prepends its directory, and runtimes of other builds go once
  nothing has unpacked them for a month. Following also replaces a start
  screen (snacks.nvim's dashboard, dashboard-nvim, alpha, mini.starter): a
  new thread's nvim opens on one, and its agent's first edits waited
  behind it for good.
  `install.sh` removes what the install before the binary copied into
  `$XDG_DATA_HOME/weaver` (its script, itself and `config/`), once nothing
  ran from there any more.
- 2026-10-15: The nvims that hung in exit for good (`preserve_exit` →
  `getout` → `wait_return`, a known limitation since 2026-10-14) were nvims
  told to go all at once (tmux killed, a crash, quit) whose ShaDa writes
  raced: E886 creating its directory, E136 renaming its temp file. Nvim's
  exit then waits for a key, with no UI left to send one; reproduced in
  about one nvim in ten with six at a time (`test/soak/nvim-exit.sh`). A
  hung one also blocked Alt v for its pane number after a restart: its
  socket took 2 s to time out and was then taken for a busy nvim. weaver's
  nvims now, with no UI left (not `--headless`), write ShaDa themselves
  with errors silenced, skip nvim's own write, and end once the exit
  autocommands have run: none in 48 hung.
- 2026-10-15: Rob chose one faint divider between a thread's nvim and its
  agent over tmux's half-lit one (`pane-border-indicators off`, both border
  styles #616161), and kept nvim's 60 columns below 104, where the agent
  gets what is left.
- 2026-10-15: weaver runs on tmux only (Rob: drop zellij and the script).
  zellij had been benched since weaver moved to tmux, its suites unrun and
  going stale (the install suite still expected the copy-and-link install),
  and the bash script, 6,300 lines, did only `help`, plain `status` and an
  explicit `gc` for tmux. Those three are Go now and the script, zellij's
  layout, keys and sidebar, `WEAVER_MUX`, `WEAVER_SIDEBAR`, `status
  --zjstatus`, `install.sh --zellij-config` and the zellij suites are gone.
  What the suites tested that still holds moved to the tmux harness: hook
  latency (whose fake nvim had been bound under zellij's key, so it measured
  nothing), the installer, and the states contract (a Go test now). A
  zellij session's leftover states are swept as their agents go, and the
  weaver session still resumes the threads a zellij one of its name lost.
