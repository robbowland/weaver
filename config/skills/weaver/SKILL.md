---
name: weaver
description: How to work in a weaver thread on Rex. Use before starting a helper agent or subagent, before committing, and before reporting that work is done.
# weaver: this file is weaver's, which keeps it up to date while this line stays. Remove the line, and the file is yours.
---
For Rex commands, use the rex skill; this one says how weaver's workflow uses them.

1. One worktree per thread: work only in the worktree you started in, and never create, enter or switch worktrees.
2. Start each helper as its own agent in its own Rex block, beside yours, without taking focus:
   `rex split -s "$REX_SESSION" -b "$REX_BLOCK" --focus=false --label <name> -c "$PWD" -- claude "<task>"`.
   Do not use built-in in-process subagents for work that changes files.
3. Give a helper its whole task, the commit rule below, and what to report.
4. Commit your own work in small Conventional Commits, one concern each, before you report done; leave nothing uncommitted.
5. Several agents in one worktree: commit only the paths you changed, `git commit -m <msg> -- <paths>`, and retry if git reports `index.lock`. A file two agents changed goes whole with the first to commit it.
6. Report done with `git log --oneline <base>..HEAD`: Rob reviews in tuicr, commit by commit.
7. When Rob says the review is ready, read it with `tuicr review list --repo .` and `tuicr review comments --session <slug>` for the newest sessions (a session's slug includes its range, so earlier comments may sit in an older one); answer each comment by its `path:line`, and commit the fixes.
8. A helper reports to the agent that started it when it finishes, with Claude Code's cross-session `SendMessage` to the address that agent put in its task.
   Never type into another agent's block: Enter could answer a permission dialog.
