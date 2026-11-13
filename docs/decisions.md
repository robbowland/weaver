# weaver: decisions

Why weaver is the way it is: the choices behind it, with what was measured
or found that settled them. The contracts themselves are in
[design.md](design.md). Entries are grouped by topic and dated; a later entry
can supersede an earlier one, and says so.

## Hooks and state

- 2026-10-13: WorktreeCreate and WorktreeRemove hooks are not registered.
  In Claude Code 2.1.296 they replace git worktree creation and removal (the
  hook must print the new path), so registering them would break `--worktree`
  and EnterWorktree. Roots come from CwdChanged, the worktree tools and
  SessionStart's cwd instead.
- 2026-10-13: The agent's pid comes from `CLAUDE_PID`, which Claude Code sets
  for hooks. A hook whose `CLAUDE_PID` differs from the pane's recorded,
  still-running agent is a nested session (`claude -p`, an SDK script) and is
  ignored.
- 2026-10-13: Edits and every other tool-call event go to the async hook
  (`async: true`), so following costs the agent nothing; the synchronous hook
  keeps the events whose state must be in place before the agent goes on, or
  that ring the bell. nvim, Worktrunk, git and the status counts are told in
  a background process after the state is written.
- 2026-10-13: Done rings the bell too (`WEAVER_BELL=needs-you` for only
  needs-you): the multiplexer's window mark is what makes a finished agent
  "unread", and it clears when you look. A done agent whose pane is focused
  when the counts are computed counts as seen.
- 2026-10-14: Agents other than Claude Code report through `wvr event`
  (normalized JSON), mapped onto the Claude Code events the state machine
  already handles; an adapter per agent turns its own hook input into it.
  Codex's hooks mirror Claude Code's (without Notification, with Interrupt),
  so `wvr adapt codex` is a small mapping. Codex's tool names differ, so a
  tool call without a file path is treated like Bash: the files it changed
  are found with git status. Codex runs weaver's hooks only after you trust
  them in `/hooks`.
- 2026-10-15: From prior art (workmux, agent-deck, herdr, claude-squad, cmux,
  tmux-claude-hatch) and Claude Code's, Codex's and tmux's documentation:
  async hooks land in any order, so a late tool event of a finished turn (its
  `prompt_id`) never reopens it (2026-10-22: within 10 s of its end; later,
  the turn has carried on past a Stop hook that blocked its end, and is
  working); Claude Code starts on a session id weaver
  chose (`--session-id`, named with `-n`), so a thread knows its conversation
  before its first hook and is never resumed with `--continue`; seen marks a
  finished agent as you look rather than when a hook next fires.
- 2026-10-15: A working thread's row says what it runs (`running npm test`),
  as do its card (`runs`) and its nvim's bar: an async PreToolUse on Bash
  records the command; its PostToolUse (paired by `tool_use_id`) or the
  turn's end clears it. Async, so the agent never waits on it; since async
  hooks land in any order, a PreToolUse whose tool has already finished
  (`done_ids`) sets nothing. Before, a working row showed at most the last
  file edited, and nothing through the long commands (tests, builds) a turn
  spends most of its time in.
- 2026-10-15: `wvr setup` puts weaver's hooks into Claude Code's settings
  and Codex's hooks: every weaver hook out (in any form, under its old name
  agentnav too), the current ones in, run by the binary's path, nobody
  else's touched, members kept in their order, a backup first. So a weaver
  installed as a package needs only `wvr setup`, and nothing of weaver
  needs jq but its tests.
- 2026-11-12: A Bash command's or an MCP tool's edits are no longer
  followed (Rob), superseding the entries on following them: Claude Code's
  bashEditDiff, else git status since the pane's last Bash call, in the
  repos the command named, and MCP tools whose names say they write. It
  fed only the board's `editing <file>` and the card's `files`, which Edit
  and Write feed from the hook's input with no machinery, as they feed the
  agent's root. A Bash-made file no longer shows in either.

## Snapshots and reviews

- 2026-10-13: The snapshot chain of an agent session lives under
  `refs/worktree/weaver/sessions/<sid>`, beside one `reviewed` ref per
  worktree. The first snapshot is a root commit, so a chain is
  self-contained.
- 2026-10-13: Snapshots are forked from the synchronous hook rather than run
  from a separate async hook entry, because an async entry would race the
  synchronous one for the turn number.
- 2026-10-14: One review view. `nvim/lua/weaver/diffview.lua` (no agent
  knowledge) tags the view's tab with `diffview_scope` (identity) and
  `diffview_context` (label); any tagged view is "the" view, so your own
  config's diff keys (which can call the same module) and weaver's scopes
  never stack. Comparisons are a closed variant (`index`, `worktree`,
  `branch`, `commits`); whether a view needs the private index is derived
  from it.
- 2026-10-14: Untracked files in reviews. diffview hides untracked files
  whenever the left side is a commit, and `git diff <commit>` calls a file
  that is in the commit but untracked DELETED. Such views run git with
  `GIT_INDEX_FILE` pointing at `$(git rev-parse --git-path weaver)/view-index`,
  a copy of the real index with untracked files intent-to-add, rebuilt before
  each listing (skipped when neither the index nor the untracked set changed).
- 2026-10-14: The bar shows a finished turn's size and the review key
  (`2 files ␣at`), from the end snapshot (one `git diff --shortstat`), and
  task progress while the agent works (`tasks` in the state file).
- 2026-10-15: Old session chains and the private indexes nothing has used
  for a month are pruned (see [Snapshots](design.md#snapshots)).
- 2026-10-29: A review is of the worktree the agent, or one of its
  subagents, last edited or moved to (the root of `edit`, `root` and `focus`
  events; a snapshot's root is any worktree its turn touched, so it does not
  count), whatever Neovim's directory; inside the view, of the view's. It had
  been the current tab's, so only following's tab per worktree pointed a
  review at a subagent's worktree. Rob wants the review of the agent he
  picks in Claude Code's agent list, but Claude Code tells hooks nothing of
  that selection, so the latest edit is the nearest signal.
- 2026-10-30: Each agent gets its own chain in every worktree it changes
  (see [Agent chains](design.md#agent-chains)), reviewed with
  `:AgentDiffs`. In a sample of busy sessions, subagents made 89% of the
  edits, in 4 to 23 worktrees the main thread never touched, and background
  subagents keep editing after the main thread's turn has ended, so a
  turn's diff neither separates agents nor covers their work. The unit is
  the agent in a worktree; within a worktree an agent's tip takes only its
  own files, so another agent's files and your edits stay out (one file two
  agents edit is shared: hunk-level attribution was left out).
- 2026-10-30: Agent chains are commits, not patches in the activity log:
  diffview reviews commits, and a patch can be far over the log's 4 KB
  line. The start comes from what the call says it changed, observed in
  Claude Code 2.1.312's PostToolUse (Edit and Write's `originalFile`,
  absent from about half of Edit calls in that sample, then
  `structuredPatch` undone; Bash's `bashEditDiff`, on in auto mode), so it
  is taken in the async hook process: the before content is too big to
  hand to the background run through its environment. A snapshot of only
  the call's paths took 41 to 63 ms on 10 000 to 50 000 files, against 55
  to 146 ms for the whole worktree.
- 2026-10-30: Review in place (`:AgentInPlace`) is gitsigns with its base
  set per buffer to the branch's root, in the worktree under review only (a
  global base would apply one worktree's root to every other's files), and
  the quickfix list for stepping, with your own keys for both: nothing new
  to learn, nothing opened or moved on its own. Commit by commit, a buffer
  can only show the file as it is now, so each commit's hunks are placed on
  today's lines and marked where a later change rewrote them; the exact
  commit is the review view's. One `git log -p` (first parents: a merge
  moves lines but lists nothing) is mapped forward commit by commit onto
  HEAD's lines and kept until HEAD moves; each refresh is then one diff,
  run without waiting, and only ever replaces the review's own quickfix
  list (by its id), so another list you made stays current. A buffer is
  under review when gitsigns has it in that worktree (nested worktrees and
  submodules are others), and its base is what gitsigns says it is.
- 2026-10-30: A chain takes only the files a call says it changed. The
  fallback following uses (what `git status` shows changed since the pane's
  previous Bash call) would credit an agent with another's edits, or yours,
  in the same worktree; a Bash change missing from a review was preferred to
  one in the wrong agent's. No private index is kept between calls: one
  per agent and worktree, each a copy of the real index, would have held
  hundreds of megabytes in a busy checkout for a month.
- 2026-10-30: An agent's review compares its chain's two commits, never a
  worktree's files, so no language server starts for another worktree (a
  file opened from it attaches one, and detaching later leaves the server
  running). diffview reads a comparison's revisions once, as commits; the
  view's adapter re-reads them at each refresh (`refresh_revs`, which
  diffview-plus calls and the git adapter leaves empty), so the view follows
  the agent's tip as it moves.
