#!/usr/bin/env python3
"""weaver's hook engine, by its contract alone: Claude Code's hook events
in, the agent's state file and the bell out (docs/design.md, State file).

Each case in cases/*.json is a run of events, as Claude Code sends them to
`wvr hook` (sync) or `wvr hook --async` (config/claude-hooks.json), each
with what the state file says after it. The runner knows nothing of how
weaver is built: it gives a binary the environment of an agent's pane in a
weaver session on Rex (the files weaver keeps of Rex's ids, and Rex's
runtime file), with no Rex server and no network, and reads what it wrote.
Any build of weaver, in any language, passes or fails it alike.

    test/conformance/run.py [--wvr PATH] [CASE...]

A case:

    {"about": "what it shows",
     "mkdirs": ["wt/.git"],                  directories under the case's own ($T), work trees and all
     "steps": [
       {"hook": "sync" | "async",            default: as claude-hooks.json routes it
        "event": {...},                      the hook's input: session_id, cwd, ... default from the case
        "pid": "agent" | "other" | null,      CLAUDE_PID: the case's agent, another live process, none
        "age": N,                            before it: the stored state N seconds older (since, ts)
        "expect": {"state": "working", ...}, fields of the state file after it ($T: the case's dir)
        "bell": true | false}]}              whether the hook rang (stdout: terminalSequence BEL)

"defaults" in a case are merged into every event (session_id, cwd).
"""
import argparse
import atexit
import glob
import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(os.path.dirname(HERE))

# The events claude-hooks.json sends through the async hook.
ASYNC = {"PostToolUse", "PostToolUseFailure", "PostToolBatch", "SubagentStart", "SubagentStop",
         "CwdChanged", "TaskCreated", "TaskCompleted"}
SESSION, BLOCK, PANE = "session:01a1d200-0000-7000-8000-00000000c0f0", "block:01a1d200-0000-7000-8000-0000000b10c0", "7"


def build():
    d = tempfile.mkdtemp(prefix="wvr-conf-bin.")
    atexit.register(shutil.rmtree, d, True)
    out = os.path.join(d, "wvr")
    subprocess.run(["go", "build", "-o", out, "./cmd/wvr"], cwd=ROOT, check=True)
    return out


def pane(at):
    """The environment of an agent's pane: weaver's record of its Rex
    session and its number for the pane's block; Rex's runtime file, naming
    a server that runs (this runner), at a socket nothing listens on."""
    state = os.path.join(at, "state")
    rd = os.path.join(state, "weaver", "rex")
    for d in ("sessions", "n", "u"):
        os.makedirs(os.path.join(rd, d), exist_ok=True)
    with open(os.path.join(rd, "sessions", SESSION.replace(":", "_", 1)), "w") as f:
        f.write("conf\n%.3f\n%d\n" % (time.time() - 60, os.getpid()))
    with open(os.path.join(rd, "n", PANE), "w") as f:
        f.write(BLOCK + "\n")
    with open(os.path.join(rd, "u", BLOCK.replace(":", "_", 1)), "w") as f:
        f.write(PANE + "\n")
    sock = os.path.join(at, "no.sock")
    ep = "unix://" + sock
    run = os.path.join(at, "run", "servers", hashlib.sha256(ep.encode()).hexdigest())
    os.makedirs(run)
    with open(os.path.join(run, "runtime.json"), "w") as f:
        json.dump({"endpoint": ep, "pid": os.getpid()}, f)
    home = os.path.join(at, "home")
    os.makedirs(os.path.join(at, "claude", "sessions"))
    os.makedirs(home)
    return {
        "HOME": home, "PATH": "/usr/bin:/bin", "TERM": "dumb",
        "XDG_STATE_HOME": state, "XDG_CONFIG_HOME": os.path.join(at, "config"), "XDG_DATA_HOME": os.path.join(at, "data"),
        "CLAUDE_CONFIG_DIR": os.path.join(at, "claude"),
        "REX_SERVER": ep, "REX_RUNTIME_DIR": os.path.join(at, "run"), "REX_SESSION": SESSION, "REX_BLOCK": BLOCK,
        "WEAVER_SELF_SETUP": "0", "WEAVER_WT_MARKERS": "0", "WEAVER_VIEWS": "",
    }, os.path.join(state, "weaver", "rex-conf_" + PANE + ".json")


