# weaver

**Run several coding agents side by side in tmux, and always know which one
needs you.**

weaver gives each task its own tmux window, called a *thread*: the agent
(Claude Code or Codex) on the right and a Neovim that follows its edits on the
left, in a git worktree of its own. One board shows every thread and what it
is doing. One key jumps to the agent that has waited on you longest. And your
threads come back after tmux crashes or the machine restarts.

```
┌─ [ WEAVER :: THREADS ] 5 ─────────────────── [!] 2  [x] 1  [/] 1 ─┐
│                                                                      │
│   NEEDS YOU · 2                                                      │
│   01 [!]  Add a CLI flag          Bash: python app.py --name … 11m  │
│   02 [!]  Tidy the fish config    question: Which config file…  8m  │
│                                                                      │
│   DONE · 1                                                           │
│   03 [x]  Add a README            Wrote README.md and CONTRIB…  8m  │
│                                                                      │
│   WORKING · 1                                                        │
│   04 [/]  Group the dashboard r…  2 subagents                   9m  │
│                                                                      │
│   IDLE · 1                                                           │
│   05 [x]  Audit yabai float rules Removed two dead rules; th…  47m  │
│   ⏎ JUMP  1-9  n NEW  x CLOSE  / SEARCH  s SCREEN  ? HELP            │
│ ─────────────────────────────────────────────────────────────────────│
│ Add a CLI flag · try/add-a-cli-flag · turn 3                         │
│ needs you · 11m                                                      │
│                                                                      │
│ asked      add a CLI flag to app.py, then run it to check it works   │
│ waits      Bash: python app.py --name rob                            │
│ said       Added --name and --shout; running it to check.            │
│ tasks      [x] parse the flag  [x] wire it into main  [ ] run it     │
└──────────────────────────────────────────────────────────────────────┘
```

- **See every agent at once.** The board groups threads by what they need
  from you, with what each is waiting on or doing.
- **Go straight to the one that's waiting.** `alt+1` jumps to the agent that
  has needed you longest; press it again for the next.
- **Review as it works.** The Neovim beside each agent follows its edits,
  shows each turn's diff, and sends your line notes back to the agent.
- **Start from a pull request or an issue.** Views of your GitHub pull
  requests and issues, and your Linear issues, start a thread on the right
  branch with the item as context.
- **Keep your threads.** A crash, a quit or a reboot doesn't lose them: `wvr`
  brings them back, each agent resuming its conversation.

## Requirements