- 2026-11-11: Per-agent change chains are gone, superseding the entries
  above on them: they fed only `:AgentDiffs`, which went with the Neovim
  plugin. A helper is an agent with its own commits now (the weaver skill),
  so `git log` tells agents apart. What a tool call says it changed is still
  read, as paths, for following a Bash edit (`hooks.Changes`), and git's
  ignore list still filters what is followed; the before-content of an
  edit (`originalFile`, `structuredPatch`, bashEditDiff's hunks) is no
  longer read. A repository's existing `refs/worktree/weaver/agents/` refs
  are left as they are.
- 2026-11-11: Per-turn snapshots and the reviewed mark are gone,
  superseding this section's entries on them: the review is tuicr's over the
  branch's commits, the weaver skill has agents commit in small steps
  (policy commits replace turns), and tuicr marks what was reviewed. With
  them went `wvr snapshot --reviewed`, `turn_ended`, `turn_roots`, the
  `.prompt` file and the done row's file count (Rob, decision 4), which read
  the end snapshot; a done row shows the last reply. The turn counter stays:
  it reads no snapshot. A repository's existing
  `refs/worktree/weaver/{sessions,reviewed}` refs and private indexes are
  left as they are.
- 2026-11-11: The activity log is gone (Rob, decision 3), and with it
  `:AgentLog` and `:AgentWhy`, which went with the Neovim plugin: the log
  told the story of each turn for them, and tests read it as an oracle,
  which now read the state. So did what only the log carried: a prompt's
  long text, and what an Agent call asked its subagent to do. A
  `~/.local/state/weaver/log/` from before is left as it is.
- 2026-11-11: weaver's workflow is a skill, not machinery (Rob: policy
  over machinery): the `weaver` skill, carried in the binary and written as
  the hooks are, says how an agent works in a thread, which removable
  machinery (chains, snapshots, `wvr send`) used to stand in for. Its
  commit rule is Rob's decision 1: each agent commits only its own paths
  (`git commit -- <paths>`, again on `index.lock`), a file two agents
  changed going whole with the first. Review notes are tuicr's comments,
  read with tuicr's CLI (decision 2), and a helper reports back with
  Claude Code's cross-session `SendMessage` rather than by typing into the
  agent's block, which could answer a dialog. Its first rule says one
  worktree per thread where the scope's draft said one Rex session per
  worktree: weaver's threads are still windows of one session until that
  follow-up lands.
- 2026-11-11: The state's Rex session id is `rex_session`, no longer
  `tmux_session`, superseding the Rex-alone entry's note on it (Rob: rename
  it; backwards compatibility does not matter). A state written under the
  old name reads as no session until the agent's next
  event writes it again; if its session goes first, weaver cannot tell it
  went (no session to ask Rex about), so the dead agent's thread is
  forgotten rather than kept as lost, unless the machine restarted, which
  still counts as lost. Quitting with the old weaver before upgrading keeps
  every thread as lost, and those come back.
- 2026-11-12: A state weaver 0.12 wrote still names its session, under
  `tmux_session` (`agent.Record` reads it when `rex_session` is not there),
  superseding the entry above. Its claim that quitting first keeps every
  thread was wrong, found by carrying 0.12.2's threads to this build on a
  throwaway server: 0.12.2's quit keeps its threads' records, not lost ones,
  so read as no session they were forgotten, quit or crash alike, and the
  sweep then removed their worktrees. Upgrade from 0.12 only: it goes with
  the back-compat cleanup, once Rob runs this build.
- 2026-11-12: A state names its session as `rex_session` only, superseding
  the entry above (Rob: once he ran 0.13 from Homebrew). His six states
  still under `tmux_session` (four agents with no event since 0.13, two
  lost records) were renamed by hand first, as the fallback read them.
  Nor is a file of another key (tmux's, zellij's) taken any more for a
  session that never runs and is never lost, or a pane's `.bash` stamp
  removed with it: no weaver since 0.12 writes either.
- 2026-11-11: `WEAVER_MUX=rex` is gone, superseding the Rex-alone entry's
  note on it (Rob asked for a rename; it had no use left): weaver's blocks
  and keys run only in sessions weaver made, which it knows by its record
  of them, so nothing needs to tell it to act on another.
- 2026-11-11: `wvr event` is gone, superseding the 2026-10-14 entry on
  agents other than Claude Code: its normalized events had no reporter
  left once Codex went, and weaver is Claude first (Rob). An agent that
  needs one again brings it back with its adapter (this commit's revert).
- 2026-11-11: Codex support is gone (Rob mainly uses Claude Code),
  superseding the 2026-10-14 entries on Codex and the Rex-alone entry's note
  that Codex threads were refused: on Rex they were, so its adapter (`wvr
  adapt codex`), its hooks file, `--agent`, the form's agent row (alt+c),
  `WEAVER_CODEX`, `wvr setup --no-codex` and the Codex skill were dead. It
  is one commit, to revert as a whole. A Rex port could bring it back: what
  stopped it is that Codex runs every session's hooks in a daemon with the
  environment of whichever session started it, so a hook does not say its
  block; placing it would need the daemon's sessions mapped to Rex blocks
  (by a Codex process's own environment and directory, say).
- 2026-11-11: The skill's helpers are windows of the thread's session,
  superseding the entry above's blocks beside the agent and its one
  worktree per thread now that a thread is a session (Rob): `rex window
  new` in `$REX_SESSION`, the window id kept; never a split of the
  agent's window (its review's place) nor a session of its own (outside
  the thread). Finished helpers are the coordinator's to close (Rob: a
  sidebar full of finished helpers nobody closed): a helper reports and
  waits; the agent that started it checks nothing it owned is left
  uncommitted, then `rex window close`s it. weaver needs nothing more:
  a helper has no thread, so one whose window closes is forgotten, never
  kept as lost.
- 2026-11-12: The review panel needs Rob's fork of tuicr
  (robbowland/kegs/tuicr, 0.28.0-rb1 or later), as doctor checks by its
  version's `-rbN`. The fork opens an empty range with `-w` on the work
  tree (upstream's #804), diffs a range reviewed with `-w` from the range's
  own base so a merged-in base no longer shows, and has the opt-in keys
  weaver's quieter look uses (`show_brand`, `section_style = "rule"`,
  `show_total_stat`, the theme's `file_header`). Upstream's tuicr is
  replaced by hand: both formulae install a `tuicr`.
- 2026-11-12: The panel runs tuicr in one form whenever the branch has a
  base, `-r FORK..HEAD -w`, superseding the entries on its special cases:
  the fork opens it on the work tree while the branch has no commits of its
  own (no `-w` alone), and on its empty diff when the branch's commits
  change nothing in all (a commit and its revert), which upstream's refused.
  It waits, saying so, only on what the fork still refuses: no commits
  since the fork point and nothing uncommitted (tuicr 0.28.0-rb1 says
  "Error: No changes to review"). A base merged into the branch needs no
  rule of its own: the fork point moves with it, and the fork keeps the
  merged base out of the diff.

## Holding threads

- 2026-10-30: A thread can be put on hold (`h`, `wvr hold`): its agent and
  Neovim close, its conversation and worktree stay, and it waits under HELD
  with a note of what is next, until you resume it. Rob keeps idle threads
  open because they hold context he may want, and too many in flight is
  what he struggles with; closing one feels like losing it, as tab-hoarding
  research found (Chang et al., CHI 2021). A held thread is a lost thread's
  record with `held` and `note`, so resuming, forgetting and listing are the
  lost threads' own. The note is asked for because a plan for an unfinished
  task is what lets it go from mind (Masicampo & Baumeister 2011; Leroy &
  Glomb 2018 on attention residue), and is optional, since asking costs.
  Left out to stay lean: coming back on a date or an event (PR merged), a
  limit on how many are held, and an agent-written note; an age a shade
  brighter after a week stands in for a weekly look. Past three held, HELD
  folds the rest into one row, so holding many never crowds the threads
  you are working on.
- 2026-10-30: A limit on open threads (`WEAVER_WIP`, 5) only says when
  you are past it, where it matters: the board's count (`7/5`, a shade
  brighter) and the new-thread form's spare line, which says how to hold or
  close one. Limiting work in flight is what Personal Kanban rests on
  (Benson; 1 to 3 for one person, a rule of thumb, set higher here for
  agents that work on their own), but a hard cap that refuses the next
  thread would block urgent work; and the status line, drawn by each
  multiplexer, is left as it is.
- 2026-10-30: Edits on the board (rename, hold) are in place, in its search
  line, not a popup: the keys let go while one is open, and the prompt
  names the thread. Leaving one clears the query, re-enables the search and
  waits for it before hiding the input line: done in one go from a
  transform (fzf 0.74.5), hiding the input left the list filtered by what
  was typed.

## Neovim

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
- 2026-10-14: Quiet by default: routine "following" and "reloaded" messages
  are gone (they covered the winbar under noice); only warnings remain.
- 2026-10-14: The agent's nvim sets 'autoread' off: with it on, nvim reloads
  an unchanged buffer without asking FileChangedShell, so the changed line
  could not be found. weaver's FileChangedShell makes the same choice.
- 2026-10-14: A review is closed from the tab you came from: closing the
  current tab fails with E445 while noice has a float open in it.
- 2026-10-14: Neovim tests sit at the public boundary: a child nvim driven
  over RPC (real typed keys and modes), events through `WeaverEvent`, and
  assertions on buffers, tabs, the winbar, messages, quickfix and diffview's
  file panel. No test reaches into module internals.
