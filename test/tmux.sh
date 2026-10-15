#!/bin/bash
# weaver on tmux (WEAVER_MUX=tmux), on a throwaway tmux server of its own with
# fake agents: a thread's window (nvim on the left, the agent at its width),
# hooks keyed by tmux pane, Alt v, the board and the status counts, the server
# crashing and the thread resumed (also one lost from zellij), close and quit.
# Its own socket, state and PATH: never your tmux, zellij or agents.
#   test/tmux.sh
set -u
ROOT=$(cd "$(dirname "$0")/.." && pwd -P)
AT=$(mktemp -d /tmp/weaver-tmux.XXXXXX)
SOCK=weaver-test-$$
export XDG_STATE_HOME=$AT/state WEAVER_TMUX_SOCKET=$SOCK WEAVER_MUX=tmux
unset TMUX TMUX_PANE ZELLIJ ZELLIJ_SESSION_NAME ZELLIJ_PANE_ID WEAVER_TMUX_SESSION
SD=$XDG_STATE_HOME/weaver
mkdir -p "$AT/bin" "$SD"
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
trap 'tx kill-server >/dev/null 2>&1; rm -rf "$AT"' EXIT
# The weaver session as `weaver` starts it, detached, with a shell for home.
start() {
	tx -f "$ROOT/config/tmux.conf" new-session -d -s weaver -x 200 -y 50 -n home \
		-e WEAVER_TMUX_SESSION=weaver "bash --noprofile --norc" \; set-environment -t =weaver WEAVER_TMUX_SESSION weaver
	echo weaver >"$SD/tmux-weaver.layout"
}
# Inside the session: run-shell, as weaver's keys do.
in_tmux() { tx run-shell "$*" 2>&1; }
# hook PANE EVENT [PROMPT]: the agent in PANE reports, as Claude Code would.
hook() {
	local sp
	sp=$(tx display -p '#{socket_path}')
	jq -nc --arg e "$2" --arg c "$AT/repo" --arg p "${3:-}" '{session_id: "sid-1", transcript_path: "/dev/null",
		cwd: $c, hook_event_name: $e} + (if $e == "SessionStart" then {source: "startup"} else {} end)
		+ (if $p != "" then {prompt: $p} else {} end)' |
		CLAUDE_PID=$(tx display -p -t "%$1" '#{pane_pid}') TMUX="$sp,0,0" TMUX_PANE="%$1" WEAVER_TMUX_SESSION=weaver \
			weaver hook
}
panes() { tx list-panes -t "=weaver:$1" -F '#{pane_index} #{pane_id} #{pane_width} #{pane_start_command}'; }

start
# --- a thread: its window takes its number, nvim left, the agent at 44 columns
out=$(in_tmux "cd $AT/repo && weaver new --here 'say hello'")
sleep 1.5
NV=$(panes 1 | awk '$1 == 0 { print substr($2, 2) }')
AG=$(panes 1 | awk '$1 == 1 { print substr($2, 2) }')
ok "new: a window at its number, named for the task" "$(tx list-windows -F '#{window_index} #{window_name}' | tr '\n' ' ')" "0 home 1 say-hello "
ok "new: nvim on the left, the agent on the right at its least width" \
	"$(panes 1 | awk '{ printf "%s:%s ", $1, $3 }')|$(panes 1 | grep -c ' nvim --thread ')|$(panes 1 | grep -c ' _agent ')" "0:155 1:44 |1|1"
ok "new: the agent took the task" "$(cat "$AT/args.$AG" 2>/dev/null)" "say hello"
ok "new: the nvim is bound to the agent" "$(head -n 1 "$SD/tmux-weaver_$AG.nvim" 2>/dev/null)" "$NV"
ok "new: the nvim pane is named for its directory" "$(tx display -p -t "%$NV" '#{pane_title}')" "nvim: repo"

