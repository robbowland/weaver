#!/bin/bash
# weaver on tmux, end to end: `weaver` starts its session on a throwaway tmux
# server of its own, in a pseudo-terminal as Ghostty would run it, and the
# tests press its keys there. Threads (nvim on the left, the agent at its
# width), hooks keyed by tmux pane, Alt y, the board's digits, Alt v, Alt 1,
# Cmd+D (as Ghostty sends it), Alt N, the server crashing and its threads resumed (by the board's
# enter, and by themselves), the board's x, and Ctrl q. Its own socket,
# session name, state, Claude Code dir, nvim config and PATH, with fake
# agents: never your tmux, zellij or agents.
#   test/tmux.sh
set -u
HERE=$(cd "$(dirname "$0")" && pwd -P)
ROOT=${HERE%/test}
AT=$(mktemp -d /tmp/weaver-tmux.XXXXXX)
SOCK=weaver-test-$$
S=wt$$
unset TMUX TMUX_PANE ZELLIJ ZELLIJ_SESSION_NAME ZELLIJ_PANE_ID WEAVER_TMUX_SESSION WEAVER_MUX WEAVER_RESTORE
export WEAVER_SESSION=$S XDG_STATE_HOME=$AT/state WEAVER_TMUX_SOCKET=$SOCK CLAUDE_CONFIG_DIR=$AT/claude
export WEAVER_FOLLOW_IDLE_MS=300 WORKTRUNK_CONFIG_PATH=$AT/worktrunk.toml WORKTRUNK_APPROVALS_PATH=$AT/worktrunk-approvals.toml
export _ZO_DATA_DIR=$AT/zoxide EDITOR=nvim VISUAL=nvim TERM=xterm-256color
[ -f "$HERE/nvim/init.lua" ] && [ "${WEAVER_TEST_USER_NVIM:-0}" != 1 ] && export VIMINIT="lua dofile('$HERE/nvim/init.lua')"
SD=$XDG_STATE_HOME/weaver
mkdir -p "$AT/bin" "$SD" "$AT/claude/sessions"
: >"$WORKTRUNK_CONFIG_PATH"
# A fake agent: notes its arguments, then waits as a shell.
cat >"$AT/bin/claude" <<EOF
#!/bin/bash
printf '%s\n' "\$@" >"$AT/args.\${TMUX_PANE#%}"
exec bash --noprofile --norc -i
EOF
chmod +x "$AT/bin/claude"
ln -s "$ROOT/weaver" "$AT/bin/weaver"
export PATH="$AT/bin:$PATH" WEAVER_CLAUDE="$AT/bin/claude"
git init -q -b main "$AT/repo" && git -C "$AT/repo" -c user.email=t@e -c user.name=t commit -q --allow-empty -m init

