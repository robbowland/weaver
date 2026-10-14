# weaver

**Pick up whichever thread needs you.**

Run several coding agents side by side in zellij and weaver keeps track of them:
which are working, which are done, and which are waiting on you. One key shows
them all, another jumps to whoever has waited longest, and a neovim bound to
each agent follows it into the worktree and file it is really editing.

For now weaver drives Claude Code through its hooks; other agents are planned.

## Keys

| Key | Does |
| --- | --- |
| `Alt y` | Dashboard of every agent in the session. `enter` jumps, `ctrl-e` jumps and opens nvim, `ctrl-r` reloads |
| `Alt 1` | Jump to the agent that has needed you longest |
| `Alt v` | Open, or jump back to, the nvim beside the focused agent |
| `Alt V` | The same, fullscreen |

An agent that starts needing you (a permission prompt or a question) rings the
terminal bell, so zellij marks its tab.

## Neovim

The nvim weaver opens for an agent loads your own config, then:

- **follows the agent politely.** An edit by the agent (or its subagents, or
  its Bash commands) is followed only once you have been idle for about 3
  seconds in normal mode, in a file window, and not reviewing a diff. Until
  then it waits in the bar as `→ file`, and `<leader>aj` goes there at once.
  It never types into nvim, never replaces a buffer with unsaved changes, a
  terminal, quickfix or git window, and brings other tabs up to date without
  switching to them;
- gives each worktree its own tab (closed again when the worktree is removed);
- reloads files the moment they change on disk, never over unsaved changes;
- shows the agent at the top of each file window: its state, turn, the edit
  waiting to be followed, and what it was asked or is waiting on.

### Reviewing

Reviews open in one [diffview-plus](https://github.com/dlyongemallo/diffview-plus.nvim)
view. A scope's key opens it, pressing it again from elsewhere brings the view
back (refreshed), pressing it inside the view closes it, and another scope
replaces it. The view refreshes itself as the agent edits.

| Key | Command | Shows |
| --- | --- | --- |
| `<leader>at` | `:AgentReview turn` | The agent's last turn (the turn so far while it works) |
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
| `<leader>af` | `:AgentFollow` | Toggle following |

In a review, `gf` opens the file at the same line in the tab of its own
worktree. Untracked files the agent created are listed like any other (your
index is never touched to do it). In diffview-plus's file panel, `w` marks a
file reviewed and `H` hides the reviewed ones.

The branch is compared against `$WEAVER_BASE`, else
`git config branch.<branch>.weaverbase`, else the default branch of the
`upstream` or `origin` remote, else `main` or `master`.

Your own config can use the same one-view review:
`require('weaver.diffview').show({ key = ..., label = ..., root = ..., compare = { kind = 'branch', base = 'origin/main' } })`
(see `nvim/lua/weaver/diffview.lua`). `require('weaver.status').text()` is the
agent's state for a statusline, and `User WeaverEvent` autocmds receive every
event from the agent.

## Install

Needs zellij 0.45+, neovim 0.10+, jq and git; fzf for the interactive
dashboard, and diffview-plus.nvim in your nvim for reviews.

```sh
./install.sh --configure
```

This copies weaver to `~/.local/share/weaver`, links `~/.local/bin/weaver`, adds
the keybindings to your zellij config and the hooks to
`~/.claude/settings.json`, backing up each file it changes. Pass
`--zellij-config FILE` (repeatable) for other zellij configs and `--dry-run` to
see the diff first. It is safe to rerun, and it replaces an install from when
weaver was called agentnav.

Restart running Claude Code sessions afterwards: they read hooks at start.

## Settings

| Variable | Default | Does |
| --- | --- | --- |
| `WEAVER_BELL` | `1` | `0` stops the bell when an agent starts needing you |
| `WEAVER_WT_MARKERS` | `1` | `0` stops setting [Worktrunk](https://github.com/max-sixty/worktrunk) branch markers (🤖 working, 💬 needs you) |
| `WEAVER_DEBUG` | unset | Write hook errors to `~/.local/state/weaver/hook.log` |

## Tests

```sh
test/run.sh [suite...]
```

End-to-end, in a throwaway zellij session with its own state; your real
sessions are not touched. `test/nvim/run.sh` runs only the Neovim specs
(headless, a few seconds); set `WEAVER_TEST_PLUGINS` to a directory holding
`diffview-plus.nvim` if lazy.nvim's plugin directory doesn't have it.