# --- hooks: the agent's state, keyed by its tmux pane
hook "$AG" SessionStart
hook "$AG" UserPromptSubmit "say hello"
sleep 1
ok "hook: state under the session's tmux key and the pane" \
	"$(jq -r '"\(.pane) \(.state) \(.turn) \(.nvim_pane)"' "$SD/tmux-weaver_$AG.json" 2>/dev/null)" "$AG working 1 $NV"
ok "board: the thread, working" "$(in_tmux 'LS_MODE=plain weaver _rows' | grep -c '^\[/\]  say hello  repo/main')" "1"
ok "status: the counts for tmux's status line" "$(weaver status --tmux weaver)" \
	"#[fg=#616161][#[fg=#999999]/#[fg=#616161]]#[fg=#999999] 1  "
ok "status: none for a session it does not know" "$(weaver status --tmux nope)" ""

# --- Alt v: nvim closed, then back on the agent's left at the split
tx kill-pane -t "%$NV"
sleep 0.5
in_tmux "weaver edit %$AG" >/dev/null
sleep 1.5
NV2=$(panes 1 | awk '$1 == 0 { print substr($2, 2) }')
ok "Alt v: nvim back on the left, bound, the agent at its width" \
	"$(panes 1 | awk '{ printf "%s:%s ", $1, $3 }')|$(head -n 1 "$SD/tmux-weaver_$AG.nvim")" "0:155 1:44 |$NV2"
tx select-pane -t "%$AG"
in_tmux "weaver edit %$AG" >/dev/null
sleep 0.5
ok "Alt v again: goes to that nvim, never a second" "$(panes 1 | wc -l | tr -d ' ')|$(tx display -p -t =weaver:1 '#{pane_id}')" "2|%$NV2"

# --- Alt y: home, and back
tx select-window -t =weaver:1
in_tmux "weaver go home"
ok "Alt y: home" "$(tx display -p -t =weaver: '#{window_name}')" "home"
in_tmux "weaver go home"
ok "Alt y again: back to the thread" "$(tx display -p -t =weaver: '#{window_name}')" "say-hello"

# --- the server crashes: the thread is kept, and comes back resumed
tx kill-server
sleep 0.5
start
in_tmux "weaver gc" >/dev/null
ok "crash: the thread is kept as lost" "$(ls "$SD/lost" 2>/dev/null)" "tmux-weaver_sid-1.json"
# One lost by the zellij session of the same name comes along.
jq -c '.session = "weaver" | .state.session_id = "sid-z" | .thread.num = 2 | .thread.name = "from-zellij"' \
	"$SD/lost/tmux-weaver_sid-1.json" >"$SD/lost/weaver_sid-z.json"
WEAVER_TMUX_SESSION=weaver WEAVER_RESTORE=1 in_tmux "weaver _restore" >/dev/null
sleep 1.5
ok "restart: each thread back in its window, by number" "$(tx list-windows -F '#{window_index} #{window_name}' | tr '\n' ' ')" \
	"0 home 1 say-hello 2 from-zellij "
AG=$(panes 1 | awk '$1 == 1 { print substr($2, 2) }')
ok "restart: its agent resumes the conversation, its turns carry on" \
	"$(tr '\n' ' ' <"$AT/args.$AG" 2>/dev/null)|$(jq -r '"\(.turn) \(.session_id)"' "$SD/tmux-weaver_$AG.json" 2>/dev/null)" "--resume sid-1 |1 sid-1"
ok "restart: nothing left lost" "$(ls "$SD/lost" 2>/dev/null | wc -l | tr -d ' ')" "0"

# --- close and quit
in_tmux "weaver close --yes $AG" >/dev/null
sleep 2
ok "close: its window goes, and its files" \
	"$(tx list-windows -F '#{window_name}' | tr '\n' ' ')|$(ls "$SD" | grep -c "^tmux-weaver_$AG\.")" "home from-zellij |0"
in_tmux "weaver quit --yes" >/dev/null
sleep 0.5
ok "quit: the session goes" "$(tx has-session -t =weaver 2>/dev/null && echo running || echo gone)" "gone"

echo "-- $pass passed, $fail failed"
exit $fail
