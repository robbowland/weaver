---
name: testing
description: Use when running, writing or judging weaver's tests: which suite covers what, how long they take, the checks that flake under load and how to tell a flake from a regression, and the habits that keep a fix honest (a red run before the fix, hermetic servers). For Rex-specific testing see the rex skill.
---

# Testing weaver

## The suites

`test/run.sh` runs them all (~4 min): `go vet` + `go test ./...` (with the
Go/nvim/design.md contract tests), `test/nvim/run.sh` (headless specs; needs
diffview-plus, gitsigns, snacks in `WEAVER_TEST_PLUGINS` or lazy's dir),
`test/hooks.sh [suite…]` (~2 min, test/suites/*.sh on a throwaway Rex
server), `test/rex.sh` (~20 s, weaver on a throwaway Rex server end to end);
both skip without rex. CI (macos-15, no Rex: it runs only Go's and the
Neovim specs) runs `test/run.sh` and golangci-lint with only-new-issues:
keep lint at its count (42 at the time of writing; `golangci-lint run
./...`).

Every suite starts its own server and state (`$AT`, `XDG_STATE_HOME`), never
the user's Rex — keep new checks that way: count only the test's
own sockets and sessions (a real weaver session on the machine made a
`rex-*.fzf.*.sock` glob fail).

## Flakes under load (not regressions)

- `05-latency`'s idle async hook is measured against `wvr version` started
  in the same run (process start is most of it: ~2 ms idle, 10+ ms loaded).
- A check that reads the screen after a restart must wait for what it
  reads, not only for the process (a view captured before its new fzf drew,
  on a busy machine).
- `05-latency` runs its hooks in no pane, so it measures wvr's start and
  the hook's gate, not the work an event does (an async Edit with a pane
  costs ~70 ms, at 61dca98 on tmux too).

To tell: rerun the suite alone with the machine idle, and compare against
the base commit in a temporary `git worktree`. Never run `go test` or builds
alongside `test/hooks.sh` or `test/rex.sh`, which build wvr when
they start, so do not edit Go while one is about to.

## Habits

- Reproduce first: a check that fails on the code before the fix (revert
  the fix in place, run, restore), then passes with it. Say so in the
  commit's Verified line.
- Test at the boundary the bug crossed: a real Rex server (rextest), the
  real key path
  (`client.key` on Rex), the real nvim (test/nvim).
- Commit messages: `type(scope): what is true now`, a body saying what was
  wrong and why, and a `Verified:` paragraph naming what ran.