- 2026-10-14: Shapes over flags. Hook events are parsed once at the boundary
  (`parse_event`: the contract's fields with the right types, seq a number);
  following works on Targets (`root` and/or `file`, never neither) and Plans
  (`new_tab` | `tab` with an optional show `in`/`beside` | `blocked`);
  `:AgentWhy` returns `unknown` | `unsaved` | `turn` | `between` | `before`,
  each described exhaustively; who asked is a literal (`agent` | `you`), not
  a force flag.
- 2026-10-15: Neovim configs load weaver's plugin from
  `$XDG_DATA_HOME/weaver/nvim` (lazy.nvim's `dir`). The binary keeps that a
  copy of its own plugin (marked `.weaver-runtime` with the build's id,
  swapped in whole when the build changes; `install.sh` runs `weaver
  _runtime` to do it at once), and `weaver.lua` prepends its directory to
  the runtimepath, so a stale copy elsewhere is never found first. Runtimes
  of other builds go once nothing has unpacked them for a month.
- 2026-10-15: Following replaces a start screen (snacks.nvim's dashboard,
  dashboard-nvim, alpha, mini.starter): a new thread's nvim opens on one, and
  its agent's first edits waited behind it for good.
- 2026-10-15: nvims that hung in exit for good (`preserve_exit` → `getout` →
  `wait_return`) were nvims told to go all at once (the tmux server killed, a
  crash, quit) whose ShaDa writes raced: E886 creating its directory, E136
  renaming its temp file. Nvim's exit then waits for a key, with no UI left
  to send one; about one nvim in ten hung with six at a time
  (`test/soak/nvim-exit.sh`). A hung one also blocked Alt v for its pane
  number after a restart: its socket took 2 s to time out and was then taken
  for a busy nvim. weaver's nvims now, with no UI left (and not
  `--headless`), write ShaDa themselves with errors silenced, skip nvim's own
  write, and end once the exit autocommands have run: none in 48 hung.
- 2026-10-19: A new worktree's tab takes a blank one (a start screen, or an
  empty unnamed buffer, alone in its tab) before it opens another. Rob found
  the tab line pointless: nvim's start screen stayed as tab 1, `[Scratch]`,
  once the agent's edits had opened worktree tabs beside it, and a
  worktree's tab opened for a `root` event stayed `[No Name]` until a file
  came.
- 2026-10-29: Following is gone, and with it `:AgentJump`, `:AgentFollow`,
  the bar's `→ file` and the tab per worktree; this supersedes the entries
  on following, start screens and blank tabs above. Rob hides the tab line
  and moves with file pickers, so the tabs piled up unseen: one nvim whose
  agent had sent subagents into ten worktrees held 18 tabs, each keeping a
  TypeScript, ESLint, CSS and Emmet server alive (14 GB, swap full).
  Unloading old hidden buffers did not help, since each tab kept its file in
  sight. What Rob wanted from it was a review of the worktree the agent is
  in, which reviews now find without tabs. Files still reload as the agent
  edits; 'autoread' stays off so that one handler decides every reload.
- 2026-11-11: tuicr takes the thread's Neovim's place beside the agent,
  superseding the per-thread Neovim of the entries above (its plugin goes
  later). Rob narrowed review to four needs, calling the rest bloat: the
  branch at once, commit by commit, notes for the agent, and Neovim on a
  file from the diff and back. tuicr 0.28 has each with no glue but a key
  that starts it, and the agent beside it makes the bar redundant. The
  panel runs tuicr in a loop rather than once: tuicr reads its range only
  as it starts, so `:q` (0.28's `q` no longer quits) starts it again on the
  branch as it is then, the cheapest fix while tuicr cannot re-read a range;
  the empty branch it refuses ("No changes to review", `-w` or not) is the
  loop's to wait out. It is given the fork point as a SHA, never the base's
  name: with `-r main..HEAD`, files main gained after the fork showed as
  deleted. The nvim its `e` opens is Rob's own, so weaver sets nothing that
  nvim would inherit, and close, hold and quit warn by finding nvim among
  the panel's processes, since no nvim weaver could ask over a socket runs
  there. Review notes are tuicr's saved comments, which the agent reads with
  tuicr's CLI.
- 2026-11-11: A thread is reviewed against the base its record keeps
  (Rob), superseding `$WEAVER_BASE` and `git config
  branch.<b>.weaverbase`, both gone, so weaver's store is the one source:
  a thread off release/2.0 was reviewed against main. The base is a
  branch's name, never a SHA (the remote-tracking one where the remote
  has it), and its fork point is worked out every round, so a rebase
  onto a newer base or the base merged in moves it; a base deleted falls
  back to the default branch. The panel fetches nothing, so a stale
  remote-tracking base is a limit.
  A pull request's thread follows its pull request's base as the GITHUB
  view refreshes it (Rob: no polling loop of its own).
- 2026-11-11: The hooks no longer talk to Neovim, superseding the
  msgpack-RPC of 2026-10-15 (Performance and packaging) and every entry
  above on what nvim is told: with tuicr beside the agent, no nvim weaver
  starts listens on a socket, so `wvr nvim`, the `WeaverEvent` calls, the
  `.nvim` binding, the socket and the sweep of nvims a server left behind
  went, and with them neovim/go-client.
- 2026-11-11: weaver's Neovim plugin is gone (`nvim/`, its specs, the
  diffview-plus fetch, `:checkhealth weaver`), superseding the entries above
  on what it did and the 2026-10-15 one on where Neovim configs load it:
  nothing of weaver's has a job in the Neovim behind tuicr's `e`, which
  loads only Rob's config. Its copy at `~/.local/share/weaver/nvim` is
  deleted once, with the plugins fetched for it, so a config that loaded it
  stops rather than keep loading the last copy forever. `wvr send`, which
  only the plugin's `:AgentSend` used, goes too: review notes are tuicr's
  comments, which the agent reads itself (Rob, 2026-11-11), and typing into
  an agent could answer its dialogs.

## Dashboard

