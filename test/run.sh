#!/bin/bash
# End-to-end tests: start a throwaway zellij session in a pty, fake Claude Code
# hook events into it, press the real keybindings and check what happens.
#
#   test/run.sh [suite...]     suites from test/suites/ (default: all, in order)
#
# Each run gets a fresh scratch directory (printed at the end, never deleted)
# holding a git repo with two worktrees, the state dir and the zellij config.
# nvim uses your own config, so plugin interplay (noice, gitsigns, diffview)
# and hit-enter prompts are exercised too. Your real agentnav state and
# zellij sessions are not touched.
set -u

HERE=$(cd "$(dirname "$0")" && pwd -P)
ROOT=${HERE%/test}
AT=$(mktemp -d "${TMPDIR:-/tmp}/agentnav-test.XXXXXX") || exit 1
# Physical path: git reports roots that way (macOS /var is a symlink).
AT=$(cd "$AT" && pwd -P)
SESSION="agentnav-test-$$"
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

# agentnav from this checkout, first on PATH.
mkdir -p "$AT/bin" "$AT/state"
ln -s "$ROOT/agentnav" "$AT/bin/agentnav"

# zellij config: defaults plus this checkout's keybindings.
{
	echo "keybinds {"
	sed 's/^/    /' "$ROOT/config/zellij-keybinds.kdl"
	echo "}"
	echo "show_startup_tips false"
	echo "show_release_notes false"
	echo "session_serialization false"
} >"$AT/zellij.kdl"
zellij --config "$AT/zellij.kdl" setup --check 2>&1 | grep -q 'Well defined' || {
	echo "zellij rejected $AT/zellij.kdl" >&2
	exit 1
}

# Three fake agents: a and b in tab one, c in tab two.
agent='echo FAKE AGENT; exec bash --noprofile --norc -i'
cat >"$AT/layout.kdl" <<EOF
layout {
    tab name="one" focus=true {
        pane split_direction="vertical" {
            pane name="agent-a" cwd="$T/repo" command="bash" { args "--noprofile" "--norc" "-c" "$agent"; }
            pane name="agent-b" cwd="$T/repo-wt-b" command="bash" { args "--noprofile" "--norc" "-c" "$agent"; }
        }
    }
    tab name="two" {
        pane name="agent-c" cwd="$T/repo" command="bash" { args "--noprofile" "--norc" "-c" "$agent"; }
    }
}
EOF

echo "$SESSION" >"$AT/session"
mkfifo "$AT/keys.fifo"
ZELLIJ_CONFIG_FILE="$AT/zellij.kdl" XDG_STATE_HOME="$AT/state" PATH="$AT/bin:$PATH" TERM=xterm-256color \
	python3 -I "$HERE/pty_drive.py" "$AT/keys.fifo" "$AT/screen.log" \
	zellij --config "$AT/zellij.kdl" --session "$SESSION" --new-session-with-layout "$AT/layout.kdl" &
driver=$!

for _ in 1 2 3 4 5 6 7 8 9 10; do
	ZELLIJ_SESSION_NAME=$SESSION zellij action list-panes -j >/dev/null 2>&1 && break
	sleep 0.5
done

if [ $# -eq 0 ]; then
	set -- "$HERE"/suites/*.sh
fi
failed=0
for suite in "$@"; do
	echo "=== ${suite##*/}"
	bash "$suite" || failed=$((failed + 1))
done

zellij kill-session "$SESSION" >/dev/null 2>&1
sleep 0.5
zellij delete-session "$SESSION" >/dev/null 2>&1
wait "$driver" 2>/dev/null
echo "scratch dir: $AT"
if [ $failed -gt 0 ]; then
	echo "$failed suite(s) failed"
	exit 1
fi
echo "all suites passed"
