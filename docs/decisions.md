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
  `prompt_id`) never reopens it; Claude Code starts on a session id weaver
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

## Work items and views

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

## Performance and packaging

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