- 2026-10-14: The dashboard is grouped by state under headings, chosen from
  mockups. From prior art: status first and the last reply in the row,
  progressive depth in a peek
  ([Claude Code's agent view](https://claude.com/blog/agent-view-in-claude-code));
  shapes that read without colour, 1-9 to jump, `?` for help, `n` for a new
  session ([agent-deck](https://github.com/asheshgoplani/agent-deck));
  waiting before done before working, a jump closes the dashboard, the
  agent's output in a preview
  ([workmux](https://workmux.raine.dev/guide/dashboard/)); sections
  ([gh-dash](https://github.com/dlvhdr/gh-dash)); `/` to search a list whose
  letters are keys (k9s, lazygit). And the usual TUI rules: groups apart by
  space, not lines; one border style; names left, numbers right; inverse
  only for the selection; a footer with the keys that work now.
- 2026-10-14: Seen done agents are under IDLE, not DONE, so each heading's
  count is the status bar's and DONE is what is new. Rows are in Alt 1's
  order (oldest first), not newest first: row 1 is where Alt 1 goes.
- 2026-10-14: Headings are items the cursor skips (a `focus` transform), not
  parts of multi-line items (`--read0`: the selection would invert the
  heading with its first row) nor fzf's raw mode with `down-match` (it needs
  a query that is always on, and page, first/last, mouse and reloads would
  still land on headings). Measured: about 1 ms per move.
- 2026-10-14: Searching does not reload: headings and gaps have nothing in
  the searched field, so a query hides them and an empty one brings them
  back, and the selection is never lost. Two fzf 0.74.5 quirks shape `esc`:
  `clear-query` from a transform's output does nothing, and `clear-query`
  does nothing while the input is hidden; so the binding is `clear-query`
  then the transform.
- 2026-10-14: The screen is on `s` and `tab`, not `ctrl-s`, which a
  multiplexer or the terminal (XON/XOFF) may take before fzf sees it.
- 2026-10-14: fzf exports `FZF_COLUMNS=0` to a `start` action, so the first
  rows are rendered before fzf starts, at the terminal's width with the
  preview where fzf will put it; reloads use `FZF_COLUMNS` and
  `FZF_PREVIEW_LEFT`. Inside `--border`, `FZF_COLUMNS` and `FZF_LINES` leave
  out the border and the padding next to it (4 columns, 2 lines; fzf
  0.74.5), so the dashboard adds them back to size itself.
- 2026-10-14: On an empty board fzf skips any action whose template names a
  field (`{1}`, `{2}`), `transform` included (fzf 0.74.5 with
  `--with-shell`), so home's `n` decides on `$FZF_TOTAL_COUNT` and prints
  the `{2}` it needs.
- 2026-10-14: The selection is pure invert, the row's own colours stripped
  (`fg+:#000000:strip,bg+:#ffffff`), in one `FZF_COLORS` for every fzf weaver
  opens.
- 2026-10-14: Rows run no git. Building them once ran `git rev-parse` and
  `git status` per agent: with 9 agents in three worktrees of a small
  repository, 166 ms before and 65 ms after, and in a large repository the
  old cost grew with each worktree. Now git runs once per new end snapshot,
  and the card's one `git status` runs in the preview, off the reload.
- 2026-10-14: The dashboard fits its pane, borderless but for fzf's own
  border, whose label holds the title; the header's blank line is where the
  search input goes (`--header-first`, `change-header` from a transform), so
  searching moves nothing. The list has priority over the card: at 80 x 30 a
  10-line card minimum cut the list mid-group.
- 2026-10-14: One glyph family, no emoji. In JetBrains Mono the first set
  had three sizes (`!` a thin stroke, `✓` raised, `●` heavier) and `↵` was
  not in the font; a Nerd Font circle set was consistent but an icon legend.
  States are task cells instead, a square with one mono mark (`x` done, `/`
  in progress, `-` cancelled, `!` in red) written as brackets (`[!]` `[x]`
  `[/]` `[ ]` `[-]`): every one three columns, brackets faint and the mark in
  its signal colour, everywhere a state shows (board, home, counts, nvim bar,
  Worktrunk's markers). Labels follow the same language: `[ WEAVER ::
  THREADS ] 5`, `NEEDS YOU · 2` captions, two-digit thread numbers,
  uppercase key labels (keys keep their case: `n` is not `N`), titles in ink
  only where a thread needs you. `⏎`, which the font has, replaces `↵`.
- 2026-10-15: The home board restarts itself when weaver is installed again.
  A board started before an install showed the new weaver's rows (its
  reloads run the installed binary) under the old weaver's keys, which did
  not know them: enter and x did nothing. Bindings live in the long-running
  board, so the board, not each key, follows the install.
- 2026-10-19: Keys are written one way everywhere weaver shows them:
  lowercase modifiers joined with `+`, then the key as typed, so a capital
  is Shift's (`alt+r`, `alt+R`, `ctrl+q`, `shift+tab`). Before, the views
  said Alt R, their menu R, the form alt-r (fzf's spelling, where alt-b and
  alt-B are different keys) and the footers ^q, and Rob could not tell
  whether Alt R wanted Shift (Rob: "lowercase modifiers and +... makes
  things much clearer"). One pair of functions (`keyLabel`, `fzfKey`)
  turns fzf's names into labels and a clicked label back into fzf's name.
  Alt R in a view, bound with and without Shift, is written `alt+r`, as the
  form's read-only switch is.
- 2026-10-22: The home board and the views start again in the weaver `wvr`
  resolves to now, not when the file at the path they started from changes.
  That missed a switch between installs (Homebrew's `bin/wvr`, and
  `install.sh`'s `~/.local/bin/wvr` ahead of it on PATH): the started
  path's file never changed, so a view kept the old weaver's keys under the
  new one's rows, and removing `~/.local/bin/wvr` left its keys running a
  binary that had gone. tmux's keys and the hooks run `wvr` from PATH
  already, so the board and views follow the weaver they do. `.born` keeps
  the real path a board started as: a switch either way is another file, an
  upgrade in place a newer one. A board's keys cannot run once their binary
  has gone, so its own process checks too, every 5 s (a few stats).
- 2026-10-22: A card you scroll stays where you left it. Every status push
  reloaded every board and view, and fzf shows a reloaded preview from its
  top, so an issue's card jumped back as any agent ran a tool. A refresh
  now reloads only rows that changed, which settles the views; the board's
  rows change as agents work (an age, what one runs), so a scrolled card
  also holds its list until the cursor moves or the list is off screen.
  Holding the list rather than restoring the scroll: fzf neither reports
  nor keeps a preview's offset, and tracking it means following every way
  of scrolling. Dragging the card's scrollbar is the one way not marked: it
  scrolls as before, without the hold.

## Threads

- 2026-10-14: The thread key is `Alt N`, beside the usual `Alt n` (new
  pane). Alt t would have been the mnemonic, but Claude Code binds meta+t
  (thinking), meta+p (model), meta+o (fast mode) and meta+w.
- 2026-10-14: The form is one fzf, its fields kept in a file that `weaver
  _form` updates from fzf's key bindings (`execute`, `transform-header`), so
  every field is a key away and enter starts at once. Its keys (tab,
  shift-tab, ctrl-r, alt-b, ctrl-e) avoid fzf's line editing.
- 2026-10-14: New branches start from the fetched remote default rather
  than from a possibly stale local `main`. The fetch starts when the form
  opens so enter seldom waits; offline it falls back instead of failing.
- 2026-10-14: `wvr close` offers to remove only worktrees `wvr new`
  made: agent tools' own worktrees (Claude Code's `--worktree`, Codex) are
  theirs to manage, and hand-made ones yours. Branches are never deleted,
  and nothing is merged: that stays with your own workflow.
- 2026-10-14: `wvr close` asks everything first and then finishes in a
  detached process that ignores the hangup, since closing a window can take
  the pane it runs in with it.
- 2026-10-15: A thread's window starts its agent first, then its nvim on
  the agent's left, told the agent's pane (`wvr nvim --fresh AGENT`) and
  recorded as bound before `wvr new` returns. Before, the nvim started
  first and polled every 100 ms for a file the agent's pane wrote, so it
  started up to 100 ms later and, for that moment, belonged to no agent: a
  thread closed then left its nvim, window and worktree behind. `close`
  still finds such an nvim from its start command, and a thread record still
  being opened holds its number for at most 30 s.
- 2026-10-15: A thread's window is named with its label (`01 task ·
  repo/branch`) as it opens, not when its agent first reports; the form and
  its pickers take the board's label grammar (`[ WEAVER :: NEW THREAD ]`).
- 2026-10-22: The thread being made shows in a frame like the form's. The
  form's popup has no border of tmux's (fzf draws the form's, with a label
  that changes), so the lines that follow it (the worktree, Worktrunk's
  report, a failure and its buttons) showed bare, unlike the form before
  them and every card. weaver draws the frame itself rather than giving the
  popup tmux's border, which would put a second border round the form.
- 2026-11-11: Each thread is a Rex session of its own (Rob), superseding
  the 2026-10-15 entries' thread window in weaver's session and placing it
  at its number: home's session keeps home and the views; a thread's holds
  its agent and review in its first window and its helpers' windows beside
  it. Rex finds a block only in its own session, so weaver's registry
  records each number's session (`in/<num>`) and a thread's session record
  names home; `rex.Fleet` routes each call. The session is labelled with
  the thread's name as it is made and never again, and nothing finds a
  thread by a label: Claude Code's agents are told to rename their Rex
  sessions, and anyone may. Closing or holding a thread ends its session,
  its helpers with it; Rex's sidebar keeps the order sessions were made in
  (Rob: the board keeps the numbers). After a server restart the husks Rex
  brings back (home and the threads' sessions) are ended and each lost
  thread resumes in a new session.
- 2026-11-11: alt+j and alt+k are Rex's own next and previous session
  (Rob, choice a), superseding weaver's window cycle (`wvr go window`): a
  thread is a session now, and the app already steps through its sidebar.
  They are bound in weaver's sessions only, through `rex.invoke`, so in
  others they stay the terminal's.
- 2026-11-11: Seen as you look is gone (Rob, choice a): a done thread is
  seen when weaver takes you to it (alt+1, the board's enter, alt+v) or
  when its agent is prompted. Home's board followed home's events alone
  (`rex events --session`), and Rex does not say which session an app
  shows, so a watcher would need one stream per thread for a guess.
- 2026-11-11: Adopting an agent resumes its conversation in a new thread
  session (Rob, choice a), superseding adoption in place: a thread is a
  session of its own, and an agent weaver did not start runs in someone
  else's (a helper's window in a thread's session, a pane of home's). Its
  agent ends with its pane first, and only between turns, so no turn is
  cut off and no two processes share the conversation.
- 2026-11-11: Helpers are off the board (Rob), and a thread's own
  board, alt+Y (alt+shift+y), lists its agent and its helpers: one board,
  scoped to the thread's Rex session, not a second one. An agent in a pane
  of home's session, no thread's helper, stays on the board, to be adopted
  (`a`). A helper is linked
  to its thread by the Rex session its events come from, matched against
  the session the thread's agent reports from, never by a label. Defaults
  Rob may veto: a helper waiting on you makes its thread need you on the
  board; alt+1 goes to the helper's own window; the thread's board names a
  helper by its window's label (the name it was started with); alt+Y
  outside a thread is alt+y.
- 2026-11-11: A helper's thread is the session its pane is in, as Rex
  lists it, superseding the entry above's events (review): a state's
  `rex_session` is a hook's copy, and a resumed thread's said home's until
  its agent's first hook, hiding home's agents as helpers and its own
  agent from its board. A resumed state takes its pane's life as the pane
  starts.
- 2026-11-11: A jump to a thread whose wait is its helper's (enter, a
  number) goes to the helper's window, as alt+1 does, and alt+1 stops at
  a helper only when it needs you, never when it is merely done (Rob).
- 2026-11-12: Worktrunk is required (Rob). weaver makes and removes a
  thread's worktree with `wt` alone; the plain-git fallback (`git worktree
  add` at `../<repo>.<branch>`, `git worktree remove`, `WEAVER_WORKTRUNK=0`)
  is gone: one path to keep and test, and your Worktrunk `worktree-path` and
  hooks always apply. `wvr doctor` checks for it as it does for tuicr, and
  `--fix` installs it.

## Work items and views

- 2026-11-11: A thread whose own window is closed by hand while its
  helpers run is closed: its session ends with them (Rob, review V9).
  Before, the thread was forgotten and its session lived on with no
  thread; kept as lost instead, it would be resumed into a second session
  beside the first.
- 2026-10-16: Threads start from a pull request or an issue, seen in the
  best tools for them rather than a picker of weaver's own: gh-dash for
  GitHub (12.6k stars, sections as tabs, custom keys with the row's
  `RepoName`, `PrNumber`, `HeadRefName`, full hex theming) and
  [k1-c/linear-tui](https://github.com/k1-c/linear-tui) for Linear, chosen
  over roeyazroel/linear-tui (more themes, but it runs its own agent and
  gives no way to read the selection) because it is built for agents:
  `linear-tui context --json` reports the row under the cursor and
  `issue show --json` the issue with Linear's branch name. Each runs in a
  view, a window beside home that Alt y steps through (thread, home, each
  view, back), with a strip on the status line. Contrabass and Symphony
  poll a tracker and run agents unattended, the opposite of what is wanted;
  workmux, agent-deck and claude-squad start nothing from an item.
- 2026-10-16: Alt N and Alt R open the new-thread form filled in, rather
  than start at once: you see the branch and repository before anything is
  made, and pick a checkout when weaver knows none. Alt R is read-only:
  Claude Code in plan mode, Codex in a read-only sandbox, and a pull
  request's nvim on the review against its base.
- 2026-10-16: The branch is the join key, as the 2026-10-14 brief proposed:
  nothing is written back to GitHub or Linear, and a branch that already has
  a thread goes to it. The item's text is fenced between marker lines with a
  random nonce and said to be untrusted, since anyone who can comment wrote
  it.
- 2026-10-16: gh-dash runs on weaver's config alone (Rob: no several
  configs to keep in mind), written whole as the view starts under a config
  home of its own, gh's sign-in left where gh keeps it. Merging his own
  config was the first try and showed why not: his gh-dash read
  `GH_DASH_CONFIG` (another file), gh-dash 4.0 replaces the global config
  with `GH_DASH_CONFIG`'s while later ones layer them, and either way what
  the view showed depended on files outside weaver. In GITHUB, tmux passes
  Alt N and Alt R to gh-dash, whose own bindings know the row; `_from` opens
  the form from a detached process, so gh-dash, waiting on its command, goes
  on at once.
- 2026-10-16: GITHUB is weaver's own view, no longer gh-dash (Rob: "the whole
  point of those views is to start a thread on the associated
  worktree/branch with the initial context", and gh-dash's page did not
  look like weaver). gh-dash's worth for that was its data: the searches
  and the fields of a pull request they ask for, about 600 lines of its
  18,000, transplanted into `internal/github` with credit (MIT, Dolev
  Hadar), through `gh api graphql` so no new dependency. The rest was its
  own UI, the part that did not match. The view is the board's: rows under
  their sections, the thread an item has, a card that leads with what enter
  does. A search takes about a second and GitHub answers aliased ones in
  turn (9 s for six), so each is a request of its own, all at once (5 s),
  behind a cache that shows at once. gh-dash's in-view reading (activity,
  checks, files, approve and merge) goes: `o` opens the item on GitHub, and
  a review thread reads it in nvim. Backlog (Rob): the sections
  configurable, and a way to open gh-dash in full.
- 2026-10-16: Which tools weaver absorbs (Rob: "for gh-dash, linear-tui
  where we want just parts and to change a lot - better to rip it off, but
  for small tools that are like unix commands - just use them"): a big app
  of which weaver wants a part, and to change much, gives that part to
  weaver with credit; a small tool that does one thing (fzf, gh, git, tmux,
  Worktrunk) is run as it is.
- 2026-10-16: In gh-dash on Charm v2, Alt N reached weaver's bindings as
  `alt+shift+n`, so `alt+N` never fired: the stand-in gh-dash of the suite
  named keys as weaver did, and agreed with it. A stand-in agrees with what
  it is given; the real tool was checked by hand from then on.
- 2026-10-16: The views offer only what weaver does not (Rob's audit):
  gh-dash keeps its lists, search, preview and GitHub's own actions
  (approve, comment, merge, ready, labels, assignees, checks); its `C`
  checkout, which would check out into your main checkout, starts a weaver
  thread instead, and its `d` diff a weaver review; its notifications and
  repository-branches views go (the fork's `defaults.views`), as Queue and
  Watch cover the one and threads and git the other. linear-tui keeps its
  lists and Linear's own edits; its notes and hand-off to an agent work only
  under herdr, and Alt N covers them.
- 2026-10-16: A remote's GitHub repository is read from the URL as
  configured, not as `git remote -v` shows it: an `insteadOf` rewrite is how
  it is reached, not what it is (and the tests reach a local bare repository
  for github.com/o/r that way).
- 2026-10-20: GITHUB's Queue is reviews asked of you and pull requests
  assigned to you, shown whole; reviews asked of your teams are a section
  of their own, Team (Rob: "I still seem to be missing some PRs"). GitHub
  counts a review asked of any team you are in as asked of you
  (`review-requested:@me`), and a busy team's can far outnumber your own.
  Queue showed the 20 most recently updated, so one asked of you directly
  could sit past them and never show.
- 2026-10-20: A read-only thread from a view is alt+m (the form's "mode"
  row), not alt+r, and the form's agent switch alt+c, not alt+a: Rob's skhd
  takes alt+r and alt+R (yabai's rotate) and alt+a (space 1) for the whole
  system, so those never reached weaver, which is why Alt R seemed broken.
  Keys a window manager is likely to hold (alt with a, s, d, f, g, q, e, r,
  z here) stay out of weaver's.
- 2026-10-20: A thread's name is a field of its own in the form (`name`,
  alt+n; `wvr new --name`), kept as its record's `title`, not something
  derived from the task or set by a rename afterwards. A thread from a pull
  request or issue is named for it by default (`#45 Add a flag`), so a
  review is called by the pull request's title, not by "Review #45" or the
  agent's paraphrase (Rob: "it should take the PRs title not the task
  written"). A plain task's default stays its agent's title, which
  summarises it better than its first line; the branch still comes from the
  task. A read-only thread's default is `review :: #45 Add a flag` (Rob:
  "titles for review threads should be prefixed with review", with "::" to
  match weaver's labels, `WEAVER :: THREADS`), so a review and work on the
  same item read apart on the board; the name row shows the prefix, so it
  is yours to keep or drop.
- 2026-10-22: A section shows more than its cap a page at a time (Rob:
  "There should be a way to get more than the cap"), not a bigger cap: a
  search costs about a second whatever its size, and the caps keep the
  first look fast. `m` (more; free in the views, and no window manager
  takes it) reads the next page of the section under the cursor, and the
  line after a section's items, `⏎ more`, is its button. A refresh's
  answer stays what each section is, and a page read before it is never
  mixed with one after (a page is kept only if its section still ends
  where it was read from). Linear counts no list, so its heading says
  `30+`. (2026-10-22: a refresh asks each first page for as many items as
  its section holds, up to one request's, GitHub's 100 or Linear's 250, so
  pages read are not lost to the next refresh; deeper than that, a refresh
  would cost a request a page, every five minutes.)
- 2026-10-22: A view's `/` stays fzf's fuzzy match (Rob: "The fuzzy search
  likely needs improving too"): fzf was not what fell short. Its search
  line began with the thread's number and cell, so digits and short words
  matched noise, and a number matched every item with those digits
  anywhere. The line is now what people search by, and a query that reads
  as a key is searched for exactly, the item it names first: through fzf's
  `search` action, not a reload of rows weaver ranks, since fzf holds
  typing while a reload finds the row it was on, and fast typing lost
  keys.
- 2026-10-22: Quick filters (Rob: "maybe quick filters as well, like
  assigned to me etc etc") are searches of the service, not narrowings of
  what is loaded: what is assigned to you may be past every section's cap.
  `f` picks one (fzf, as the form's pickers), and its answer is a section
  of its own, SEARCH, first in the view and paged like the others, so the
  sections stay what they are beside it. weaver's own searches come first,
  then a repository's and a label's from what weaver has seen (a label
  within the repositories it was seen in: alone it would search all of
  GitHub), Linear's team likewise. GitHub's is asked of pull requests and
  issues alike, so `s` shows the other kind's answer, and its heading says
  how many there are; a review-asked filter finds no issues, which is
  simpler than kinds of filter. The search is kept in the view's cache, so
  a refresh asks it again; picking it again, or `esc`, ends it.
- 2026-10-22: A search can go to the service (`ctrl+s`, or enter when
  nothing loaded matches), since what is loaded is a few pages at most.
  GitHub's search syntax passes through as typed when it says where (a
  qualifier); bare words are searched within the owners of the
  repositories weaver knows, as GitHub's search is of all of GitHub and
  words alone would find strangers' issues first. Owners rather than each
  repository: few qualifiers, and a team's other repositories are what
  one usually wants. Its answer is SEARCH, as a filter's is. (2026-10-22:
  an item named, `o/r#45`, or a number alone, `#45`, in the repositories
  weaver knows, is read as it is: GitHub's search does not find an item by
  its number.)

- 2026-10-22: LINEAR's sections are a partition of the open issues that
  involve you, each in one: by role (assigned to you, else made by you, else
  followed) and state. Queue stays what is yours to do (unstarted or
  started); an issue of yours in Triage or the Backlog was in no section,
  so not loaded and not findable with /, and they have their own, Backlog,
  after Queue. A test checks the partition rather than the filters'
  wording.
- 2026-10-22: A whole id typed in `/` that the view doesn't hold is asked
  for once typing stops on it (600 ms, the query still that id), as
  `ctrl+s` asks: someone who types `ENG-42` wants that issue, and `/` alone
  answered with nothing. Only a whole id: words are asked for with
  `ctrl+s` or `enter`, since a search on every pause would ask the service
  for every half-typed word.
- 2026-10-22: A view's search lists an item once, and its cursor keeps its
  line through a reload. An item in two sections (SEARCH and QUEUE, say)
  showed twice while searching, and fzf, tracking rows by the item's ref
  alone, put the cursor back on its first row at every reload, the top of
  the list for SEARCH's. Rows are tracked by section and ref; only the
  item's first row is searched. An item that moves section across a reload
  leaves the cursor where it was rather than following it.

## Lost threads

- 2026-10-15: Threads survive their multiplexer's server crashing: a thread
  whose agent died with its session is kept as lost and resumed (`weaver
  resume`, and the weaver session's home as it starts). A multiplexer's own
  resurrection is the wrong tool: it saves each pane's command line, which
  after weaver's exec is `claude <task>` (a new conversation, sent the task
  again) and an nvim on the old pane's socket. Only threads are kept: an
  agent you started by hand is resumable through `claude --resume`, and
  keeping every one would fill the board after each quit.
- 2026-10-15: "Its session went" is decided from the server, not the
  SessionEnd hook: at a crash every agent gets SIGHUP and reports reason
  `other`, as it does when you close its pane, and the hook's background
  work may die with the pane. The server either no longer has the session or
  has a new one of its name; `/exit`, `/clear` and logout (SessionEnd's
  reason, kept as `end_reason`) are never lost.
- 2026-10-15: Quitting everything is one key, `Ctrl q`, from any pane: since
  lost threads come back, it loses nothing. `wvr quit` asks first and
  works from outside the session, for one left running behind a closed
  window.
- 2026-10-16: `Ctrl q` asks first while an agent is mid-turn (working, or
  waiting on you): its thread comes back, but the turn under way, and the
  command it runs, stop. Otherwise it quits at once: a question asked every
  time becomes a reflex and protects nothing (Raskin).
- 2026-10-19: An agent whose last event came before the machine started
  lost its session, whatever tmux says. A server's pid tells servers apart
  within one boot, not across two: after a restart, a weaver server that
  happened to get its old server's pid would have had every thread
  forgotten. The boot time is the kernel's, so the check is free.
- 2026-10-19: A conversation weaver did not start becomes a thread when you
  adopt it (`wvr adopt`, the board's `a`), never by itself (Rob, choosing
  between adopting by hand, every agent in the weaver session, and every
  agent anywhere). The 2026-10-15 reason for keeping only threads stands:
  keeping every agent would fill the board with one-off ones. What it missed
  showed on 2026-10-19: a restart kept the threads, but the long
  conversation that mattered had been started by hand outside tmux, and
  nothing could bring it in. An agent adopted where it runs is never
  restarted: `break-pane` moves its pane, process and all, so a turn under
  way goes on. One that runs nowhere is resumed through the lost-thread path,
  from a record its transcript gives. One running elsewhere is refused, not
  resumed beside it: two processes would interleave one conversation.
- 2026-10-19: `wvr adopt SESSION-ID` (and `resume`, and `rename`) work from a
  terminal outside tmux, into the weaver session, as `wvr quit` already did.
  Rob found adopting by session id did not work: the id is copied from
  wherever the conversation ran, usually a plain terminal, and adopt refused
  to run outside tmux.
- 2026-10-19: A thread can be renamed (`wvr rename`, the board's `r`; Rob:
  "there should be a way to rename the thread from weaver"). The name is
  weaver's, kept in the thread record and shown in place of the agent's
  title, not sent to the agent: Claude Code retitles a conversation as it
  goes, which would undo it, and typing into an agent's prompt while it
  works is not weaver's to do.
- 2026-10-20: A thread's agent can be restarted in place (`wvr restart`, the
  board's `R`) so an updated Claude Code or Codex takes over (Rob: after an
  update "I need to close and re-open the session but there's no good way
  to do that"). The pane runs with `remain-on-exit` off, so an agent that
  exits takes its pane with it: the old "enter resumes it" was zellij's.
  tmux's `respawn-pane -k` ends the agent and runs the pane's own command
  again in one step, so the pane, window, nvim and number stay, and the
  agent resumes through the same meeting place a restored thread's does.
  An agent mid-turn is left unless forced: restarting stops the turn.

## The weaver session

- 2026-10-15: weaver runs on tmux. Its server is mature and rarely crashes,
  does from the command line what other multiplexers need workarounds for
  (focus, absolute pane widths, window indices as thread numbers), and runs
  headless, so its tests need no client. Herdr was weighed and set aside:
  one server for every pane, open crashes of its own, and pre-1.0. See
  [History](#history).
- 2026-10-15: tmux redraws on an option change, so no client is refreshed by
  hand; popups run weaver without your shell; the server starts from `/`, so
  a worktree removed under it is never its directory.
- 2026-10-15: Cmd+D reaches tmux as `ESC [ 100 ; 9 ~`, not as the kitty
  keyboard protocol's `ESC [ 100 ; 9 u`: tmux decodes that before its user
  keys are matched, as Meta+d, which is Option+d in Ghostty
  (`macos-option-as-alt`) and deletes a word in a shell. Nothing else claims
  the `~` form, so `user-keys` catch it.
- 2026-10-15: One faint divider between a thread's nvim and its agent, over
  tmux's half-lit one (`pane-border-indicators off`, both border styles
  #616161). Below 104 columns nvim keeps 60 and the agent gets what is left.
- 2026-10-14: A sidebar of threads in every thread window (after
  [workmux's](https://workmux.raine.dev/guide/sidebar/)) was built and then
  turned off in daily use: home on Alt y already shows every thread when you
  want it. Thread windows are nvim and the agent.
- 2026-10-19: Alt y from a thread shows the board in a popover over it, most
  of the terminal, rather than switching to home (Rob: a popover "would be
  less jarring"; of a popover from a thread, only for the views, or for
  everything, he chose from a thread). The thread stays around it, so a
  look at the board and back moves nothing; Tab steps to the views inside
  it, as on home. Home stays the window you land on as the session starts
  or you attach. tmux shows one popup at a time and none over another, so
  what in the popover would open a dialog or go elsewhere ends it, and the
  `go home` that opened it, waiting on tmux's popup to close, then does it:
  a file between them carries the choice as wvr commands, which keeps each
  key's meaning the board's own.
- 2026-10-29: weaver can run on Rex as well as tmux (`WEAVER_MUX=rex`),
  while Rob tries Rex; tmux stays the default. Much of weaver on tmux is
  what a terminal multiplexer app already does: its status line, popups,
  keys and bell, a done agent's mark. Rex is pre-1.0 and one server holds
  every pane, which is why Herdr was set aside, so it is an option, not
  the base. What weaver needs of a multiplexer is one interface,
  `mux.Mux`, with tmux and Rex behind it (design.md); weaver's own
  policy (numbers, state, what counts as seen) stays out of both.
- 2026-10-29: On Rex, panes keep weaver's numbers. Weaver's files, records,
  the Neovim side and its commands name panes by number (tmux's `%3` is
  3), and Rex names blocks by UUID, so the Rex backend gives each block and
  window it meets the next number, in a registry of its own, rather than
  every record changing to strings. A block started in another's place
  takes its number, as a respawned tmux pane keeps its id.
- 2026-10-29: On Rex, threads are windows of one Rex session, as on tmux:
  moving between them is the server's (focus a window), where switching
  Rex sessions is the app's, which Rex lets a command ask for only with
  Remote Control on.
- 2026-11-11: weaver runs on Rex alone, superseding the 2026-10-15 entry
  (weaver runs on tmux) and the 2026-10-29 one (Rex as an option): Rob
  works in Rex now, and backwards compatibility does not matter. tmux, its
  config, its suites and every path that only tmux had went: its status
  line and counts file, the right-click menus (`display-menu`), the
  `_seen` and `_fit` hooks, `wvr --rex` and `--tmux`, and the last zellij
  remnants (the setup warning, `--zellij-config`, the resume of a zellij
  session's threads). So did what weaver did only on tmux: following Codex
  (its daemon's hooks were placed among tmux's panes; on Rex they are
  dropped and Codex threads refused, as before) and sweeping what a gone
  thread left running (told by tmux's environment). `mux.Mux` stays, with
  Rex behind it, minus the methods Rex never did (`MarkUnseen`, `Publish`,
  `Strip`, `Looking`); home's board shows the counts the status line used
  to. `WEAVER_MUX=rex` still makes weaver act on a Rex session it did not
  make, and state files keep `tmux_session` as the name of the session's
  id. The hook suites (`test/hooks.sh`) run on a throwaway Rex server, so CI,
  which has no Rex, runs only Go's tests and the Neovim specs.

## Performance and packaging

- 2026-11-11: weaver brings what its tuicr review panel needs on a fresh
  machine: tuicr (a needed tool, from homebrew/core), its Micrographics
  theme with the syntax theme beside it, and `diff_watch_interval_ms = 500`,
  which only tuicr's own `config.toml` sets (no flag or environment), so a
  `config.toml` is written only where there is none. weaver sets nothing
  for tuicr to find them elsewhere (`XDG_CONFIG_HOME`, `VIMINIT`): the nvim
  that tuicr's `e` opens would inherit it, and that nvim is Rob's own. Each
  file carries weaver's mark and is rewritten while it does; one without it
  is the user's and left alone, so Rob's own theme and settings stay.

- 2026-10-15: weaver is one Go binary, a standalone installable app. It
  carries its Neovim plugin and configs (`go:embed`) and unpacks them once
  per build under `$XDG_DATA_HOME/weaver/runtime/<content hash>`. It was
  compiled from a bash script in stages, hot paths first, each measured
  against the script and held to its output byte for byte (the suites ran
  unchanged):
  - the dashboard's rows: 78 ms to 18 ms (one tmux query, and process start
    times from sysctl rather than ps); its header, footer and chrome about
    20 ms to 4 ms; home board start 840 ms to 540 ms before compiling, by
    moving its start-up work behind the board;
  - the hook engine: the event and the stored state read in-process, nvim's
    events sent over its socket as msgpack-RPC (neovim/go-client) rather
    than an `nvim --remote-expr` per event, dashboards reloaded by an HTTP
    POST to fzf's socket rather than curl. A sync hook about 50 ms to 10 ms,
    an async one that changes nothing about 65 ms to 13 ms;
  - the new-thread form: one git query for the header where the script made
    several, the background fetch a detached `wvr _fetch`; `n` on the
    board to the form's first paint, about 230 ms to 120 ms, most of the
    rest fzf starting;
  - then everything else: threads, resume, close, send, quit, edit, nvim.
- 2026-10-15: The weaver tmux config and the hooks' settings come from the
  binary's own files, so an install reaches a running session as `wvr`
  next attaches, and a package needs no files of its own beside the binary.

- 2026-10-16: The command is `wvr` (Rob's call), short to type; the
  product, its repository, formula, tmux server and session, state and
  settings stay weaver. `wvr setup` replaces hooks run by `weaver` (and
  `agentnav`) like any old ones. An installed `~/.local/bin/weaver` is kept
  the same build while it exists, since Claude Code sessions started before
  run their hooks by that path, and a home board open since runs its keys by
  it; it is never made anew.

- 2026-10-16: `wvr` sets itself up as it starts (Rob: "configure things
  itself when it runs"): the hooks merged as `wvr setup` merges them, silent
  unless a file changes, and diffview-plus fetched in the background. Only
  as the `wvr` on PATH, so a build run from a checkout never points the
  hooks at itself, and only for an agent whose config directory exists.
  `wvr setup` stays, for a dry run.

- 2026-10-16: Installing is one command (Rob: "one command ideally for it
  and its dependencies"): `brew install robbowland/kegs/weaver` brings every
  tool weaver works with, and `wvr` sets itself up as it starts. What a
  package cannot do, signing in to GitHub and Linear, happens in place: a
  view not ready yet shows its need and runs the fix on one key. What
  weaver works with is known to wvr alone: `wvr doctor` names what is
  missing and the fix for each, and `wvr doctor --fix` does those it can
  (Homebrew installs, the hooks, the plugin, the sign-ins offered), no
  Brewfile beside it (Rob: "Can't that just be made into a wvr command?").
  Homebrew is the installer (Rob: "Isn't brew the installer so I shouldn't
  have to call install.sh by itself?"): the formula's `head` gives what is
  on main, and `install.sh` is for working on weaver only, a checkout's
  build ahead of Homebrew's on PATH.
  Every view is in the session, ready or not, so you see what you could
  have; `h` hides one you do not use.

- 2026-10-16: weaver is public (MIT) and released through Rob's tap,
  `robbowland/homebrew-kegs`, as `weaver` 0.1.0: built from the tag, with
  every tool weaver works with as a dependency (the tap's gh-dash fork and
  linear-tui among them). The formula lives in the tap only.
- 2026-11-12: Text is measured, cut and wrapped by charmbracelet/x/ansi
  (Rob): one library, in terminal columns (wide characters and emoji two,
  marks none), colours and links not counted. It replaced rex's own width
  table, markdown's and the views' rune counts, and the cards' wrap, which
  counted characters, so a CJK title overran its column. go-runewidth,
  which it brings, stays at v0.0.31 or later: earlier releases build their
  width tables as they load, 21 ms at the start of every hook (inittrace;
  start-up 10.8 to 23.1 ms). From v0.0.31 the libraries add 0.05 ms of
  init, and start-up is unchanged (10.55 against 10.50 ms median, 400
  interleaved runs each, load 7 on 12 cores).
- 2026-11-12: weaver no longer tidies up after an install from before
  0.13, superseding the 2026-10-16 entry's notes on the old names (Rob:
  once he ran 0.13 from Homebrew; backwards compatibility does not matter).
  `wvr setup` takes out only hooks run by `wvr`; one run by `weaver` or
  `agentnav` is left as anyone's. `install.sh` no longer removes agentnav's
  install or the copies the install before the binary left, nor keeps
  `~/.local/bin/weaver` as its build, and `wvr` no longer removes the
  Neovim plugin's copies under `~/.local/share/weaver`. Rob's machine had
  none of them left.

- 2026-11-13: A look at the fleet (the board's rows, its card, the
  status counts, alt+1) is one `rex do` against home: its Lua lists the
  sessions, attaches to each thread's that runs (a script's connection
  reaches only the sessions it is attached to: `session.view` of another
  answers "stream is not attached") and calls each terminal's methods by
  session (`block.method`). It was `session.list`, then a `rex do` for home
  and each thread at once: with six threads, eight processes of a 42 MB
  binary and two in turn (~45 ms); now one, `_rows` 43.8 to 28.7 ms median,
  `_preview` 47.6 to 32.9 ms, the same rows byte for byte. A Rex without
  `rex.call` or `rex.session.attach` (a script error, `-e:<line>:`) is
  looked at session by session as before.

- 2026-11-13: weaver speaks Rex's control connection itself rather than
  through the rex command. The server documents it (`GET /llms.txt` on its
  socket: a WebSocket, `rex.control.v1`, revisions negotiated in the hello,
  each later revision translated for older clients), so it is a contract,
  not the CLI's internals. A call through the rex command was a process of
  a 42 MB binary, 15 to 20 ms; over the connection 0.2 ms, after 0.7 ms to
  open it once per wvr. With nine threads (median of 20): `_rows` 31.2 to
  12.8 ms, `_preview` 32.2 to 12.5 ms, `status` 30.6 to 12.4 ms, about 5 ms
  of each the process starting; the same rows and card byte for byte. The
  client is weaver's own (RFC 6455's client half for one local socket:
  masked text frames out, pings answered, fragments joined), no library.
  What the connection makes it owns, so it gives that up at once (the rex
  command's connection ended with its call, which did the same). The rex
  command stays where there is no Unix socket, for the app's actions and
  popups, and behind `WEAVER_REX_WIRE=0`.

## Code

- 2026-10-16: An audit against Go's module layout guide, Google's and
  Uber's style guides and Go Code Review Comments found the port still
  shaped like the script: one package, everything a string, JSON built as
  text. The engine was reshaped in small behaviour-preserving steps, each
  under the full suite:
  - the records, the hook's input and the board's rows are typed structs;
    agent states, pane and window ids, work items (a sealed union), where a
    thread works and what it does with its item are types, and a switch
    over a closed set names every case (the `exhaustive` and
    `gochecksumtype` linters);
  - the state machine is a pure function, `agent.Transition`, with table
    tests for each event;
  - commands return errors and `main` says them, from a command table;
  - the domain and the adapters are packages, dependencies pointing
    inward: `agent`; `store`, `tmux`, `git`, `nvim`, `items`, `hooks`,
    `setup`; with `jsonx`, `oneline`, `files` and `cli` beneath them.
    `session` is the core the rest share (`Env`, its panes, the agents in
    them, lost threads, the status counts, the sweep); `engine`, the hook
    engine, runs on it, in the hooks' processes on every event; `wvr` is
    the application and its screens, its `Env` embedding `session.Env`.
  - `wvr` is not split further into threads, views and screens: they call
    each other by design (the form opens threads, closing one shows its
    card, the board lists lost threads), so packages would only export
    their insides to each other and give no boundary a reader could use.
  - golangci-lint runs in CI on new code, with a complexity ceiling that
    comes down as the big functions split (115, from 130).
- 2026-10-18: Tab between views, and a footer that holds still (Rob: "I
  feel like tab is more natural than alt + y for view shifting"; on the
  form's Tab, "it just seems like the footer jumps"). The strip is a tab
  bar, and Tab is a tab bar's key: on home and in a view, Tab and
  Shift-Tab are the next and previous view, round (Alt y stays, from a
  thread, where Tab is the agent's and nvim's). `s` alone keeps the card
  or screen, and a view's kind. The form's footer said what enter does on
  the row first, so every hint after it moved as the cursor did, and ^e
  went off start: its hints now keep their places, enter's slot as wide as
  its widest, ^e on every row.
- 2026-10-18: Descriptions in ink, and links that open (Rob: "should the
  descriptions be grey or white and the dominant (not sure how to express
  code)", "if the links can become command clickable"). Micrographics'
  own reading text is ink; grey (60%) is its metadata and faint (38%) its
  frames, Material's emphasis levels; its "soft" reading mode is the grey
  the cards had. The description is what a card is for, so its prose is
  ink, quotes and minor headings grey. Code then needs its own mark:
  faint backticks, the theme's way of showing a token (as `[ SRC: ]`),
  rather than a fill it never uses. Links are OSC 8 hyperlinks, as is the
  card's title, marked by their `[ SRC: ]` token alone (Rob: "I hate
  underlines").
- 2026-10-18: Cards' markdown in the Micrographics style (Rob: "push the
  markdown rendering even further in terms of the way different elements
  are displayed in the micrographics style"). The vault theme's own
  vocabulary, in a terminal's terms: sections under a faint rule with a
  faint index, capitals for titles and labels, `[ SRC: … ]` links, code
  in crop corners with its language as a label, callouts under an ink
  stripe with a bracketed kind, tables with faint capital headers and no
  inner rules. Task boxes take weaver's own cells, and strong text is
  bold, so it no longer looks like code.
- 2026-10-18: Dense rows, grouped (Rob, on the form with a blank line
  between every row: "I'm not sure the spacing between the lines actually
  makes this more readable ... the cognitive effort to read and find the
  right information has increased"). Nearness is what says things belong
  together (Gestalt proximity), so even spacing says nothing and makes the
  eye travel further; in a terminal the least gap is a whole line, twice a
  row (a GUI's "relaxed" row is a fifth taller); and people reading
  technical detail scan, which dense aligned columns serve. The form's
  rows are dense now, a blank line between groups only (what it starts
  from; where it works; who works); its popup is as tall as it needs,
  so its keys sit under its rows; the welcome's keys are dense too. The
  width stays a share of the terminal: paths are long.
- 2026-10-18: Room, and a form that holds still (Rob: "I don't think we
  should be afraid to use more space ... there's no reason to be cramped
  when we have loads of space"; "The new thread does jump around a bit
  with selections").
  - Dialogs were fixed boxes (the form 96 x 20) in the middle of a large
    terminal: they take a share of it now, within bounds, and the form
    and its pickers breathe (a margin, a line between rows) when there is
    room. The empty board, three lines at the top left, is a centred
    welcome whose keys are buttons.
  - The form moved the cursor back to start after every choice: the
    auto-advance that GOV.UK's testing and WebAIM find users trip on, and
    what WCAG 3.2.2 means by an unexpected change of focus. Focus now
    stays (or returns, after a picker, to what opened it).
  - Rows came and went as choices changed (a checkout's thread had no
    branch or base; a task naming an existing branch dropped base): every
    row stays, saying why it does not apply. A message's line is kept
    when it is empty, as validation messages reserve their space, so
    none pushes the rows.
  - Pickers sat two columns right and a line up from the form's grid, and
    opened on their first item: they keep the grid and open on the value
    chosen now.
  - weaver's fzf took your `$FZF_DEFAULT_OPTS` (its style, its keys):
    it runs with weaver's settings only.
- 2026-10-18: Transitions (Rob: "is the flow clear to a user or does
  changing a view result in jarring jumps and inconsistencies that
  increase mental fatigue"). Held to: orientation (where am I, how did I
  get here, how do I get back), continuity (what persists stays put),
  starting a transition where it was asked for (Material motion), one
  change at a time and no masking flash (NN/g on change blindness), and
  not moving things under the user (spatial memory).
  - The board's `n` replaced the board with a full-screen form, and `x`
    with a full-pane card, where Alt N and the menus open popups over
    where you are: all are the same popups now, one size each (the form
    had been 96 x 18 or 96 x 20 by where it was asked from).
  - Closing the thread you were in landed on whichever window tmux had
    shown last (a view, say): it lands home.
  - Going home from a thread left the board's cursor where it had been:
    it is on that thread's row.
  - A view's search pushed its list down a line (the board's takes the
    header's line): both do the board's.
  - Reloads emptied the list while their command ran (`reload`): they
    swap it whole (`reload-sync`). Resuming a lost thread showed the
    pane's own screen between (`execute`): it runs in the background.
  - The footers' toggles say where they go (`? CARD` while the help shows).
  - Left as it is: a row moves between the board's groups as its agent's
    state changes, with the cursor kept on it (`--track`). The move is
    the signal (a thread rising to NEEDS YOU); holding the order still
    would hide it.
- 2026-10-17: A polish pass against modern TUI conventions (Rob: "another
  visual and interaction pass ... a final super nice bit of polish using
  modern tui standards").
  - The strip is a tab bar on every window, a thread's name after it, so
    the views are a click away from a thread too (zellij's and tmux's
    tab bars never go).
  - Lists and cards have a scrollbar when they overflow (the help's last
    sections were cut off with no sign there was more).
  - The board leaves a gap between its list and its keys, as the views do.
  - Keys are written one way: `^r`, `Alt R` (footers had `Alt-R`, the
    form `alt-w`).
  - Pickers say where they are: `WEAVER :: NEW THREAD › WHERE`.
- 2026-10-17: Nothing piles up (Rob: "Make sure we clean up old worktrees
  and processes so nothings bloating storage"). Found: thread nvims left
  running (one at 1.9 GB) when a tmux server was killed under them, the
  embedded server outliving its TUI; 439 dead sockets; the worktrees of
  threads closed before their work had landed, or forgotten, never went;
  and the test suites kept every run's scratch dir (17 GB in /tmp).
  - GC ends weaver's nvims whose parent has gone and removes dead sockets.
  - weaver lists the worktrees it makes, and as a session starts removes
    those no thread or pane uses once nothing in them would be lost (the
    rule close's offer uses), branch kept. Keeping one as its thread closes
    makes it yours. Forgetting a lost thread no longer keeps its worktree
    for good.
  - The suites remove their scratch dirs and nvims unless a suite failed.
- 2026-10-17: No prompt is sent for you (Rob: "I don't think a default
  prompt should be sent when starting an agent either unless
  configured"). A thread from a pull request or an issue had its task
  written in (`Implement ENG-42`), and enter sent it with weaver's
  instructions, so the agent set to work on a task nobody wrote. The
  form's task is now empty; empty, the agent starts with no prompt and
  waits, the thread named for its item; written, it goes with the item as
  before. `WEAVER_ITEM_TASK=1` brings weaver's task back.
- 2026-10-17: The mouse, as modern terminal apps have it (Rob: "a lot of
  them are mouse supported as well which is a convenience we should have
  too"). What they share: btop ("all buttons with a highlighted key are
  clickable"), Textual (a footer key's click presses it, and it lights
  on hover), lazygit and yazi (click to pick, the wheel scrolls what is
  under the pointer), tmux itself (the status line's ranges, right-click
  menus); and the rule under them, that every mouse action has a key
  (WCAG 2.1.1), the mouse a convenience over it.
  - Hints are buttons: a footer's hint does what its key does when
    clicked (fzf's `click-footer` and `trigger`), across the board, the
    views, the form and its pickers, as the cards' choices already were.
  - The status line's strip and counts are buttons (tmux user ranges), on
    the release: tmux makes a second click soon after a first a double
    click, and its `MouseDown` never comes.
  - A right click opens weaver's menu of what is under it, each item with
    its key: a row's (its keys, sent to its list), an agent's thread, or
    weaver's own on the status line. tmux's default menus offered Kill,
    Respawn and Rename on weaver's windows, which end an agent or break
    its label behind weaver's back; they stay on Alt and a right click.
  - Not done: hover. fzf has no hover event, and lighting a card's
    choice under the pointer would either move what enter takes (a
    surprise) or mean a second highlight; tmux's menus light on hover by
    themselves.
- 2026-10-17: The new-thread form is rows you move between (Rob: "REALLY
  bad ergonomics, there's no way to pick a worktree ... I'd expect to be
  able to navigate via mouse and also arrow keys but instead I have to use
  awkward shortcuts that are situated a long way from the actual field").
  - The task is fzf's query; the choices are rows under it, `start` first,
    so typing and enter still start a thread. Up/down, tab and the mouse
    move; enter or a click acts on the row (a picker, or a switch), then
    the cursor is back on `start`. The footer says what enter does on the
    row and its key from anywhere: a hint beside what it changes, not a
    column at the far edge. The start row says what would start.
  - `where` is a picker: a new worktree, or any checkout of the
    repository (the main one, each worktree, one a thread works in marked).
  - No `--track` on its reloads: fzf holds keys while a reload looks for
    the tracked row, and typing lost them.
  - The same ergonomics everywhere: a card's choices (close, remove the
    worktree, forget, quit, a view's setup, a failed start, doctor's
    sign-ins) are buttons that ←/→, tab, enter and a click work, each
    with its key as before; enter's first pick is the one that keeps
    things as they are where the other undoes something. Pickers are
    menus, so a click picks (lists with a card, the board and the views,
    keep click to look and double-click to go).
- 2026-10-16: An interaction pass, against Nielsen's ten heuristics,
  Shneiderman's golden rules and clig.dev (Rob: "check that the
  interactions are all logical and the UX makes sense").
  - One meaning per key: `q` and `esc` go back (home's had gone on to
    GITHUB since the views came); `v` is the board's nvim as Alt v is
    everywhere; `tab` switches a view's kind as it switches the board's
    card; the form's fields are each on an Alt letter (`alt-w` where,
    `alt-a` agent), since `shift-tab` set a different field than `tab`,
    where it conventionally undoes it.
  - Recognition over recall: a view's `?` explains its keys, sections and
    signals in its own service's words, and the board's lists the keys that
    work anywhere; an empty view says it is empty; a failed refresh says how
    old what it shows is.
  - Feedback: enter or Alt R on an item with a thread goes there and says
    so on tmux's line.
- 2026-10-16: LINEAR is weaver's own too, as GITHUB became (Rob: "do the
  same porting for linear-tui, take what we need, strip what we don't";
  "keep the fork, but remove it as a dependency of weaver"). From
  linear-tui (Rust, 33k lines, MIT, k1-c): its issue fields and how a list
  of them is asked for, transplanted into `internal/linear` with credit; the
  two views are one (`itemsview.go`) over a source each.
  - Signing in is a personal API key, pasted into `wvr linear login`,
    checked with Linear, and kept in the Keychain (`security`, fed on stdin
    so the key is on no command line); `$LINEAR_API_KEY` wins when set.
    Not linear-tui's OAuth: its client ID is k1-c's registered app, and
    using it would be weaver passing as linear-tui; nor reading
    linear-tui's tokens, which keeps the dependency. weaver's own OAuth
    app would need registering and a callback server, for a read-only list.
  - A thread from a Linear issue works in the repository used last: Linear
    knows no repository, and linear-tui's view per repository did the same.
  - The card renders the description's markdown (Rob: "a better terminal
    markdown renderer for the PR descriptions"): glamour first, which
    looked right but took every `wvr` from 3.7 to 9.8 ms to start (chroma
    registers every lexer and style as it loads), on every agent hook; so
    a renderer of weaver's own over goldmark (0.3 ms), in its palette.

## History

- weaver began as agentnav, a bash script for Claude Code agents in zellij
  (0.44 to 0.45.1) with zjstatus for the status bar. Much of the zellij work
  was workarounds: zjstatus never released CLI pipes, so `zellij pipe`
  deadlocked the server after about 100 pushes and the counts came from a
  command widget polling a file every 2 s; `focus-pane-id` from the command
  line left a phantom focus, so moving to an agent went through tab
  switches and `focus-next-pane`; percentage sizes worked only in layouts,
  so a thread tab was a layout string; resizing went in 5% steps.
- 2026-10-15: zellij's server crashed with heap corruption in its screen
  thread (zellij-org/zellij#5423) five times in three days on one Mac, once
  in the test suite's own session, taking every agent with it. Lost threads
  were built for that, and weaver moved to tmux behind one multiplexer
  layer; tmux became the default the same day, after two more crashes.
- 2026-10-15: weaver runs on tmux only. zellij had been benched since the
  move, its suites unrun and going stale, and the bash script (6,300 lines)
  had been compiled down to `help`, plain `status` and an explicit `gc`. Those
  became Go and the script and every zellij part went. A zellij session's
  leftover states are still swept as their agents go, and the weaver session
  still resumes the threads a zellij session of its name lost.
