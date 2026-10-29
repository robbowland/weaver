#!/bin/bash
# weaver's hook engine on tmux: the suites in test/suites/ (the hooks' latency,
# their state machine and its robustness, attention, subagents, per-turn
# snapshots, other agents' events, the installer and the dashboard's
# renderings), against a throwaway tmux server with three fake agents, a and b
# in window one, c in window two (so their panes are 0, 1 and 2).
#
#   test/hooks.sh [suite...]   default: every suite in test/suites/
#
# Each run gets a fresh scratch directory (kept, and printed, only when a
# suite fails) holding a git repo with two worktrees, the state dir and the
# server's socket. Your real weaver state, Claude Code sessions and tmux
# servers are not touched.
set -u

# These tests may run inside a Claude Code agent: nothing of its session may
# leak into the fake agents.
unset CLAUDE_PID CLAUDE_CODE_SESSION_ID CLAUDECODE CLAUDE_PROJECT_DIR CLAUDE_ENV_FILE
unset TMUX TMUX_PANE ZELLIJ ZELLIJ_SESSION_NAME ZELLIJ_PANE_ID WEAVER_TMUX_SESSION WEAVER_TMUX_SOCKET

HERE=$(cd "$(dirname "$0")" && pwd -P)
ROOT=${HERE%/test}
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
# Everything the server runs (its keys' run-shell, its panes) inherits these:
# the run's own state, never yours.
export XDG_STATE_HOME=$AT/state CLAUDE_CONFIG_DIR=$AT/claude
export WORKTRUNK_CONFIG_PATH=$AT/worktrunk.toml WORKTRUNK_APPROVALS_PATH=$AT/worktrunk-approvals.toml
: >"$WORKTRUNK_CONFIG_PATH"
export WEAVER_SELF_SETUP=0 _ZO_DATA_DIR=$AT/zoxide EDITOR=nvim VISUAL=nvim
if [ -f "$HERE/nvim/init.lua" ] && [ "${WEAVER_TEST_USER_NVIM:-0}" != 1 ]; then
	export VIMINIT="lua dofile('$HERE/nvim/init.lua')"
fi
# weaver: the binary built from this checkout, its files unpacked here.
(cd "$ROOT" && go build -o "$AT/bin/wvr" ./cmd/wvr) || exit 1
export XDG_DATA_HOME=$AT/data
export PATH="$AT/bin:$PATH"

# The server: its own socket and weaver's tmux config (its keys and hooks);
# three fake agents.
SOCK=$AT/tmux.sock
agent='echo FAKE AGENT; exec bash --noprofile --norc -i'
tmux -S "$SOCK" -f "$ROOT/config/tmux.conf" new-session -d -s "$SESSION" -x 200 -y 50 -n one -c "$T/repo" "bash --noprofile --norc -c '$agent'" \; \
	split-window -h -t "=$SESSION:one" -c "$T/repo-wt-b" "bash --noprofile --norc -c '$agent'" \; \
	new-window -d -t "=$SESSION:" -n two -c "$T/repo" "bash --noprofile --norc -c '$agent'" \; \
	select-pane -t %0 || exit 1
[ "$(tmux -S "$SOCK" list-panes -s -t "=$SESSION" -F '#{pane_id}' | tr '\n' ' ')" = "%0 %1 %2 " ] || {
	echo "the test server's panes are not %0 %1 %2" >&2
	exit 1
}
echo "$SESSION" >"$AT/session"
echo "$SOCK" >"$AT/socket"

# A client on it, in a pseudo-terminal as a terminal would run it: the suites
# press keys through it ($AT/keys.fifo).
mkfifo "$AT/keys.fifo"
TERM=xterm-256color python3 -I "$HERE/pty_drive.py" "$AT/keys.fifo" "$AT/screen.log" \
	tmux -S "$SOCK" attach -t "=$SESSION" &
driver=$!
for _ in $(seq 50); do [ -n "$(tmux -S "$SOCK" list-clients 2>/dev/null)" ] && break; sleep 0.1; done

# The server, its client and the nvims of its threads go; the scratch dir
# too, unless a suite failed (it is printed, to look into).
keep=0
cleanup() {
	trap - EXIT INT TERM HUP
	tmux -S "$SOCK" kill-server >/dev/null 2>&1
	kill "$driver" 2>/dev/null && wait "$driver" 2>/dev/null
	pkill -f -- "--listen /tmp/weaver-$(id -u)/[^ ]*${SESSION}_" 2>/dev/null
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