- macOS, with [Homebrew](https://brew.sh).
- [Claude Code](https://github.com/anthropics/claude-code) or
  [Codex](https://github.com/openai/codex), installed and signed in.
- A terminal that sends Option as Alt, since weaver's keys are Alt keys. In
  Ghostty, set `macos-option-as-alt = true`; in iTerm2, set the Option key to
  `Esc+`.

Homebrew installs everything else weaver uses: tmux, Neovim, fzf,
[Worktrunk](https://worktrunk.dev) for worktrees, and gh.

## Install

```sh
brew install robbowland/kegs/weaver
```

The command is `wvr`. `brew upgrade` keeps it current.

## Quick start

1. **Start weaver.**

   ```sh
   wvr
   ```

   This opens weaver's tmux session on *home*, the board of your threads.
   The first time, `wvr` adds its hooks to Claude Code (and to Codex, if you
   use it). Restart any Claude Code session that was already running so it
   picks them up.

2. **Start a thread.** Press `alt+N`, type what the agent should do, and press
   `enter`. weaver creates a worktree on a new branch named after the task,
   opens a window with Neovim on the left and the agent on the right, and
   gives the agent your task as its first prompt.

3. **Check on your threads.** Press `alt+y`: the board opens over the thread
   you are in. Each thread is a row showing its state, what it is doing or
   waiting on, and for how long. Press `enter` on a row, or its number, to
   jump to it, or `esc` to close the board.

4. **Answer whoever is waiting.** When an agent needs you (a permission
   prompt, a question), the board moves it to the top and `alt+1` takes you
   to it.

5. **Close it when you're done.** On the board, `x` closes a thread: its
   agent, its Neovim and its window. If weaver created the worktree and
   nothing in it would be lost, weaver offers to remove it. The branch always
   stays.

Closing the terminal only detaches you: the session keeps running, and `wvr`
reattaches. `ctrl+q` quits the session and every agent in it; your threads
come back the next time you run `wvr`.

## How it works

**Threads.** A thread is one task for one agent, in a window of its own:
Neovim on the left, the agent on the right, in a new worktree or a checkout
you choose. Each thread has a number, shown in its window's name (`01 Add a
CLI flag · repo/add-a-cli-flag`), and that number means the same thread on the
board, in `Alt` keys and on the status line.

**Home.** Window 0 is home: the board of every thread. A card beside the
board shows the selected thread's last prompt, what it is waiting on, what it
last said and its task list.

**Views.** Beside home are GITHUB (your pull requests and issues) and LINEAR
(your Linear issues). `alt+N` on an item opens the new-thread form filled in
for it, and `alt+r` starts a read-only review or investigation of it.

**States.** weaver follows each agent through its hooks and shows its state
as a cell that reads without colour:

| Cell | State | Meaning |
| --- | --- | --- |
| `[!]` | needs you | A permission prompt, a question, an MCP prompt or an API error |
| `[x]` | done | It finished. It stays under DONE until you look at it |
| `[/]` | working | It is running a turn |
| `[ ]` | idle | It is waiting for your next prompt |
| `[-]` | interrupted | You pressed Esc, or rejected a dialog |

**Its own tmux.** The session runs on a tmux server of its own (`tmux -L
weaver`), so it never touches your own tmux sessions or config.

## Keys

Keys are written as you press them: `alt+r` is Alt and R, and `alt+R` is Alt,
Shift and R.

| Key | Does |
| --- | --- |
| `alt+N` | Start a thread (in a view: a thread on the selected item) |
| `alt+y` | In a thread: the board over it (`tab`: the views; `esc` or `alt+y`: close). On home: each view, then back |
| `alt+1` | The agent that has needed you longest; press again for the next |
| `alt+j` / `alt+k` | The next / previous window, in thread order |
| `alt+h` / `alt+l` | The pane to the left / right |
| `alt+v` / `alt+V` | The focused agent's Neovim / the same, full screen |
| `alt+r` | In a view: a read-only review of the selected item |
| `tab` / `shift+tab` | On home or in a view: the next / previous view |
| `ctrl+q` | Quit the session (it asks first while an agent is mid-turn) |

On the board, `enter` or `1`–`9` jumps to a thread, `n` starts one, `x`
closes one, `r` renames one, `a` adopts one (below), `v` opens its Neovim, `s` swaps the card
for the agent's screen, `/` searches, and `?` lists every key. Everything can
be clicked too: the hints under a list are buttons, and a right click opens a
menu. tmux's own keys work behind its prefix, `ctrl+b`.

## Threads survive crashes and restarts

A thread lasts until you close it. If tmux crashes, you quit with `ctrl+q`, or
the machine restarts while its agent runs, `wvr` brings the session back with
each thread in its window and each agent resuming its conversation. A thread
weaver can't bring back by itself waits on the board under LOST, where `enter`
resumes it.

weaver keeps only threads. To keep an agent you started yourself, *adopt*
it: press `a` on its row, or type `! wvr adopt` into the agent, and it
becomes a thread where it runs, without restarting. To bring in a conversation that
isn't running (one you ran outside tmux before a reboot, say), press `a`
anywhere else on the board and pick it from your recent Claude Code
conversations.

## Neovim

The Neovim beside each agent loads your own config, then follows the agent:
after you have been idle for a few seconds, it opens the file the agent just
edited. It never types for you or replaces a buffer with unsaved changes.

weaver snapshots the worktree at the start and end of every turn, so you can
review exactly what each turn changed:

- `<leader>at` reviews the agent's last turn, and `<leader>ab` the whole
  branch.
- `<leader>an` notes a line for the agent, and `<leader>as` sends your notes
  as its next prompt.

The [guide](docs/guide.md#neovim) lists every review key, and shows how to use
the plugin in your own Neovim.

## Configuration

weaver works without configuration. Environment variables change its
defaults, such as `WEAVER_CLAUDE="claude --model opus"` for the command a
thread's agent runs; the [guide](docs/guide.md#settings) lists them all.

`wvr doctor` checks what weaver depends on and says how to fix anything
missing; `wvr doctor --fix` fixes what it can.

## Documentation

- [Guide](docs/guide.md): weaver in detail, including the session, threads,
  views, the board, Neovim reviews, setup and settings.
- `wvr help`: every command.
- [Design](docs/design.md): the contracts between weaver's parts, its files
  and their formats.
- [Decisions](docs/decisions.md): why weaver is the way it is.

weaver follows Claude Code through its hooks and Codex through an adapter. Any
other agent can report to it with `wvr event`
([design](docs/design.md#events-from-other-agents)).

## Development

The engine is Go (`cmd/wvr`, `internal/`) and the Neovim side is Lua
(`nvim/`). They meet at the contracts in [docs/design.md](docs/design.md).

```sh
test/run.sh    # every suite, each on a throwaway tmux server (needs jq and python3)
./install.sh   # build this checkout into ~/.local/bin/wvr, ahead of Homebrew's
```

Remove `~/.local/bin/wvr` to go back to Homebrew's build. The
[guide](docs/guide.md#development) describes each test suite.

## AI use

weaver is built with AI coding agents, Claude Code among them: they write most
of its code, tests and documentation. I direct the work, make the design
decisions, review what lands and use weaver every day. Read the code with
that in mind, and open an issue for anything that looks wrong.

## Credits

The GITHUB view's searches, and the pull request fields they read, come from
[gh-dash](https://github.com/dlvhdr/gh-dash) by Dolev Hadar (MIT). The LINEAR
view's queries, and the issue fields they read, come from
[linear-tui](https://github.com/k1-c/linear-tui) by k1-c (MIT). Reviews are
built on [diffview-plus.nvim](https://github.com/dlyongemallo/diffview-plus.nvim)
(GPL-3.0), which weaver fetches rather than bundles.

## Licence

MIT: see [LICENSE](LICENSE).
