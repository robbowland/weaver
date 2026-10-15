#!/bin/bash
# weaver's hook engine on tmux: the suites in test/suites/ that test the hooks
# (their state machine and its robustness, subagents, per-turn snapshots, other
# agents' events) rather than a multiplexer's keys, against a throwaway tmux
# server with three fake agents, a and b in window one, c in window two, as
# test/zellij.sh lays them out in zellij (so their panes are 0, 1 and 2).
#
#   test/hooks.sh [suite...]   default: 20-robustness 30-attention 40-subagents 45-snapshots 60-agents
#
# Each run gets a fresh scratch directory (printed at the end) holding a git
# repo with two worktrees, the state dir and the server's socket. Your real
# weaver state, Claude Code sessions and tmux servers are not touched.
set -u

# These tests may run inside a Claude Code agent: nothing of its session may
# leak into the fake agents.
unset CLAUDE_PID CLAUDE_CODE_SESSION_ID CLAUDECODE CLAUDE_PROJECT_DIR CLAUDE_ENV_FILE
unset TMUX TMUX_PANE ZELLIJ ZELLIJ_SESSION_NAME ZELLIJ_PANE_ID WEAVER_MUX WEAVER_TMUX_SESSION WEAVER_TMUX_SOCKET

HERE=$(cd "$(dirname "$0")" && pwd -P)
ROOT=${HERE%/test}
AT=$(mktemp -d /tmp/weaver-hooks.XXXXXX) || exit 1
# Physical path: git reports roots that way (macOS /tmp is a symlink).
AT=$(cd "$AT" && pwd -P)
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
# Everything the server runs (its keys' run-shell, its panes) inherits these:
# the run's own state, never yours.
export XDG_STATE_HOME=$AT/state CLAUDE_CONFIG_DIR=$AT/claude WEAVER_FOLLOW_IDLE_MS=300 WEAVER_MUX=tmux
export WORKTRUNK_CONFIG_PATH=$AT/worktrunk.toml WORKTRUNK_APPROVALS_PATH=$AT/worktrunk-approvals.toml
: >"$WORKTRUNK_CONFIG_PATH"
export _ZO_DATA_DIR=$AT/zoxide EDITOR=nvim VISUAL=nvim
if [ -f "$HERE/nvim/init.lua" ] && [ "${WEAVER_TEST_USER_NVIM:-0}" != 1 ]; then
	export VIMINIT="lua dofile('$HERE/nvim/init.lua')"
fi
ln -s "$ROOT/weaver" "$AT/bin/weaver"
export PATH="$AT/bin:$PATH"

# The server: its own socket and weaver's tmux config (its keys and hooks);
# three fake agents.
SOCK=$AT/tmux.sock
agent='echo FAKE AGENT; exec bash --noprofile --norc -i'
tmux -S "$SOCK" -f "$ROOT/config/tmux.conf" new-session -d -s "$SESSION" -x 200 -y 50 -n one -c "$T/repo" "bash --noprofile --norc -c '$agent'" \; \
	split-window -h -t "=$SESSION:one" -c "$T/repo-wt-b" "bash --noprofile --norc -c '$agent'" \; \
	new-window -d -t "=$SESSION:" -n two -c "$T/repo" "bash --noprofile --norc -c '$agent'" || exit 1
[ "$(tmux -S "$SOCK" list-panes -s -t "=$SESSION" -F '#{pane_id}' | tr '\n' ' ')" = "%0 %1 %2 " ] || {
	echo "the test server's panes are not %0 %1 %2" >&2
	exit 1
}
echo "$SESSION" >"$AT/session"
echo tmux >"$AT/mux"
echo "$SOCK" >"$AT/socket"

# A client on it, in a pseudo-terminal as a terminal would run it: the suites
# press keys through it ($AT/keys.fifo, as on zellij).
mkfifo "$AT/keys.fifo"
TERM=xterm-256color python3 -I "$HERE/pty_drive.py" "$AT/keys.fifo" "$AT/screen.log" \
	tmux -S "$SOCK" attach -t "=$SESSION" &
driver=$!
for _ in $(seq 50); do [ -n "$(tmux -S "$SOCK" list-clients 2>/dev/null)" ] && break; sleep 0.1; done

cleanup() {
	trap - EXIT INT TERM HUP
	tmux -S "$SOCK" kill-server >/dev/null 2>&1
	kill "$driver" 2>/dev/null
}
trap cleanup EXIT
trap 'exit 130' INT TERM HUP

[ $# -eq 0 ] && set -- 20-robustness 30-attention 40-subagents 45-snapshots 60-agents
failed=0
for suite in "$@"; do
	suite=${suite##*/}
	suite=${suite%.sh}
	echo "=== $suite"
	bash "$HERE/suites/$suite.sh" || failed=$((failed + 1))
done

cleanup
echo "scratch dir: $AT"
if [ $failed -gt 0 ]; then
	echo "$failed suite(s) failed"
	exit 1
fi
echo "all suites passed"
