#!/bin/bash
# End-to-end tests: start a throwaway zellij session in a pty, fake Claude Code
# hook events into it, press the real keybindings and check what happens.
#
#   test/run.sh [suite...]     suites from test/suites/ (default: all, in order)
#
# Each run gets a fresh scratch directory (printed at the end, never deleted)
# holding a git repo with two worktrees, the state dir and the zellij config.
# nvim starts from test/nvim/init.lua (a minimal config) when it exists; set
# WEAVER_TEST_USER_NVIM=1 to run it with your own config instead. Your real
# weaver state, Claude Code sessions and zellij sessions are not touched.
set -u

# These tests may run inside a Claude Code agent: nothing of its session may
# leak into the fake agents.
unset CLAUDE_PID CLAUDE_CODE_SESSION_ID CLAUDECODE CLAUDE_PROJECT_DIR CLAUDE_ENV_FILE

HERE=$(cd "$(dirname "$0")" && pwd -P)
ROOT=${HERE%/test}
AT=$(mktemp -d "${TMPDIR:-/tmp}/weaver-test.XXXXXX") || exit 1
# Physical path: git reports roots that way (macOS /var is a symlink).
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
export CLAUDE_CONFIG_DIR=$AT/claude
# Follow edits after 0.3s of idle instead of 3s.
export WEAVER_FOLLOW_IDLE_MS=300
# Worktrunk with its defaults, not your config, and approvals of its own.
export WORKTRUNK_CONFIG_PATH=$AT/worktrunk.toml WORKTRUNK_APPROVALS_PATH=$AT/worktrunk-approvals.toml
: >"$WORKTRUNK_CONFIG_PATH"
# Nor your zoxide history (the new-thread form offers its repositories), nor
# your editor (the form's ctrl-e).
export _ZO_DATA_DIR=$AT/zoxide EDITOR=nvim VISUAL=nvim
if [ -f "$HERE/nvim/init.lua" ] && [ "${WEAVER_TEST_USER_NVIM:-0}" != 1 ]; then
	export VIMINIT="lua dofile('$HERE/nvim/init.lua')"
fi
ln -s "$ROOT/weaver" "$AT/bin/weaver"

# zellij shim, also first on PATH (weaver calls zellij through PATH too).
# When the named session is gone, `zellij action` silently picks another
# running session: never let a test act on one of yours.
REAL_ZELLIJ=$(command -v zellij) || exit 1
cat >"$AT/bin/zellij" <<EOF
#!/bin/bash
case " \$* " in
*" action "* | *" subscribe "* | *" pipe "*)
	if ! "$REAL_ZELLIJ" list-sessions --no-formatting 2>/dev/null | grep -v EXITED |
		awk '{print \$1}' | grep -qx -- "\${ZELLIJ_SESSION_NAME:-}"; then
		echo "zellij test shim: session '\${ZELLIJ_SESSION_NAME:-}' is not running; refusing: zellij \$*" >&2
		exit 1
	fi
	;;
esac
exec "$REAL_ZELLIJ" "\$@"
EOF
chmod +x "$AT/bin/zellij"

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

# Three fake agents: a and b in tab one, c in tab two. With WEAVER_TEST_ZJSTATUS=1
# (the zjstatus soak test), zjstatus with weaver's counts widget at the bottom
# of each tab; it needs zellij's permission, granted once by pressing y.
agent='echo FAKE AGENT; exec bash --noprofile --norc -i'
ZJSTATUS=${XDG_CONFIG_HOME:-$HOME/.config}/zellij/plugins/zjstatus.wasm
# A status bar in every tab, as in a real layout: a new thread's tab must keep it.
bar="default_tab_template {
        children
        pane size=1 borderless=true { plugin location=\"zellij:compact-bar\"; }
    }"
if [ -f "$ZJSTATUS" ] && [ "${WEAVER_TEST_ZJSTATUS:-0}" = 1 ]; then
	bar="default_tab_template {
        children
        pane size=1 borderless=true {
            plugin location=\"file:$ZJSTATUS\" {
                format_left \" {tabs}\"
                format_right \"{command_weaver}\"
                tab_normal \"[{index}] {name} \"
                tab_active \"[{index}] {name}* \"
                command_weaver_command \"weaver status --zjstatus\"
                command_weaver_format \"{stdout}\"
                command_weaver_interval \"${WEAVER_TEST_ZJSTATUS_INTERVAL:-2}\"
                command_weaver_rendermode \"dynamic\"
            }
        }
    }"
fi
cat >"$AT/layout.kdl" <<EOF
layout {
    $bar
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

up=0
for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
	if zellij list-sessions --no-formatting 2>/dev/null | grep -v EXITED | awk '{print $1}' | grep -qx -- "$SESSION"; then
		up=1
		break
	fi
	sleep 0.5
done
if [ $up = 0 ]; then
	echo "the test zellij session did not start; see $AT/screen.log" >&2
	exit 1
fi
sleep 1

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
