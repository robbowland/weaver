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

