# weaver design: contracts and decisions

The engine (the `weaver` script) and the Neovim side (`nvim/`) meet at the
contracts below. Change one only with both sides agreeing, and log the change.

## Files

| Path | What |
| --- | --- |
| `$STATE_DIR` | `${XDG_STATE_HOME:-~/.local/state}/weaver` |
| `$STATE_DIR/<skey>_<pane>.json` | state of the agent in zellij pane `<pane>` of session `<skey>` (session name with `/` as `_`); one JSON object on one line |
| `$STATE_DIR/<skey>_<pane>.nvim` | line 1: the pane id of the agent's nvim; line 2: that nvim's pid |
| `$STATE_DIR/log/<session_id>.jsonl` | the agent session's activity log |
| `$STATE_DIR/<skey>.status` | the session's counts for zjstatus: `needs-you done-unseen working` |
| `$STATE_DIR/<skey>_<pane>.prompt` | the current turn's prompt, for its snapshots |
| `/tmp/weaver-<uid>/<skey>_<pane>.sock` | the agent's nvim `--listen` socket |
| `$STATE_DIR/<skey>_<pane>.thread` | the thread record of the agent in pane `<pane>`, when `weaver new` started it (below) |
| `$STATE_DIR/new/<skey>_<token>/` | a thread tab being opened: `task` and `thread.json` until the agent's pane takes them, then `pane` (the agent's pane id) until the nvim beside it reads it |
| `$STATE_DIR/repos` | repositories threads were started in, most recent first |
| `$STATE_DIR/close.log` | what a `weaver close` finishing in the background said |

## State file

Readers must ignore fields they don't know and tolerate missing ones.

| Field | Type | Meaning |
| --- | --- | --- |
| `pane` | int | the agent's zellij pane id |
| `agent` | string | `claude`, `codex`, ... |
| `state` | string | `working`, `needs-you`, `done`, `idle`, `interrupted`, `ended` |
| `since` | int | epoch s the state began; `ts` is the last event |
| `seen` | bool | you have looked since it changed; unread = `done` and not `seen` |
| `session_id`, `prompt_id`, `transcript` | string | from the agent's hook input |
| `pid`, `proc_start` | int, string | the agent process; `proc_start` is formatted like `TZ=UTC LC_ALL=C ps -o lstart=` (and `procStart` in `~/.claude/sessions/<pid>.json`) |
| `title` | string | the session title, when the agent reports one |
| `turn` | int | +1 on each prompt of yours (not on subagent reports or task notifications) |
| `prompt`, `waiting`, `last_message` | string | your last prompt; what it waits on; its last reply |
| `cwd`, `root`, `start_cwd`, `project_root` | string | `root` is the repo or worktree it is really working in |
| `file` | string | the last file it edited |
| `files` | [string] | absolute paths edited this session, most recent last, at most 200 |
| `subagents` | [{id, type, since}] | subagents running now |
| `tasks` | [{id, subject, done}] | its task list (TaskCreated, TaskCompleted), at most 30 |
| `turn_ended` | int | the last turn that got its `end` snapshot |
| `turn_roots` | [string] | the roots the current (or last) turn touched |
| `background`, `background_tasks` | int, string | background subagents it waits on at Stop |
| `log` | string | absolute path of the activity log |
| `nvim_pane` | int or null | the agent's nvim pane |

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
`WEAVER_BIN` (this script) in nvim's environment.

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
`ZELLIJ_PANE_ID` unless the event names one. Adapters translate an agent's own
hook input into it (`weaver adapt codex`). `--bell` rings the terminal bell on
`/dev/tty` when the agent starts needing you or finishes, for agents that
cannot ring through their hook output.

| Field | For | Meaning |
| --- | --- | --- |
| `agent` | all, required | `codex`, `gemini`, `pi`, ...: the state's `agent` |
| `event` | all, required | below |
| `session` | all | the agent's session id |
| `pid` | all | the agent process (liveness); adapters find it at `session_start` |
| `pane`, `cwd`, `transcript`, `prompt_id`, `title` | all | as in the state file |
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
one tab: `zellij action new-tab --layout-string <kdl> --cwd <dir> --name
<name>`. The layout is the session's `new_tab_template` from `zellij action
dump-layout`, its bare `pane` (the tab's content) replaced by a vertical split:
`weaver nvim --thread <token>` on the left (close on exit), `weaver _agent
<token> <agent>` on the right at `$WEAVER_AGENT_WIDTH` (default `20%`) with the
focus. Without a template, the split alone.

Neither pane knows the other's id when the layout spawns them, so they meet
in `$STATE_DIR/new/<skey>_<token>/`: the agent's pane moves `thread.json` to
`<skey>_<its pane>.thread`, deletes `task` (read first), writes `pane`, then
execs the agent with the task as its prompt (`claude "<task>"`, `codex
"<task>"`); the nvim's pane waits up to 10 s for `pane`, removes the directory
and goes on as `weaver nvim <agent pane>`. So the agent's `.nvim` file and
socket exist before its first hook. Run again (zellij reruns an exited command
on enter), the agent's pane finds no `task` and resumes instead: `claude
--resume <session_id>` (`--continue` when unknown), `codex resume <id>`
(`--last`).

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
| `created` | int | epoch s |
| `pane` | int | the agent's pane |

The tab's current name is zellij's (`tab_name` in `list-panes`); weaver never
renames it.

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
