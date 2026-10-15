package wv

// Usage is `weaver help`.
const Usage = `usage: weaver [command]
  (none)                       outside tmux: attach to, or start, the session "weaver"
                               ($WEAVER_SESSION) on weaver's own tmux server; inside
                               tmux: the dashboard
  start [session]              the same, for another session
  new [task...]                start a thread: a new tab with nvim and an agent, in a new
                               worktree whose branch starts from the remote's default
                               branch, fetched; with no task, a form. Options: --repo DIR,
                               --branch NAME, --base REF (@: this checkout's HEAD),
                               --here (the current checkout),
                               --agent claude|codex, --edit (write the task in $EDITOR),
                               --from PANE (that agent's repository), --no-focus
  close [--yes] [--remove-worktree] [pane-id]
                               close an agent (the focused one) with its nvim, and its
                               thread's tab; offers to remove a thread's worktree when it
                               is clean and merged or pushed
  resume [--all] [which]       threads lost when their session went (a crash, or quit):
                               alone, list them; with a name, number or session id, bring
                               that one back in a new tab resuming its conversation;
                               --all, every one. --forget [--yes] which: drop one
  send [--force] PANE < text   the text to agent PANE's prompt (its nvim's :AgentSend), when
                               it waits for you to say more; --force, even while it works
  quit [--yes]                 quit this session (outside it, "weaver") with
                               every agent and nvim in it, after asking; its threads are
                               kept and come back as the weaver session starts again
  hook [--async]               Claude Code hook (reads the event JSON on stdin)
  event [--bell]               an event of any agent, as normalized JSON on stdin
  adapt codex [--bell]         Codex hook (reads Codex's event JSON on stdin)
  edit [--full] [pane-id]      open or jump to the nvim for the focused agent
  ls                           live dashboard of the agents in this session
  next                         jump to the next agent that needs you; repeat to cycle
  status [--tmux SESSION]      agent counts (--tmux: for tmux's status line)
  snapshot --reviewed [dir]    mark the worktree holding dir reviewed
  gc                           forget agents that are gone, in every session
  nvim <agent-pane> [file]     internal: start the nvim bound to an agent
`
