#!/bin/bash
# weaver's hook engine on Rex: the suites in test/suites/ (the hooks' latency,
# their state machine and its robustness, attention, subagents, the
# installer and the dashboard's renderings), against a throwaway Rex server
# with three fake agents, a and b in window one, c in window two (weaver's
# panes 0, 1 and 2).
#
#   test/hooks.sh [suite...]   default: every suite in test/suites/
#
# Each run gets a fresh scratch directory (kept, and printed, only when a
# suite fails) holding a git repo with two worktrees, the state dir and the
# server's socket. Your real weaver state, Claude Code sessions and Rex
# server are not touched. Skipped without rex.
set -u

# These tests may run inside a Claude Code agent, in a Rex terminal: nothing
# of its session may leak into the fake agents. Every Rex variable but where
# rex is (REX_BIN_DIR) is the caller's: gone.
unset CLAUDE_PID CLAUDE_CODE_SESSION_ID CLAUDECODE CLAUDE_PROJECT_DIR CLAUDE_ENV_FILE WEAVER_SESSION
for v in $(compgen -e | grep '^REX_' | grep -v '^REX_BIN_DIR$'); do unset "$v"; done
command -v rex >/dev/null || { echo "SKIP rex is not installed"; exit 0; }

HERE=$(cd "$(dirname "$0")" && pwd -P)
ROOT=${HERE%/test}
# A Unix socket's path has room for about 100 bytes: not $TMPDIR's.
AT=$(mktemp -d /tmp/weaver-hooks.XXXXXX) || exit 1
# Physical path: git reports roots that way (macOS /tmp is a symlink).
AT=$(cd "$AT" && pwd -P)
# Gone however the run ends (cleanup, below, takes over once there is a
# server to end).
trap 'rm -rf "$AT"' EXIT
SESSION="weaver-test-$$"
export AT

# Scratch repo: main checkout plus two worktrees.
T=$AT/t
mkdir -p "$T"
(
	cd "$T" && git init -q -b main repo && cd repo &&
		git config user.email test@example.com && git config user.name test &&
		mkdir src && printf 'line one\nline two\nline three\n' >src/app.txt && printf 'readme\n' >README.md &&
		git add . && git commit -qm init &&
		git worktree add -q -b feat-a ../repo-wt-a && git worktree add -q -b feat-b ../repo-wt-b
) || exit 1

# weaver from this checkout, first on PATH; a Claude Code config dir of our own.
mkdir -p "$AT/bin" "$AT/state" "$AT/claude/sessions"
# Everything the server runs (its blocks) inherits these: the run's own
# state, never yours.
export XDG_STATE_HOME=$AT/state CLAUDE_CONFIG_DIR=$AT/claude
export WORKTRUNK_CONFIG_PATH=$AT/worktrunk.toml WORKTRUNK_APPROVALS_PATH=$AT/worktrunk-approvals.toml
: >"$WORKTRUNK_CONFIG_PATH"
export WEAVER_SELF_SETUP=0 WEAVER_VIEWS= _ZO_DATA_DIR=$AT/zoxide
# weaver: the binary built from this checkout, its files unpacked here.
(cd "$ROOT" && go build -o "$AT/bin/wvr" ./cmd/wvr) || exit 1
export XDG_DATA_HOME=$AT/data
export PATH="$AT/bin:$PATH"

# The server: its own socket, runtime files and state; no configuration.
export REX_RUNTIME_DIR=$AT/run REX_SERVER=unix://$AT/r.sock
printf -- '-- nothing\n' >"$AT/init.lua"
rex server run --listen "$REX_SERVER" --state-dir "$AT/rexstate" --config "$AT/init.lua" >"$AT/server.log" 2>&1 &
server=$!
R() { rex --autostart=false "$@"; }
for _ in $(seq 100); do R api call server.status '{}' >/dev/null 2>&1 && break; sleep 0.1; done

# A session of three fake agents: a and b side by side in window one, c in
# window two.
agent=(bash --noprofile --norc -c 'echo FAKE AGENT; exec bash --noprofile --norc -i')
new=$(R new "$SESSION" --window one --json -c "$T/repo" --shell=none -- "${agent[@]}") || exit 1
SID=$(printf '%s' "$new" | jq -r .session_id)
B0=$(printf '%s' "$new" | jq -r '.initial_windows[0].block_ids[0]')
B1=$(R split -s "$SID" -b "$B0" --focus=false --json -c "$T/repo-wt-b" --shell=none -- "${agent[@]}" | jq -r '.block_ids[0]')
two=$(R run -s "$SID" --focus=false --json -c "$T/repo" --shell=none -- "${agent[@]}")
B2=$(printf '%s' "$two" | jq -r '.block_ids[0]')
W0=$(printf '%s' "$new" | jq -r '.initial_windows[0].window_id') W1=$(printf '%s' "$two" | jq -r .window_id)
[ -n "$SID" ] && [ -n "$B1" ] && [ -n "$B2" ] && [ "$B1" != null ] && [ "$B2" != null ] || {
	echo "the test server's session was not made" >&2
	exit 1
}
R api call session.focus_block "{\"session_id\":\"$SID\",\"block_id\":\"$B0\"}" >/dev/null
# weaver's: its record of the session (as it makes one: its name, when and
# its server), and its numbers 0, 1 and 2 for the three agents' blocks, 10
# and 11 for the windows (as it numbers the blocks and windows it meets).
RD=$AT/state/weaver/rex
mkdir -p "$RD/sessions" "$RD/n" "$RD/u"
pid=$(R api call server.status '{}' | jq -r .pid)
printf '%s\n%s\n%s\n' "$SESSION" "$(perl -MTime::HiRes=time -e 'printf "%.3f", time')" "$pid" >"$RD/sessions/${SID/:/_}"
number() {
	printf '%s\n' "$2" >"$RD/n/$1"
	printf '%s\n' "$1" >"$RD/u/${2/:/_}"
}
number 0 "$B0"
number 1 "$B1"
number 2 "$B2"
number 10 "$W0"
number 11 "$W1"
echo "$SESSION" >"$AT/session"
echo "$SID" >"$AT/sid"

# The server goes; the scratch dir too, unless a suite failed (it is
# printed, to look into).
keep=0
cleanup() {
	trap - EXIT INT TERM HUP
	kill "$server" 2>/dev/null && wait "$server" 2>/dev/null
	[ "$keep" = 1 ] || { chmod -R u+w "$AT" 2>/dev/null; rm -rf "$AT"; }
}
trap cleanup EXIT
trap 'exit 130' INT TERM HUP

if [ $# -eq 0 ]; then
	set --
	for f in "$HERE"/suites/*.sh; do
		f=${f##*/}
		set -- "$@" "${f%.sh}"
	done
fi
failed=0
for suite in "$@"; do
	suite=${suite##*/}
	suite=${suite%.sh}
	echo "=== $suite"
	bash "$HERE/suites/$suite.sh" || failed=$((failed + 1))
done

[ $failed -gt 0 ] && keep=1
cleanup
if [ $failed -gt 0 ]; then
	echo "scratch dir: $AT"
	echo "$failed suite(s) failed"
	exit 1
fi
echo "all suites passed"