pass=0 fail=0
ok() {
	if [ "$2" = "$3" ]; then
		echo "PASS $1"
		pass=$((pass + 1))
	else
		echo "FAIL $1: got [$2] want [$3]"
		fail=$((fail + 1))
	fi
}
tx() { tmux -L "$SOCK" "$@"; }
# waitfor CONDITION [TENTHS]: until it holds, at most TENTHS tenths of a second.
waitfor() {
	local i
	for ((i = 0; i < ${2:-50}; i++)); do
		eval "$1" && return 0
		sleep 0.1
	done
	return 1
}
driver=""
cleanup() {
	tx kill-server >/dev/null 2>&1
	[ -n "$driver" ] && kill "$driver" 2>/dev/null
	rm -rf "$AT"
}
trap cleanup EXIT
trap 'exit 130' INT TERM HUP
# Keys go through a FIFO to the terminal (pty_drive.py), held open both ways so
# a key never blocks.
mkfifo "$AT/keys"
exec 3<>"$AT/keys"
key() { printf '%s\n' "$1" >&3; }
# `weaver` (with environment ARGS) in a 200 x 50 terminal: the session, attached.
attach() {
	python3 -I "$HERE/pty_drive.py" "$AT/keys" "$AT/screen.log" env "$@" weaver &
	driver=$!
	waitfor "tx has-session -t =$S 2>/dev/null" 100
	waitfor '[ -n "$(board)" ]' 100
	sleep 0.5
}
board() { ls /tmp/weaver-$(id -u)/tmux-$S.fzf.*.sock 2>/dev/null | head -n 1; }
windows() { tx list-windows -t "=$S" -F '#{window_index}' | tr '\n' ' '; }
where() { tx display -p -t "=$S:" '#{window_index} #{pane_id}'; }
panes() { tx list-panes -t "=$S:$1" -F '#{pane_index} #{pane_id} #{pane_width} #{pane_start_command}'; }
pane_at() { panes "$1" | awk -v i="$2" '$1 == i { print substr($2, 2) }'; }
# hookj PANE JSON: the agent in PANE reports, as Claude Code would.
hookj() {
	printf '%s' "$2" | CLAUDE_PID=$(tx display -p -t "%$1" '#{pane_pid}') TMUX="$(tx display -p '#{socket_path}'),0,0" \
		TMUX_PANE="%$1" WEAVER_TMUX_SESSION=$S weaver hook >/dev/null
}
# A tmux server numbers its panes from 0 again: the agents' notes of the last one go.
fresh_args() { rm -f "$AT"/args.*; }
# Have processes PID... all exited?
gone() {
	local p
	for p in "$@"; do kill -0 "$p" 2>/dev/null && return 1; done
	return 0
}
# crash: the server goes, and with it the client and (as they exit) the agents.
crash() {
	local pids
	pids=$(tx list-panes -a -F '#{pane_pid}')
	tx kill-server
	waitfor '! kill -0 "$driver" 2>/dev/null'
	waitfor 'gone $pids'
	fresh_args
}
# The board's agent rows.
board_rows() { curl -s -m 2 --unix-socket "$(board)" "http://localhost?limit=100" | jq '[.matches[].text | select(startswith("a"))] | length'; }
ev() { printf '{"session_id":"sid-1","transcript_path":"/dev/null","cwd":"%s","hook_event_name":"%s"%s}' "$AT/repo" "$1" "${2:-}"; }

# --- weaver: tmux by default, on its own server, home first with the board
attach
ok "weaver: tmux by default, its own server, home in window 0 with the board" \
	"$(tx list-windows -t "=$S" -F '#{window_index} #{window_name}')|$(panes 0 | grep -c ' home$')|$([ -S "$(board)" ] && echo board)" \
	"0 home|1|board"

# --- a thread, as Alt N's form makes it (weaver new, here)
tx run-shell -t "=$S:" "cd '$AT/repo' && weaver new --here 'say hello' >/dev/null 2>&1"
waitfor '[ "$(panes 1 | wc -l | tr -d " ")" = 2 ]'
NV=$(pane_at 1 0) AG=$(pane_at 1 1)
waitfor '[ -f "$AT/args.$AG" ] && [ -f "$SD/tmux-${S}_$AG.nvim" ]'
ok "new: window 1, nvim on the left, the agent on the right at its least width" \
	"$(windows)|$(panes 1 | awk '{ printf "%s:%s ", $1, $3 }')|$(panes 1 | grep -c ' nvim --thread ')|$(panes 1 | grep -c ' _agent ')" \
	"0 1 |0:155 1:44 |1|1"
ok "new: the agent took the task, and its nvim is bound to it" \
	"$(cat "$AT/args.$AG")|$(head -n 1 "$SD/tmux-${S}_$AG.nvim")" "say hello|$NV"

