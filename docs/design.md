# weaver design: contracts

The contracts of the engine (the `wvr` binary, `cmd/wvr` and `internal/`):
its files and their formats, and what it asks of Rex, tuicr and the agents.
Change one only with what reads it, and record why in
[decisions.md](decisions.md).

## Files

| Path | What |
| --- | --- |
| `$STATE_DIR` | `${XDG_STATE_HOME:-~/.local/state}/weaver` |
| `$STATE_DIR/<skey>_<pane>.json` | state of the agent in pane `<pane>` of session `<skey>` (`rex-` and the session name with `/` as `_`); one JSON object on one line |
| `$STATE_DIR/<skey>_<pane>.panel` | line 1: the pane of the agent's review panel; line 2: the panel's pid (`wvr _panel`'s), empty while not known (the panel writes its own as it starts); a pid of 1 or less is no process, never a live panel |
| `$STATE_DIR/<skey>_<pane>.thread` | the thread record of the agent in pane `<pane>`, when `wvr new` started it (below); `.closing` while `wvr close` ends it, so nothing keeps a thread being closed as lost |
| `$STATE_DIR/new/<skey>_<token>/` | a thread being opened: `task` (or `resume`) and `thread.json` until the agent's pane takes them |
| `$STATE_DIR/repos` | repositories threads were started in, most recent first |
| `$STATE_DIR/fetch/<repo path, / as %>.{pid,rc,err}` | a repository's background fetch for a new branch's base: its pid while it runs, then git's status (or `none`: no remote) and its errors |
| `$STATE_DIR/close.log` | what a `wvr close` finishing in the background said |
| `$STATE_DIR/github.json`, `linear.json` | what GitHub and Linear said last for the views' sections, each section with when it was read and why asking for it last failed; changed under `.lock` beside it (see [Views](#views)) |
| `/tmp/weaver-<uid>/<skey>.fzf.<pid>.sock` | an open dashboard's fzf `--listen` socket (the hooks reload it); beside it `.keys` (the fzf actions for 1-9) and `.view` (`screen` or `help` while the preview shows them) |
| `$STATE_DIR/lost/<skey>_<session_id>.json` | a thread lost with its session (see [Lost threads](#lost-threads)); pruned after 31 days, but one held |
| `$STATE_DIR/rex/` | weaver's numbers for Rex's blocks and windows, and what Rex does not keep of them (see [Rex](#rex)) |

## State file

Readers must ignore fields they don't know and tolerate missing ones.

| Field | Type | Meaning |
| --- | --- | --- |
| `pane` | int | the agent's pane: weaver's number for its Rex block |
| `agent` | string | `claude` |
| `state` | string | `working`, `needs-you`, `done`, `idle`, `interrupted`, `ended` |
| `since` | int | epoch s the state began; `ts` is the last event |
| `seen` | bool | you have looked since it changed; unread = `done` and not `seen` |
| `session_id`, `prompt_id`, `transcript` | string | from the agent's hook input |
| `pid`, `proc_start` | int, string | the agent process; `proc_start` is formatted like `TZ=UTC LC_ALL=C ps -o lstart=` (and `procStart` in `~/.claude/sessions/<pid>.json`) |
| `title` | string | the session title, when the agent reports one |
| `turn` | int | +1 on each prompt of yours (not on subagent reports or task notifications) |
| `prompt`, `waiting`, `last_message` | string | your last prompt; what it waits on; its last reply |
| `cwd`, `root`, `start_cwd`, `project_root` | string | `root` is the repo or worktree it is really working in; `start_cwd` the directory Claude Code snaps its cwd back to (where it started, moved by the worktree tools), the baseline a move is measured from; `project_root` its repository, `-` when it has none |
| `pending` | string | while it needs you for a tool: that call's key (name and input), so the tool's PostToolUse, or the PostToolBatch listing it, can be matched to it; `StopFailure` while an API error stopped it (no late hook of its turn ends that wait, only your next prompt) |
| `marker`, `marked_root` | string | the Worktrunk marker weaver set, and the worktree it set it on |
| `file` | string | the last file it edited with Edit or Write (or MultiEdit, NotebookEdit), or a subagent of it did; a Bash command's or an MCP tool's edits are not followed |
| `files` | [string] | absolute paths edited so this session, most recent last, at most 200 |
| `subagents` | [{id, type, since}] | subagents running now |
| `tasks` | [{id, subject, done}] | its task list (TaskCreated, TaskCompleted), at most 30 |
| `background`, `background_tasks` | int, string | background subagents it waits on at Stop |
| `doing`, `doing_id` | string | the command it runs now, from the async PreToolUse on Bash until that tool's PostToolUse (its `tool_use_id`), the turn's end or the agent stopping work; absent when none |
| `done_ids` | string | the `tool_use_id`s of the last 8 tools that finished this turn, space-separated: an async PreToolUse landing after its PostToolUse sets nothing |
| `server` | int | the pid of the Rex server the agent runs on; a server started again is another, however soon, so its threads count as lost |
| `rex_session` | string | the id of its Rex session; a session quit and made again is another. With `server`, the life of the session it runs in (`agent.Where`); a resumed state takes the life of the session its pane runs in as the pane starts (`wvr _agent`), before any hook |
| `end_reason` | string | after SessionEnd, its `reason` (`prompt_input_exit`, `clear`, `logout`, `other`, ...); absent before |

## Threads

`wvr new` makes the worktree (Worktrunk's `wt switch [--create] <branch>
--no-cd --format json`, whose `path` and `action` say where and whether it was
made), then
one window: its agent's pane, `wvr _agent <token> <agent>` in `<dir>`,
labelled `<label>`, then a split on its left running `wvr _panel <agent
pane>` (argv, no shell): its review panel, recorded as bound to it
(`.panel`) before `wvr new` returns, the agent on the right at
`$WEAVER_AGENT_WIDTH` (default `20%`, at least 44 columns while the panel
keeps 60) with the focus. Its window is named with its label from the
start.

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
HEAD of the checkout you are in (Worktrunk runs with `-C` that checkout), `^`
the default branch. A branch that exists only on the remote is checked out
tracking it (`wt switch`).

The agent's pane takes the thread from `$STATE_DIR/new/<skey>_<token>/`:
it moves `thread.json` to `<skey>_<its pane>.thread`, deletes `task` (read
first, which claims it), removes the directory, then execs the agent with the
task as its prompt (`claude "<task>"`). A thread restored
after its session went leaves `resume` (the session id) there instead of
`task`, and its agent's pane resumes: `claude --resume <session_id>`
(the agent afresh when unknown). A thread closed
before its panel is recorded as bound is closed whole all the same: `close`
finds that panel as the pane started for its agent. The pane is made to
stay when its program exits (`Spawn.Keep`; Rex says how it exited),
so an agent that
exits, cleanly or not (a broken install, a bad flag in `WEAVER_CLAUDE`), or
a `wvr _agent` that fails before it, leaves what it said on its pane and
its thread on the board, exited: `R` (`wvr restart`) starts its agent again in that pane on
its conversation (afresh when none is known), `x` closes the thread. A
thread lasts until you close it, so its session never stays with its
panel alone in it.

### Review panel

A thread's left pane is its review: `wvr _panel AGENT`, in the worktree,
records itself as the agent's (`.panel`: its pane and pid), then runs tuicr
(`$WEAVER_TUICR`, else `tuicr`) there, and again each time tuicr ends
(`q`, with weaver's `q_quits`, or `:q`), so it reads the branch afresh:
tuicr takes its range once, as it starts. While it runs the loop looks
every second whether that range was rewritten (`span.rewritten`): the
fork point moved (a rebase onto a newer base, the base merged in, the
thread's base changed), or HEAD is neither what tuicr was launched on nor
after it (an amend, a squash, a reset); then it ends tuicr and runs it
again on the range as it is now. New commits on top wait for `q`: an end
for them would drop a comment half typed. Each round it picks what to show
(`reviewArgs`):

- the branch's commits since its fork point, with what is not committed:
  `-r <fork>..HEAD -w`, the fork point as a SHA (`git.ForkPoint`: the merge
  base of HEAD and the review base, never the base's name, since tuicr
  diffs a range's two ends and with `main..HEAD` a file main gained after
  the fork showed as deleted). Rob's fork of tuicr opens this on the work
  tree while the branch has no commits of its own, and opens one whose
  commits change nothing in all (a commit and its revert) on its empty diff;
- with no review base, what is not committed (`-w`), else tuicr's own
  choice of commit;
- with no commits since the fork point and nothing uncommitted (files git
  does not track count), `no changes on this branch yet`, looked at again
  every 2 s: the one review the fork refuses ("No changes to review").

The review base is the thread's record's `base`, read again each round
(weaver's store is its one source); when that is none or names no commit
(a branch deleted), the default branch: the upstream remote's (its `HEAD`,
else `main` or `master`: a fork's base), else origin's, else a local `main`
or `master`. Its fork point is worked out each round too, so a rebase onto
a newer base, or the base merged in, moves it. The base is the
remote-tracking branch, which the panel never fetches: one fetched long
ago is as old as that. Every run has
`--theme micrographics --no-update-check` and `EDITOR=nvim`, so tuicr's `e`
(on a diff line) opens `nvim +N file` in the panel, with your own Neovim
config, and tuicr comes back on the same hunk when it quits. tuicr shows a
change as it is made (`diff_watch_interval_ms`, which weaver brings: see
[Setting itself up](#setting-itself-up)); a commit made while it is open
joins its commit list at the next round. The panel ignores `ctrl+c` while
tuicr runs (tuicr's own key), a tuicr that ends within a second waits out
the second (two when it failed), and the panel ends with its pane.

`wvr review [--full] [PANE]` (alt+v, alt+V, the board's `v`, `e` and
`ctrl+e`) goes to the agent's panel, or opens one on its left in the
worktree it is in (`agentWhere`) and puts the thread's widths back; in a
pane that is no agent's, a panel beside it in its directory. `--full` zooms
it. One press per agent at a time (`<skey>_<pane>.opening.lock`, `store.Lock`), under the
session's UI lock.

Review notes are tuicr's own: a comment is saved as it is written, readable
while tuicr is open, with `tuicr review list --repo .` (the checkout's
sessions, newest by `updated_at`) and `tuicr review comments --session
<slug>` (each comment as JSON with its `location`, `path:line`). A session's
slug includes its range, so after the panel's next round on a new commit
earlier comments sit in the previous session.

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
| `cmd` | string | the command its agent runs (`$WEAVER_CLAUDE`, else `claude`) |
| `num` | int | its number in the session (below) |
| `created` | int | epoch s |
| `base` | string | the branch it is reviewed against, a branch's name and never a SHA: a pull request's thread's what the pull request merges into (`origin/<base>`), another's the branch it started from as the remote has it (`origin/main`, `origin/release/2.0`; `@` recorded as the branch checked out); `""` for none, the default branch |
| `pane` | int | the agent's pane |
| `session_id` | string | its conversation: Claude Code's from the start (weaver chooses it), any agent's from its SessionStart on; absent while unknown |
| `item` | object | the work item it started from: `kind` (`pr`, `issue`, `linear`), `ref` (`owner/name#45`, `ENG-42`), `url`, `title`; absent without one |
| `mode` | string | `review`: read-only, whenever its agent starts (new, resumed, restarted): `agent.Command` takes from `cmd` what would loosen it and adds Claude Code's `--permission-mode plan`; absent otherwise |
| `title` | string | its name (the form's `name` row or `wvr new --name`; for one from an item, by default the item's, `#45 Add a flag`, read-only `review :: #45 Add a flag`; later `wvr rename`, the board's `r`), which rows, cards, window labels and lost rows show in place of its agent's `title`; absent for none |

A thread is a Rex session of its own, labelled with its `name` as it is
made and never again: Rex's labels are anyone's (Claude Code's agents
rename their sessions), so weaver finds a thread by ids alone. Its first
window holds its review panel and its agent, labelled by weaver
(`threadTabLabels`); its helpers' windows beside it are theirs.

A task's slug: its first line in lowercase ASCII letters and digits, every
other run of characters one `-`, at most 48 characters, cut at a `-`.

### Adopting

`wvr adopt [PANE | SESSION-ID]` makes a conversation weaver did not start a
thread. PANE is a number (pane numbers have no length: they only grow); one of 4+ digits is a pane only when weaver follows an agent
there (its state, thread or panel file). Anything else is a session id or
its first 4+ characters.
Alone, it adopts the agent in its own pane when it has one (so `! wvr
adopt` inside an agent), else opens the picker. Outside
weaver's session it acts on the weaver session (`$WEAVER_SESSION`, else
`weaver`), as `wvr resume`, `wvr rename` and `wvr quit` do
(`toWeaverSession`), and says so when that is not running.

- **From its pane** (an agent weaver follows, without a thread record):
  refused when its agent has ended, has not said its session id, is in a
  turn (working or needing you), or runs background commands or subagents,
  which its pane's end would end. Its pane closes, ending its agent (one
  process to a conversation); once its process has gone its state is
  forgotten and its conversation resumed as one by session id is, below.
  Typed into the agent (`! wvr adopt`), wvr runs in the pane that goes, so
  the rest runs in a process of its own (`outside`, as close's does).
- **By session id:** a lost thread's is resumed (`threadRestore`); an agent
  of this session running it is adopted from its pane; else its transcript,
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
  process is in it. Errors go to a message too, since the popup closes.

### Renaming

`wvr rename [PANE] [NAME...]` sets the thread record's `title` (PANE: as for
adopt; else the agent this pane belongs to, its own or its panel's), under
the numbering lock its other writers take, then relabels the windows and
reloads the boards. Without NAME it asks on the terminal (fzf, the query the
current name); an empty NAME clears it. The board's `r` on a thread's row
(alt+y's popover's too) edits in place: `wvr _mode rename PANE` prints the
fzf actions that show its input line, let go of its keys (`lsKeys`) and
stop it filtering, with the prompt `rename NN › ` (NN the thread's number,
else `%PANE`) and the current name as the query; on an agent that is no
thread it says to adopt it first. `enter` with such a prompt runs `wvr
_modedone "$FZF_PROMPT" "$FZF_QUERY"`, which renames the thread the prompt
names (a name unchanged is left as it is, so an agent's title is never
taken for yours) and prints the actions back to browsing; `esc` goes back
without it.

### Restarting

`wvr restart [--all] [--force] [PANE]` starts a thread's agent again in its
own pane, resuming its conversation, for an updated harness to take over
(PANE as for rename; `--all`, every thread record of the session; outside
weaver's session, the weaver session). A thread whose state is `working` or `needs-you`
is left unless `--force`, and one whose agent has not said its session id
cannot be. For each: its record (`pane` cleared), its state as a restored
thread's carries on (`writeResume`: idle, seen, without its process's
fields) and `resume` (its session id) go to `new/<skey>_<token>/`, then
`Respawn` starts `wvr _agent TOKEN` in DIR in the pane's place, ending
the agent, which takes them as a restored thread's pane
does and resumes (`claude --resume`). DIR is where a lost
thread would resume (`Lost.Dir`). The pane id, its window, panel and number
stay. It says what it did (restarted, left mid-turn, failed) on stdout and
in a message, since the board's `R` and the popover run it out of
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
to that thread instead. A pull request's thread's record keeps what it
merges into as its `base`, so reviews compare with that, and follows it:
each time the GITHUB view asks GitHub again (`followBases`), a pull
request retargeted there (a stacked one moved to main after its parent
merged) changes its thread's `base`, which the panel's next round takes.

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
`review`: its agent runs Claude Code in plan mode, each time it starts; a pull request's review panel shows the
branch against what the pull request merges into (its record's `base`).

### Commands for the dashboard

- `wvr new [--from PANE]`: with no task and a terminal, the form. Exit 0
  started (you are on the new tab unless `--no-focus`), 130 cancelled (nothing
  made), 1 error (one line on stderr). `--from` starts in that agent's
  repository.
- `wvr close [--yes] [--remove-worktree] [PANE]`: PANE is an agent or its
  review panel (default: the focused one). It says what goes and asks `close it?
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
there, a lost one, a pane of the session in it; not the thread being
closed's). It checks again after the agent has exited, and never forces:
Worktrunk (`wt remove --no-delete-branch --foreground`) refuses a dirty
worktree itself. Its ignored files, which removing it would take (an
`.env`, local settings; not whole ignored directories a build makes again,
nor `.DS_Store`), are copied first to `$STATE_DIR/trash/<name>-<when>/`,
kept a month, and what says it was removed says where. The branch always
stays.

## Lost threads

A thread lasts until you close it. When its agent dies because its session
went (its Rex server crashed or was killed, or you quit the session), its
thread is kept as lost rather than forgotten, so it can be resumed.

- **When:** where an agent's files would be forgotten for a dead pid
  (`AgentRows`, `GC`), `retire` keeps them as lost when the agent has a
  thread record (whether or not its conversation is known), and its session
  has gone since its last event (one whose `end_reason` is
  `prompt_input_exit`, `logout` or `clear`, its agent ended on purpose and
  its pane kept, only when the machine or its server went, since its
  session gone on the same server may be its window closed by hand; `wvr
  quit` first clears that reason from its threads' states, quitting being
  no close): the machine has started since its last event `ts`
  (`kern.boottime`), or its server's process has ended
  (neither asks Rex), or Rex says another server runs or the session is no
  longer on it (`rex.Answer.Lost`); when a call that takes fails, neither: its
  files stay as they are for a later look (one taken for lost while it runs
  would be resumed twice, one taken for not lost forgotten). `GC` likewise
  retires and loses nothing of a Rex session it cannot ask about, but what
  is from before the machine started. One pass (a `GC` run, the board's
  rows) judges every file by one answer from each multiplexer
  (`session.lives`): Rex's server and sessions
  asked once, each session's panes looked at once. Anything else is
  forgotten as before; a pane closed while its
  session runs on is not lost. The hook does
  the same check before a new agent's state replaces a dead one's in a reused
  pane id (a new server numbers panes from 0 again), and only ever keeps
  there, never forgets. When the check cannot tell (Rex not answering),
  neither the hook nor a resumed thread's agent (`AgentPane`, retiring
  another life's files under its pane) keeps anything, and their writes
  replace that pane's old files all the same; on Rex that is practically
  never another thread's, since its pane numbers are never given twice. Files with no pid to test are an earlier server's
  when the running session of their name was created after them: `GC`
  retires a state without a pid created before its last event `ts`, and
  keeps as lost a thread record whose agent never reported (no state)
  written before the session was made, as the pane's first event does
  (`LoseStaleThread`), so a new pane of that number never takes it for its
  own, and the thread is never dropped. A thread record learns its
  conversation at its agent's SessionStart (`session_id`: Claude Code's
  changes with /clear), so it can be resumed whether or
  not its agent's state survives.
- **Record:** `$STATE_DIR/lost/<skey>_<session_id>.json` (a thread whose
  conversation was never known: `<skey>_thread-<created>-<num>.json`), one object:
  `lost` (epoch s), `skey` (the session key it was lost from; records
  before 2026-10-15 call it `session`), `state`
  (the state file) and `thread` (the thread record). The pane's files go.
  Readers take its directory and key through `lostDir` and its `skey`, where
  an empty field counts as none.
- **Held:** a thread you put on hold (`wvr hold [PANE] [NOTE]`, the
  board's `h`) is a lost record of the same name with `held` (epoch s) and
  `note` (what is next, one line), written under its state file's lock
  before its agent and panel are closed as `wvr close --hold` closes them:
  unasked, its worktree never offered for removal. The board lists it under
  HELD, last, newest first, its note as its fact, its cell `[=]`, and its age
  a shade brighter after a week. Past three, HELD shows the three newest and
  one row (id `m`) for the rest, `… N more held`, whose `enter` (`wvr _fold`,
  the board's `<base>.heldopen`) shows them all and then folds them again;
  the board as text (`wvr ls | cat`) lists every one. A held thread is hidden while an agent with its
  session id still runs (being closed). Neither the session starting (its
  lost threads brought back) nor `wvr resume --all` takes a held thread;
  `wvr resume WHICH` and the board's `enter` do, as a lost one's, and `x`
  forgets it.
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
  life's, as when it restarts in its pane, stay, its panel still bound),
  then execs `claude --resume <id>` (no conversation known: the agent
  afresh, never `--continue`, which would guess another's); the panel binds as for a new thread. A
  conversation Claude Code runs elsewhere is refused, not resumed beside it. The state
  carries on: the first hook (SessionStart, source `resume`) keeps its turns,
  title and last reply.
  A record whose directory is gone stays lost.
- **The weaver session:** as it starts, its home (`wvr home`, behind the
  board as `wvr _boot`) runs `GC`, then resumes every thread lost by a
  session of its name, in number order, without leaving home
  (`WEAVER_RESTORE=0`: listed instead), then sweeps worktrees (below).
- **Sweeping:** `GC` (also on every SessionStart hook) removes sockets in
  the socket dir nothing accepts on (a board's or a view's that was
  killed), a day after they were made. What an agent left running in a
  pane that has gone (a server put in the background) is not swept: Rex
  does not say which processes were a block's.
- **Worktrees:** `$STATE_DIR/worktrees` lists the worktrees weaver made (`<dir> TAB <repo> TAB <unix time>`; threads' from
  before the list join it as they are seen); `SweepWorktrees` (at `_boot`,
  and `wvr gc`) removes one an hour or more old that nothing uses
  (`worktreeUsers`, as close asks), when `worktreeRemovable` holds
  (clean, its HEAD merged into a default branch or pushed), its ignored
  files kept first as close keeps them, with `wt remove
  --no-delete-branch`, and says so in a message.
  Close's `keep` takes a worktree off the list.
- **Dashboard:** lost threads (of every session) last, under `LOST`, glyph
  `[-]`, without a number or a 1-9 key and not counted in the title; their item
  id is `l<session id>` and field 2 the session id. Enter resumes one
  (`wvr resume`), x forgets it (`wvr resume --forget`), and the preview
  shows its card (`wvr _preview <session id>`).
- **Quit:** `ctrl+q` kills the session, and the threads come back as above.
  `wvr quit [--yes]` kills the session you are in (outside it,
  `$WEAVER_SESSION`) after a card (ctrl+q's, in a popup): the threads kept,
  the turns under way, and the reviews where nvim is open (`editing`: an
  nvim among the panel's processes, which tuicr's `e` started, whose
  unsaved edits would go; no nvim weaver could ask has a socket), as `wvr
  close` says of its thread. The footer shows `^q QUIT`.

## Dashboard

The dashboard is fzf: `wvr home` in the weaver session's home window, at
full size, and `wvr ls` in the pane it runs in. `PushStatus`, run by every hook that changes a state,
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
(`cardHeld`; Rex does not say whether the list is on screen, so it is taken
to be). The hold ends as the cursor moves (the focus event clears the mark
and refreshes), or as the card is shown from its top again (`ctrl+r`, `s`,
`?`).

fzf draws one sharp border in faint grey: `┌─ [ WEAVER :: THREADS ] 5 ──── [!] 2  [x] 1  [/] 1 ─┐`, the
title and the counts in its label. Inside, from the top: a blank
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
hooks' state refined by Claude Code's own session file). The border's
counts, ctrl+q's question (needs you or working: mid-turn), alt+1,
the card, close's card, quit's card and `wvr restart`'s mid-turn guard all
take it from there, so none says otherwise. So the headings' counts are the
border's, and the order is the one alt+1
visits, but for read-only threads: REVIEWS holds them whatever their state
(the title's counts, by state, still include them). A row: its number (1-9), glyph, name (`Row.Called`, as everything that
names it: the name you gave the thread, else Claude Code's title, else the
thread's task or item, else the last prompt; at most 40 columns), one fact (what it
waits on; for a finished turn, its last reply;
the command it runs (`running npm test`), background tasks, subagents, task
progress or the file being edited; the
reason it is not reporting), repo/branch (from 96 columns; `×N` when N agents
share the worktree) and its age in that state. Columns are no wider than
their longest entry, so repo/branch and age stay close at any width.

Rows cost no git command: repo and branch come from the checkout's `.git`
file, `commondir` and `HEAD`.

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
from its pane into a thread's session, said in a message; anything else, the empty board
included, opens the picker of conversations in the form's popup), `e`
(`ctrl-e`) jump and open its review, `s` card or screen, `tab` / `shift-tab` the
next / previous view (`wvr go next|prev`, round home and the views as a
tab bar; a view's the same), `?` keys,
`ctrl-r` reload, `q` and `esc` close. `/` shows the input and unbinds the
letters; `esc` clears the query, hides it and binds them again. The footer is
recomputed (`wvr _footer`) on every load and mode change, from the width
and the mode, `?` always last. Browsing, it holds the few keys most used
(`⏎ OPEN`, `n`, `x`, `h`, `/`, `v`, `tab`, `ctrl+q`), the rest in `?`'s help;
`⏎ OPEN` is a thread's window, or a lost or held one resumed. While a hold
is on its way (`<board>.holding`, the thread's number, until its agent has
closed) it starts with `… HOLDING NN`; holding a thread mid-turn adds `!
MID-TURN: HOLDING STOPS IT`.

A heading or gap never keeps the cursor: on `focus`, a transform sends it on
to `next` (to `prev` when the move was upward, or when there is no `next`).
That covers every way of moving (keys, page, first/last, mouse, a reload
whose agent went away) in one place, for one `/bin/dash` spawn per move
(about 1 ms).

Outside home, `n` and `e` end fzf with a key (`print`), and `Ls` acts: `e`
jumps and runs `wvr review`; `n` runs `wvr new --from <pane>` on the
pane's terminal and reopens the dashboard when it is cancelled (130) or
fails. Home keeps running: `e` and enter act in the background
(`execute-silent`), as does enter on a lost thread (`wvr resume`, its
failure said in a message), and `n` and `x` open the dialogs
everything else opens (`wvr _popup new --from <pane>`, `_popup close
<pane>`, `_popup forget <thread>`: the form 96 x 20, a card 80 x 12, over
the board), whose status push reloads it.

### Transitions

Changing view keeps you oriented (where you are, how you got there, how
to get back) and changes one thing at a time:

- **One dialog, one place:** the form and the cards are popups over where
  you are, the same size whichever key or click asks (`popup.go`);
  never the board replaced by a full-screen one, nor fzf's `execute`,
  which shows the pane's own screen between.
- **Back where you were:** going home from a thread puts
  the boards' cursor on its row (`_pos`, posted to each board), and alt+y's
  popover over a thread opens with its cursor there;
  closing the thread you are in lands home, not on whichever window Rex
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
  action (`hintAct`): `trigger(<its fzf key>)`, `^q` as `wvr _popup quit`
  (ctrl+q's card), nothing for `1-9` and `↑↓`.
### Resume card

`wvr _preview PANE LABEL`: the label in bold, then the state and its age,
then aligned fields (asked, waits, background, said, tasks, subagents, files,
git), each wrapped under itself, at most a few lines. `git` is the worktree's
`git status` in one line: branch, ahead/behind, changed and new files. A
thread whose agent has not reported shows its record (task, worktree).
`s`/`tab` swap in the agent's screen (`Mux.Screen`) under
`screen · name · repo/branch · turn`, as many lines as fit; `?` the keys.

## The weaver session

weaver runs on Rex alone. `wvr` outside weaver's session (`Start`) makes
its Rex session, `$WEAVER_SESSION` else `weaver`, with home in its first
window (see Start, under [Rex](#rex)), or shows the one running. Each
thread is a Rex session of its own beside it, made by weaver as a thread of
that one (home's).

- **Identity:** weaver acts on the Rex session it runs in (`rex.Chosen`)
  when weaver made it (`$STATE_DIR/rex/sessions/<id>`), home's or a
  thread's; in another Rex session's terminal, or outside Rex, it
  is in none, and commands that act on a session act on weaver's.
  `Session` is the name weaver gave home, a thread's session's too (one it
  did not make is named by its id), `RexID` home's Rex id and `RexHere`
  the one weaver runs in, `SKey` is `rex-<session>` (a `/` as `_`), and `MyPane` the
  number of the block weaver runs in (`$REX_BLOCK`; empty in a key's
  command, which runs in no block).
- **Numbers:** a thread's number is its place: `num` in its thread record,
  the lowest free among the session's thread records (those being opened
  included, for at most 30 s), under `$STATE_DIR/<skey>.num.lock`. Rex's
  sidebar keeps its sessions in the order they were made. Wherever a
  row is numbered (the home board, a dashboard) the number is the thread's
  `num` when any agent has one, else the row's place, and the digits go by it.
- **Home:** `wvr home`, in the first window, is the dashboard at full size
  that never ends: enter, digits and `e` go to a thread and leave it running,
  `n` runs `wvr new` inside it (also on an empty board), `q` and `esc` go
  back to the window you came from, as alt+y does. It outlives installs of
  weaver (`installs.go`), so it writes `<skey>.fzf.<pid>.born` as it starts:
  its time is when, and it holds the real path of the binary it runs. It
  starts again in the weaver `wvr` resolves to on `$PATH` (else its own), as
  Rex's keys and the hooks run it, once that is another file than the one in
  `.born` (an upgrade; Homebrew's and `install.sh`'s `~/.local/bin/wvr`
  swapped, either way) or one changed since: `_chrome`, on the first reload
  after, aborts the board, which then execs that weaver's board (`LS_HOME=1
  wvr ls`) in the same pane and process. The board's own process asks too,
  every 5 s, and aborts its fzf through its socket: its keys cannot once the
  binary they run by has gone. Its old keys never meet rows they do not
  know.
- **alt+y:** `wvr go home` (run in the pane's directory). From a thread, a
  popover over it (`peek`): a floating layer over most of the window,
  running `wvr _peek FILE AGENT`, whose pages are the board (`ls` with its
  socket named `.fzf.p<pid>`, so hooks reload it, and its cursor on AGENT's
  row) and each view that is set up (`itemsView`), `tab` and `shift-tab`
  going round them. One popup at a time, so a key there that would open a
  dialog or go elsewhere ends the popover with its choice (`peekChoice`,
  `peekItemChoice`) written to FILE, one wvr command a line, its words
  tab-separated; `go home`, which waits for the popover to close, then runs
  them: a jump (`_goto`), `review`, `resume`, `_popup new|close|
  forget|adopt`, `_items NAME from REF [--review]`, or `next` (alt+1).
  `esc`, `q` and alt+y close it with nothing to run. Without an app attached
  to show it in, alt+y goes home as from anywhere else. From home, on to
  each view in turn, and from the last back to the thread's window home was
  left from (`$STATE_DIR/<skey>.last`, or home when it has gone); from
  anywhere else home, made again if it was closed. Home is the window
  weaver placed at 0 that shows no view, by its registry, whatever its
  label.
- **Views:** see [Views](#views).
- **Threads:** a session labelled with the thread's name, its first
  window labelled LABEL in DIR running `wvr _agent TOK`, then a split on its left running `wvr _panel AGENT` (argv, no
  shell), titled `review: <dir>`, then the agent focused: whole before it
  is shown, as Rex needs. alt+v's panel, opened again, is a split on the
  agent's left.
- **Seen:** a done agent is seen when weaver takes you to it (the board's
  enter, `_goto`; alt+1; alt+v) or when it is prompted again. Rex does not
  say which session an app shows, so reaching it any other way (the
  sidebar, alt+j/k) leaves it new.
- **alt+1** says where it landed in a message (`Say`): `[2/3] 02 name ·
  needs you · what it waits on`; a helper's, `02 › <its window's label>`.
  It goes to every agent that needs you, helpers included, and to every
  agent done that you have not looked at but a helper (its thread's to
  look at).
- **A thread's window closed by hand:** its agent gone while its session
  runs on (its helpers' windows) is the thread closed, as `x` closes it:
  as the agent is retired its session ends, helpers and all, and the
  thread is forgotten, not kept as lost.
- **Helpers:** a helper is an agent with no thread whose pane is in a
  thread's session, as the session's snapshot lists it (`Row.Session`, from
  the pane, never a copy a hook wrote). Where rows are made (`AgentRows`,
  `rollUp`) each helper gets its thread (`Row.Thread`) and each thread's
  agent the helper that has needed you longest (`Row.Via`); `Row.Shown` is
  the thread as every list of threads shows it, needing you when that
  helper does, saying which helper waits on what, though its own agent
  works, is done or has exited. The boards, the views' thread rows and the
  counts (`wvr status`) read `Shown` and leave helpers out.
- **Boards:** home's and alt+y's list threads as they show (`onBoard`).
  A jump to a thread (enter, a number, `_goto`) whose wait is its helper's
  goes to the helper's window (`Row.Waiter`).
  alt+Y (`wvr go
  local`) in a thread's session opens the same board over it scoped to
  that session (`_peek FILE PANE SESSION`, `LS_SESSION` for its reloads),
  no views: its agent and helpers, a helper by its window's label; from
  anywhere else, alt+y's.
- **Notes to the agent:** tuicr's comments, which the agent reads (see
  [Review panel](#review-panel)); weaver types nothing into an agent.
- **Resume:** a lost thread whose directory has gone resumes in its
  repository's main checkout (`.thread.repo`), from which Claude Code finds
  the session in any of its worktrees (and since 2.1.223, in any project).
- **Resizing:** Rex's splits are ratios, so the app's window resizing keeps
  them, and below 104 columns the panel keeps 60 and the agent gets the rest. But
  the app evens out a window it shows when its panes change (a split made,
  a block swapped in), so after such a change weaver puts the thread's
  widths back twice, late (`settleWidths`: `wvr _width`, 1 and 2.5 s on),
  unless the split has been moved since by you (`rex.Session.Moved`:
  neither even nor at the ratio weaver last gave it, kept in the registry's
  `split/`). A window made behind you is not drawn yet (Rex's 80 by 24
  terminals): its widths come from the width the app draws the session's
  windows at (`drawnCols`).
- **Keys:** `config/rex.lua` (see Start, under [Rex](#rex)). alt+N opens
  the form in a floating layer, which draws its own border (fzf's), and
  what follows it there, the thread being made, is drawn in a frame like it
  (`framed`): stderr goes through a pipe for the while, so Worktrunk's and
  git's lines land inside too, cut to its width, and a failure's buttons sit
  on a row of it (`frame.choose`).

## Rex

Weaver acts on its session through `rex.Fleet` (`internal/rex`), which
`Env.Mux` returns: home's `rex.Session` with its threads' sessions, in the
words of `internal/mux` (its panes, windows and programs). Rex finds a block or window only in its own session
(a call naming another's answers `block not found`), so a pane's or
window's call goes to the session its number is in; `Panes` and `Windows`
are every session's, asked at once; a popup or message goes where wvr
runs. A method that steers what you see (focus, names, widths) is
best-effort and says nothing of failing.

- **Ids:** `mux.PaneID` and `mux.WindowID` are what weaver's files keep,
  numbers (`mux.ParsePane`, `mux.ParseWindow`). Rex's blocks and windows
  are UUIDs, so `rex` numbers them: `$STATE_DIR/rex/n/<num>` is the block or
  window a number names, `u/<id>` (`:` as `_`) the number of an id, given
  under `rex/lock`, never twice (`rex/start` is held while `wvr` asks
  whether weaver's session runs and makes it, so two at once make one);
  `Respawn` points the pane's number at the block that takes its place,
  holding `rex/lock` from before that block is made until its number names
  it, so the program it starts, asking its number however soon, gets the
  pane's. The old block, until it has closed, is retired (`u/<id>` holds
  `-`): no number, neither the pane's nor one of its own, so its agent's
  last hooks, which would reset the new agent's state under the pane's
  number, reach no pane. One that fails once the block is made closes it
  and leaves the old pane as it was, its number and all. Beside them:
  `in/<num>` the session a block or window is in (none for one numbered
  before weaver kept it: home's), `cmd/<num>` the argv weaver started a
  block with (Rex does not say; `Pane.Cmd`), `want/<num>` the place a
  window of home's was given (home 0, a view 900 and on: Rex orders
  windows, it does not index them, so a window is moved before the first
  placed further on), `view/<num>` its view, and `sessions/<id>` the
  name weaver gave a session it made, when (epoch seconds to the
  millisecond), the pid of the server it made it on and, for a thread's,
  home's id (a thread's session has home's name, so its agents' files are
  keyed by home). `GC` forgets the record of a session Rex no longer
  runs (a husk ended, an old home, a thread's closed; `rex.Prune`), but
  not in the minute after it was made, which Rex may not list yet. Rex saves its sessions as its server stops and brings them
  back when one starts again at that endpoint, ids and all, with a fresh
  shell in each terminal; one whose server is not the one weaver made it on
  is such a husk (`Restored`): `rex.Ensure` makes it again and gives it back
  for `wvr` to end once the new one shows, with the threads' sessions Rex
brought back with it, and `wvr` in one starts again. A home made while
threads' sessions of its name run on, their home closed alone, takes them
over (`takeOver`): its id goes into their records.
- **Keys:** files are keyed `rex-<name>`; a Rex session weaver did not make
  is named by its id. Files of another key (tmux's and zellij's, from before
  weaver ran on Rex alone) name a session that no longer runs, and go as
  their agents do.
- **Where:** `agent.Where` is the Rex server's pid and the session's id
  (stored as `server` and `rex_session`). `serverPID`
  reads the pid from Rex's runtime file for `$REX_SERVER`, `servers/<SHA-256
  of the endpoint, hex>/runtime.json` in `$REX_RUNTIME_DIR`, else
  `~/Library/Application Support/rex` (its endpoint and pid checked), so
  hooks make no call; without one it asks the server (`server.status`).
  `rex.Answer` asks the server's pid, then its sessions, each once and only
  when needed; its `Lost` says whether a life has gone and whether it could
  tell: gone when its server's process has ended (no call), when another
  server runs, or when the session is no longer on it; not known when a call
  that takes fails, when weaver takes nothing for lost (see [Lost
  threads](#lost-threads)).
- **Calls:** over the server's control connection (`wire.go`): the
  WebSocket at `/api/control` on its Unix socket, `rex.control.v1`, which
  the server documents at `GET /llms.txt`. One connection per wvr, opened
  at its first call (hello at revision 12, oldest 8) and closed, with a
  close message, before it exits or execs (a client gone without one is
  kept a minute); one idle 20 s is opened again. A call naming a session
  attaches the connection to it first; blocks a call makes are given up
  (`session.disown_block`), since a terminal takes its size from its owner
  and weaver asks none. Calls that do not depend on each other go at once
  (`wireBatch`): a look at the fleet is the sessions' list, their views,
  then every terminal's size (with `Panes` its directory and processes
  too). A Rex error reads as the rex command's (`method: rex: message
  (status=N code=C)`). Where there is no Unix socket to the server (a
  host's address, none), its handshake is refused, or
  `WEAVER_REX_WIRE=0`, the rex command as before: each look one `rex do`
  (its view and terminals; the fleet's, `fleetLua`, against home), every
  other method one `rex api call`. The app's actions go over the
  connection as `rex do` asks them (`client.perform` of the app's
  `session.select`, `origin` cli, the arguments as `rex do`'s key=value
  pairs give them: verified against a client of kind app, which `rex do`
  asked the same); a popup's `rex wait` is always the rex command's. A pane's environment goes through
  `env(1)`, Rex setting none: `WEAVER_WVR`, the
  weaver that started it, which home follows ahead of PATH (Rex runs a
  command after the login shell's startup). `Spawn.Keep` makes a pane stay
  when its program exits: Rex is told as it is made. A popup is a floating
  layer, its command `rex wait` on it, made only when an app is attached to
  the session: otherwise `Popup` says false, makes nothing, and gives a
  command whose `Start` fails with why. The app keeps its own tab for each
  session: the server's active window (`session.focus_window`) is not what
  it shows, and it sets it back to its tab. So `Show`, `Focus`, and
  `OpenWindow` and `OpenSession` in the foreground ask the app (`rex do
  session.select` with the window: Remote Control on; with more than one
  app connected, which `rex do` cannot choose among, each app attached to
  the session by name), else make it the server's active window. `Focus`
  asks whether or not the window is its session's active one (a thread's
  only window always is): the app may show another session, and Rex says
  not which. What goes on behind you never asks: a session opened in the
  background, threads restored as weaver starts, `--no-focus`, and
  `Respawn`, which keeps its window's focus on the server alone. A window's width
  is its zoomed block's while one is zoomed (`zoomed_block_id`), whatever
  that block's share of the layout. `SplitLeft` gives no pid, none being
  the program's yet (Rex runs a command through the login shell): `wvr
  _panel` records its own pid as it starts.
- **Messages:** Rex has no message line: `Say` and `SayToSession` show
  their text as one line in a small floating layer at the foot of the
  active window that takes no focus (`focus: false`) and closes on its own
  after its time (what the session shows is its active window, so both are
  the same). It is sized by the text's columns (wide characters two), and
  text wider than the window is cut keeping its start, ending in `…`. Its
  block is labelled with the text: Rex heads a floating layer with its
  label, over its one row. A new message replaces the one before: each
  `Say` closes the block kept in `$STATE_DIR/rex/said/<session>` and keeps
  its own there, under a lock of its own (`$STATE_DIR/rex/say`), so two at
  once never both show. Rex has no status line or window marks, and does
  not say which session an app shows.
- **Start:** `wvr` makes the Rex session (home its first window, `want` 0)
  unless one weaver made of that name runs, asks the app to show it
  (`session.select`, with Remote Control on), and writes `config/rex.lua`,
  run by its own path, to `weaver.lua` beside the init.lua Rex reads
  (`$REX_CONFIG`, else `~/.config/rex/init.lua`) for it to `require`; when
  weaver.lua changed, or the server's keymap (`client.keymap`) has none of
  its bindings, it has Rex read its configuration again (`rex config
  reload`), the server having read init.lua before weaver.lua was there.
  weaver.lua's keys are weaver's only in the sessions it made, and run wvr
  with `REX_BIN_DIR` where the wvr that wrote it found rex, and the settings
  a key's wvr reads that it had set (`rexSettings`: `XDG_STATE_HOME`,
  `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `WEAVER_AGENT_WIDTH`,
  `WEAVER_AGENT_MIN_COLS`, `WEAVER_VIEWS`): Rex's server runs them with an
  app's environment, where `rex.Bin` would find no rex but the app's
  server's own, and weaver its state in the wrong place. They run in the
  pane's directory (its program's when it reports none), from home when
  that has gone; what wvr says on stderr goes to Rex's log, and a key whose
  wvr cannot run at all is the terminal's, not swallowed. In a floating
  layer (weaver's popups: the form, a card, alt+y's board) the keys are the
  popup's program's. Rex starts a key's wvr at once, not after the last
  one's: `wvr review` holds the session's UI lock (`uiLock`) and `wvr next`
  tries it, so a press while the last is at work does not undo it. alt+j
  and alt+k are the app's own `session.next` and `session.previous`
  (`rex.invoke`), in weaver's sessions only.

## Setting itself up

`wvr` outside weaver's session (`Start`), when `$WEAVER_SELF_SETUP` is not
`0` and it is the `wvr` on PATH (`os.SameFile` with `exec.LookPath`; a build
run from a checkout never takes the hooks), runs `SelfSetup` before it
starts or shows the session: for Claude Code (`$CLAUDE_CONFIG_DIR`, else
`~/.claude`), when that directory exists, the hooks are merged as `wvr setup`
merges them (`mergeHooks`, a backup first when the file was there), and one
line on stderr says when a file changed. The files weaver brings to other
programs (`setup.BroughtFiles`: tuicr's `config.toml`, and its theme's
`themes/micrographics.toml` and `themes/Micrographics.tmTheme`, in
`${XDG_CONFIG_HOME:-~/.config}/tuicr`; and the weaver skill,
`config/skills/weaver/SKILL.md`, as `skills/weaver/SKILL.md` in Claude
Code's config directory when it is there) are written where there is none, or
where the one there carries weaver's mark (`weaver: this file is weaver's`)
and differs, each said on stderr; one without the mark is the user's, left
alone (`Brought.Fate`). Nothing is set for tuicr to find them
(`XDG_CONFIG_HOME`, `VIMINIT`): the nvim its `e` opens would inherit it.
`wvr setup` says each file's fate and writes it; `wvr doctor` checks them
(`theme and skill`: a tuicr config of the user's is named short of
`q_quits` or `diff_watch_interval_ms` when it lacks them, which the panel
leans on) and tuicr itself (needed, 0.28 or newer). The skill is
weaver's workflow as policy, not machinery: a thread's session on one
worktree, helpers as agents in windows of it (`rex window new`, the window
id kept) that report back with Claude Code's cross-session `SendMessage`,
then wait for the agent that started them to close their window once
nothing they owned is uncommitted, small commits of only the paths an agent
changed, done reported as `git log --oneline <base>..HEAD`, and review
comments read with tuicr's CLI. It names the rex skill for Rex's commands
rather than repeat them; a skill loads when its description matches. Unpacking its
files also removes what weaver kept beside them until it dropped its Neovim
plugin: the plugin's copy (`$XDG_DATA_HOME/weaver/nvim`, which Neovim
configs loaded) and the plugins it fetched for it (`weaver/plugins`).

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
`failed, as of 1h ago`, and one with nothing from before shows too.

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
fzf's preview passes on: Cmd and a click opens one), as
is the card's title, to the item; images `[ IMG: alt ]`;
code in crop corners with its language at the top right; a GitHub alert
(`> [!WARNING]`) under an ink stripe, its kind a `[ LABEL ]` (red for one
to heed); task boxes as cells (`[x]`, `[ ]`); a quote on a rail in grey
italics; tables with a faint capitals header over a rule, columns apart
by space; rules the card's width. Not glamour, whose highlighter
registers every lexer as it loads, which costs every hook 6 ms.

- **alt+N / alt+m** in a view: a thread from the item under the cursor, or a
  read-only review of it. weaver's keys leave alt+N to a view, whose fzf (also
  enter) runs `wvr _items NAME from REF [--review]`, which runs `wvr _from
  github pr|issue <owner/name> <number>` or `wvr _from linear <ID>`. `_from`
  opens `wvr new --pr/--issue … [--review]` in a popup (96 x 20) from a
  detached process, so the view goes on; a Linear issue's thread starts in
  the repository used last (`$STATE_DIR/repos`).
- **alt+j / alt+k** are Rex's next and previous session, views and all.

