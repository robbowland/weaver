---
name: weaver
description: How to work in a weaver thread on Rex. Use before starting a helper agent or subagent, before committing, before reporting that work is done, and once a helper has reported.
# weaver: this file is weaver's, which keeps it up to date while this line stays. Remove the line, and the file is yours.
---
For Rex commands, use the rex skill; this one says how weaver's workflow uses them.

1. Your thread is a Rex session of its own (`$REX_SESSION`) on one worktree: work in the worktree you started in, and leave creating, entering or switching worktrees to your user.
2. Start each helper as its own agent in a new window of your thread's session, named for its job, without taking focus, and keep the window id the command prints:
   `rex window new -s "$REX_SESSION" <name> --focus=false -c "$PWD" --json -- claude "<task>"`.
   Your own window stays the agent's and its review's alone, and a helper in another session is outside your thread: so no `rex split` and no `rex new` for helpers. Use helpers, not built-in in-process subagents, for work that changes files.
3. Give a helper its whole task, the commit rule below, what to report, and your cross-session `SendMessage` address to report to.
4. Commit your own work in small Conventional Commits, one concern each, before you report done; leave nothing uncommitted.
5. Several agents in one worktree: commit only the paths you changed, `git commit -m <msg> -- <paths>`, and retry if git reports `index.lock`. A file two agents changed goes whole with the first to commit it.
6. Report done with `git log --oneline <base>..HEAD`: your user reviews in tuicr, commit by commit.
7. When your user says the review is ready, read it with `tuicr review list --repo .` and `tuicr review comments --session <slug>` for the newest sessions (a session's slug includes its range, so earlier comments may sit in an older one); answer each comment by its `path:line`, and commit the fixes.
8. As a helper, report to the agent that started you with `SendMessage` to the address in your task, then stop and wait: the agent that started you closes your window. Talk to other agents only through `SendMessage`, never by typing into their blocks: Enter could answer a permission dialog.
9. Once you have taken a helper's report and its commits are on the branch, check that it left nothing uncommitted in the paths it owned (`git status --porcelain -- <paths>`). Clean: close its window, which ends it, `rex window close -s "$REX_SESSION" <window-id>`. Leftovers: ask the helper to commit them, or report them to your user, and keep its window open.