# --- hooks: the agent's state under the session's tmux key and its pane
hookj "$AG" "$(ev SessionStart ',"source":"startup"')"
hookj "$AG" "$(ev UserPromptSubmit ',"prompt":"say hello"')"
ok "hook: working, turn 1, its nvim" "$(jq -r '"\(.state) \(.turn) \(.nvim_pane)"' "$SD/tmux-${S}_$AG.json" 2>/dev/null)" "working 1 $NV"
waitfor '[ "$(tx display -p -t "=$S:1" "#{window_name}")" = "01 say hello · repo/main" ]'
ok "hook: the thread's window takes its label" "$(tx display -p -t "=$S:1" '#{window_name}')" "01 say hello · repo/main"
ok "status: the counts for tmux's status line" "$(weaver status --tmux "$S")" \
	"#[fg=#616161][#[fg=#999999]/#[fg=#616161]]#[fg=#999999] 1  "
ok "...set on the session for its status line to show, nothing polled" \
	"$(tx show -v -t "=$S:" @weaver-counts)|$(tx show -gv status-interval)" \
	"#[fg=#616161][#[fg=#999999]/#[fg=#616161]]#[fg=#999999] 1  |0"

# --- the keys
tx select-window -t "=$S:1" \; select-pane -t "%$AG"
key 1b79 # Alt y
waitfor '[ "$(where)" = "0 %$(pane_at 0 0)" ]'
ok "Alt y: home" "$(where | cut -d ' ' -f 1)" "0"
key 1b79
waitfor '[ "$(where)" = "1 %$AG" ]'
ok "Alt y again: back where you were" "$(where)" "1 %$AG"
key 1b79
waitfor '[ "$(where | cut -d " " -f 1)" = 0 ]'
key 31 # 1, on the board
waitfor '[ "$(where)" = "1 %$AG" ]'
ok "board: 1 goes to thread 1's agent" "$(where)" "1 %$AG"
key 1b76 # Alt v
waitfor '[ "$(where)" = "1 %$NV" ]'
ok "Alt v: to the agent's nvim" "$(where)" "1 %$NV"
tx kill-pane -t "%$NV"
tx select-pane -t "%$AG"
key 1b76
waitfor '[ "$(panes 1 | wc -l | tr -d " ")" = 2 ] && [ "$(head -n 1 "$SD/tmux-${S}_$AG.nvim")" = "$(pane_at 1 0)" ]'
NV=$(pane_at 1 0)
ok "Alt v: its nvim closed, back on the left, bound, the agent at its width" \
	"$(panes 1 | awk '{ printf "%s:%s ", $1, $3 }')|$(head -n 1 "$SD/tmux-${S}_$AG.nvim")" "0:155 1:44 |$NV"
hookj "$AG" "$(ev PermissionRequest ',"tool_name":"Bash","tool_input":{"command":"git push --force"}')"
key 1b79
waitfor '[ "$(where | cut -d " " -f 1)" = 0 ]'
key 1b31 # Alt 1
waitfor '[ "$(where)" = "1 %$AG" ]'
ok "Alt 1: to the agent that needs you" "$(where)" "1 %$AG"
key 1b5b3130303b397e # Cmd+D, as Ghostty sends it (csi:100;9~)
waitfor '[ "$(panes 1 | wc -l | tr -d " ")" = 3 ]'
ok "Cmd+D: splits the pane" "$(panes 1 | wc -l | tr -d ' ')" "3"
tx kill-pane -t "=$S:1.2"
# A narrower terminal attaches (tmux sizes windows to the latest client): the
# agent keeps its width, which tmux would take from it.
python3 -I - "$AT/keys" "$AT/screen2.log" <<'PY' &
import os, pty, struct, fcntl, termios, sys, time
pid, fd = pty.fork()
if pid == 0:
    fcntl.ioctl(0, termios.TIOCSWINSZ, struct.pack('HHHH', 35, 120, 0, 0))
    os.execvp('tmux', ['tmux', '-L', os.environ['WEAVER_TMUX_SOCKET'], 'attach', '-t', '=' + os.environ['WEAVER_SESSION']])
while True:
    try:
        if not os.read(fd, 65536):
            break
    except OSError:
        break
