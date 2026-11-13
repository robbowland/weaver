# Hook engine conformance

These cases test weaver's hook engine by its contract alone: the events
Claude Code sends to `wvr hook`, and what the agent's state file then says
([design.md](../../docs/design.md#state-file)). They know nothing of how
weaver is built, so any build passes or fails them alike, whatever language
it is written in:

```sh
test/conformance/run.py                       # this checkout's build (go build)
test/conformance/run.py --wvr /path/to/wvr    # any other build
test/conformance/run.py cases/06-batch.json   # one case
```

The runner needs Python 3 and nothing else: no Rex server, no network, no
git. It runs in about a second, and `test/run.sh` (so CI) runs it.

## What the binary is given

Each case gets a directory of its own (`$T` in a case), and the binary the
environment of an agent's pane in a weaver session on Rex, made of files:

- `XDG_STATE_HOME=$T/state`: weaver's record of the Rex session
  (`weaver/rex/sessions/<session id>`: its name, `conf`, when it was made,
  the server's pid) and its number, 7, for the pane's block (`weaver/rex/n/7`,
  `weaver/rex/u/<block id>`), so the state file is
  `$T/state/weaver/rex-conf_7.json`.
- `REX_SESSION`, `REX_BLOCK`: that session and block.
- `REX_SERVER`: a Unix socket nothing listens on, and Rex's runtime file
  for it (`REX_RUNTIME_DIR`) naming a server that runs (the runner itself):
  Rex never answers, as when a call to it fails.
- `CLAUDE_CONFIG_DIR=$T/claude`, `HOME=$T/home`, `PATH=/usr/bin:/bin`,
  `WEAVER_SELF_SETUP=0`, `WEAVER_WT_MARKERS=0`.
- `CLAUDE_PID`: the pid of a process standing in for the agent (a `sleep`),
  or of another live process for a step with `"pid": "other"`, or none for
  `"pid": null`.
- `$T/repo/.git`: the work tree events' `cwd` is in by default; a case's
  `mkdirs` adds others.

Each step runs `wvr hook` (or `wvr hook --async`, as
[claude-hooks.json](../../config/claude-hooks.json) routes the event unless
the step says) with the event on stdin, and reads the state file and
stdout (`{"terminalSequence":"\u0007"}` rings the bell).

## A case

```json
{"about": "what it shows",
 "defaults": {"session_id": "conv-1", "cwd": "$T/repo"},
 "mkdirs": ["wt/.git"],
 "steps": [
   {"event": {"hook_event_name": "UserPromptSubmit", "prompt": "go"},
    "expect": {"state": "working", "turn": 1}, "bell": false},
   {"age": 20, "event": {"hook_event_name": "PostToolUse", "tool_name": "Read"},
    "expect": {"state": "working"}}]}
```

- `defaults` are merged into every event (these are the defaults).
- `hook`: `sync` or `async`; else as claude-hooks.json routes the event.
- `age`: the stored state made N seconds older first (its `since` and
  `ts`), for what depends on time (a late hook's window).
- `expect`: fields of the state file after the step; `$T` is the case's
  directory; `{"len": N}` a list's length; `null`, `""`, `0`, `false` and
  `[]` all mean absent. `pid` takes `"agent"` or `"other"`.
- `bell`: whether the step rang.

A failing case keeps its directory, and says where.

## Adding one

Write what Claude Code sends (its [hooks
reference](https://code.claude.com/docs/en/hooks) has every event's input)
and what the board must then say, as a case of its own, numbered after the
last. A bug in the hook engine gets its case before its fix: run it against
the build before the fix (`--wvr`) to see it fail.
