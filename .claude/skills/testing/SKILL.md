---
name: testing
description: Use when running, writing or judging weaver's tests: which suite covers what, how long they take, the checks that flake under load and how to tell a flake from a regression, and the habits that keep a fix honest (a red run before the fix, hermetic servers). For Rex-specific testing see the rex skill.
---

# Testing weaver

## The suites

`test/run.sh` runs them all (~3 min): `go vet` + `go test ./...` (with the
Go/design.md state contract), `test/conformance/run.py` (~1 s, no Rex: the
hook engine's events in and state file out, as JSON cases any build of
weaver must pass, `--wvr PATH` for another build; see its README),
`test/hooks.sh [suite…]` (~2 min,
test/suites/*.sh on a throwaway Rex server), `test/rex.sh` (~30 s, weaver
on a throwaway Rex server end to end, with a fake tuicr); both skip without
rex. CI (macos-15, no Rex: it runs only Go's tests) runs `test/run.sh` and golangci-lint with only-new-issues:
keep lint at its count (36 at the time of writing; `golangci-lint run
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
- `05-latency` times hooks run as the agent's pane, and checks they were:
  a hook from no pane does nothing, which is all it timed for a while
  (zellij's `ZELLIJ_PANE_ID`, which named the pane, outlived zellij).

To tell: rerun the suite alone with the machine idle, and compare against
the base commit in a temporary `git worktree`. Never run `go test` or builds
alongside `test/hooks.sh` or `test/rex.sh`, which build wvr when
they start, so do not edit Go while one is about to.

## Habits

- Reproduce first: a check that fails on the code before the fix (revert
  the fix in place, run, restore), then passes with it. Say so in the
  commit's Verified line.
- Test at the boundary the bug crossed: a real Rex server (rextest), the
  real key path (`client.key` on Rex), and by hand what no suite reaches
  (below).
- Commit messages: `type(scope): what is true now`, a body saying what was
  wrong and why, and a `Verified:` paragraph naming what ran.

## By hand

What the suites cannot reach, checked in a scratch Rex session with the
app attached:

- The real tuicr: its `:q`, `e`, saved comments.
- wvr opening its popups: Ctrl q's card, Alt N's form, the board's `x`,
  Alt y over a thread. wvr opens a popup only when an app is attached, and
  a test server has none, so `test/rex.sh` opens a card in a floating
  layer itself.