PY
narrow=$!
waitfor '[ "$(tx display -p -t "=$S:1" "#{window_width}")" = 120 ]'
waitfor '[ "$(panes 1 | awk "\$1 == 1 { print \$3 }")" = 44 ]'
ok "resize: a narrower window, the agent keeps its width" "$(panes 1 | awk '{ printf "%s:%s ", $1, $3 }')" "0:75 1:44 "
kill "$narrow" 2>/dev/null
waitfor '[ "$(tx display -p -t "=$S:1" "#{window_width}")" = 200 ]'
waitfor '[ "$(panes 1 | awk "\$1 == 1 { print \$3 }")" = 44 ]'
ok "...and back" "$(panes 1 | awk '{ printf "%s:%s ", $1, $3 }')" "0:155 1:44 "
n=$(grep -c 'what should the agent do' "$AT/screen.log")
key 1b4e # Alt N
waitfor '[ "$(grep -c "what should the agent do" "$AT/screen.log")" -gt "$n" ]'
ok "Alt N: the new-thread form, in a popup" "$([ "$(grep -c 'what should the agent do' "$AT/screen.log")" -gt "$n" ] && echo form)" "form"
key 1b # esc: the form goes, nothing made
sleep 1
ok "...esc: nothing made" "$(windows)" "0 1 "

# --- the server crashes: the board lists the lost thread, and enter resumes it
crash
attach WEAVER_RESTORE=0
ok "crash: the lost thread waits on the board (WEAVER_RESTORE=0)" "$(windows)|$(ls "$SD/lost")" "0 |tmux-${S}_sid-1.json"
ok "...and the status line no longer counts what the session that went ran" "$(weaver status --tmux "$S")" ""
key 0d # enter, on the lost thread
waitfor '[ "$(panes 1 | wc -l | tr -d " ")" = 2 ]'
AG=$(pane_at 1 1)
waitfor '[ -f "$AT/args.$AG" ]'
ok "board: enter resumes it in its window, its conversation resumed" \
	"$(windows)|$(tr '\n' ' ' <"$AT/args.$AG" 2>/dev/null)|$(ls "$SD/lost" | wc -l | tr -d ' ')" "0 1 |--resume sid-1 |0"
ok "...its turns carry on" "$(jq -r '"\(.turn) \(.session_id)"' "$SD/tmux-${S}_$AG.json")" "1 sid-1"
# Its agent reports as a resumed Claude Code does: its window takes its label
# (window ids start again with the server, so its old one counts for nothing).
hookj "$AG" "$(ev SessionStart ',"source":"resume"')"
waitfor '[ "$(tx display -p -t "=$S:1" "#{window_name}")" = "01 say hello · repo/main" ]'
ok "...and its window its label again" "$(tx display -p -t "=$S:1" '#{window_name}')" "01 say hello · repo/main"

# --- again, and the session brings it back by itself
crash
attach
waitfor '[ "$(panes 1 2>/dev/null | wc -l | tr -d " ")" = 2 ]'
AG=$(pane_at 1 1)
waitfor '[ -f "$AT/args.$AG" ]'
ok "restart: the thread comes back by itself, resumed, and you stay home" \
	"$(windows)|$(tr '\n' ' ' <"$AT/args.$AG" 2>/dev/null)|$(where | cut -d ' ' -f 1)" "0 1 |--resume sid-1 |0"

# --- the board's x closes it (y to confirm); Ctrl q quits
waitfor '[ "$(board_rows)" = 1 ]'
n=$(grep -c 'the agent, its nvim and the tab' "$AT/screen.log")
key 78 # x
waitfor '[ "$(grep -c "the agent, its nvim and the tab" "$AT/screen.log")" -gt "$n" ]'
key 79 # y
waitfor '[ "$(windows)" = "0 " ]'
ok "board: x closes the thread, its window and its files" "$(windows)|$(ls "$SD" | grep -c "^tmux-${S}_$AG\.")" "0 |0"
key 11 # Ctrl q
waitfor "! tx has-session -t =$S 2>/dev/null"
ok "Ctrl q: the session goes" "$(tx has-session -t "=$S" 2>/dev/null && echo running || echo gone)" "gone"

echo "-- $pass passed, $fail failed"
exit $fail
