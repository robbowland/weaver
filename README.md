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

- follows the agent's edits, including those made by subagents and by Bash,
  giving each worktree its own tab;
- reloads files the moment they change on disk;
- never takes over a diff you are reviewing.

| Command | Does |
| --- | --- |
| `:AgentFollow` | Toggle following the agent |
| `:AgentChanges` | Quickfix list of the files changed on this branch |
| `:AgentReview` | Review the branch in Diffview |

The branch is compared against `$WEAVER_BASE`, else
`git config branch.<branch>.weaverbase`, else `main` or `master`.

## Install

Needs zellij 0.45+, neovim, jq and git; fzf for the interactive dashboard.

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
sessions are not touched.