def expand(v, at):
    if isinstance(v, str):
        return v.replace("$T", at)
    if isinstance(v, list):
        return [expand(x, at) for x in v]
    if isinstance(v, dict):
        return {k: expand(x, at) for k, x in v.items()}
    return v


def run_case(wvr, path):
    case = json.load(open(path))
    at = os.path.realpath(tempfile.mkdtemp(prefix="wvr-conf."))
    fails = []
    agent = subprocess.Popen(["/bin/sleep", "600"])
    other = subprocess.Popen(["/bin/sleep", "600"])
    try:
        env, sf = pane(at)
        os.makedirs(os.path.join(at, "repo", ".git"))
        # Other work trees the case's events name (a .git each).
        for d in case.get("mkdirs", []):
            os.makedirs(os.path.join(at, d), exist_ok=True)
        defaults = expand(case.get("defaults", {"session_id": "conv-1", "cwd": "$T/repo"}), at)
        for i, step in enumerate(case["steps"], 1):
            if step.get("age") and os.path.exists(sf):
                st = json.load(open(sf))
                for k in ("since", "ts"):
                    if k in st:
                        st[k] = int(st[k]) - step["age"]
                with open(sf, "w") as f:
                    json.dump(st, f)
            ev = dict(defaults)
            ev.update(expand(step["event"], at))
            mode = step.get("hook") or ("async" if ev["hook_event_name"] in ASYNC else "sync")
            e = dict(env)
            pid = {"agent": agent.pid, "other": other.pid}.get(step.get("pid", "agent"))
            if pid:
                e["CLAUDE_PID"] = str(pid)
            argv = [wvr, "hook"] + (["--async"] if mode == "async" else [])
            p = subprocess.run(argv, input=json.dumps(ev).encode(), env=e, capture_output=True, timeout=20)
            rang = b"terminalSequence" in p.stdout
            name = "%s step %d (%s %s)" % (os.path.basename(path), i, mode, ev["hook_event_name"])
            if "bell" in step and step["bell"] != rang:
                fails.append("%s: bell %s, want %s" % (name, rang, step["bell"]))
            want = expand(step.get("expect", {}), at)
            if want:
                try:
                    got = json.load(open(sf))
                except (OSError, ValueError) as x:
                    fails.append("%s: no state (%s)" % (name, x))
                    continue
                for k, v in want.items():
                    g = got.get(k)
                    if k == "pid":
                        v = {"agent": agent.pid, "other": other.pid}.get(v, v)
                        g = int(g) if g not in (None, "") else None
                    if isinstance(v, dict) and set(v) == {"len"}:
                        g, v = len(g or []), v["len"]
                    if g != v and not (v in ("", None, 0, False, []) and g in ("", None, 0, False, [])):
                        fails.append("%s: %s = %r, want %r" % (name, k, g, v))
    finally:
        for p in (agent, other):
            p.kill()
            p.wait()
        if not fails:
            shutil.rmtree(at, ignore_errors=True)
        else:
            fails.append("  (its files: %s)" % at)
    return case.get("about", ""), fails


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--wvr", help="the weaver binary (default: built from this checkout)")
    ap.add_argument("cases", nargs="*")
    a = ap.parse_args()
    wvr = a.wvr or build()
    cases = a.cases or sorted(glob.glob(os.path.join(HERE, "cases", "*.json")))
    bad = 0
    for c in cases:
        about, fails = run_case(wvr, c)
        if fails:
            bad += 1
            print("FAIL %s: %s" % (os.path.basename(c), about))
            for f in fails:
                print("  " + f)
        else:
            print("PASS %s: %s" % (os.path.basename(c), about))
    print("-- %d passed, %d failed" % (len(cases) - bad, bad))
    sys.exit(1 if bad else 0)


if __name__ == "__main__":
    main()
