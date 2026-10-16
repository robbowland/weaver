#!/bin/bash
# weaver on tmux, end to end: `weaver` starts its session on a throwaway tmux
# server of its own, in a pseudo-terminal as Ghostty would run it, and the
# tests press its keys there. Threads (nvim on the left, the agent at its
# width), hooks keyed by tmux pane, Alt y, the board's digits, Alt v, Alt 1,
# Cmd+D (as Ghostty sends it), Alt N, the server crashing and its threads resumed (by the board's
# enter, and by themselves), the board's x, Ctrl q, and threads from work
# items in the GITHUB and LINEAR views. Its own socket, session name, state,
# Claude Code dir, nvim config and PATH, with fake agents, gh, gh-dash and
# linear-tui: never your tmux, agents or accounts.
#   test/tmux.sh
set -u
HERE=$(cd "$(dirname "$0")" && pwd -P)
ROOT=${HERE%/test}
AT=$(mktemp -d /tmp/weaver-tmux.XXXXXX)
SOCK=$AT/tmux.sock
S=wt$$
unset TMUX TMUX_PANE ZELLIJ ZELLIJ_SESSION_NAME ZELLIJ_PANE_ID WEAVER_TMUX_SESSION WEAVER_RESTORE
export WEAVER_SESSION=$S XDG_STATE_HOME=$AT/state WEAVER_TMUX_SOCKET=$SOCK CLAUDE_CONFIG_DIR=$AT/claude
export WEAVER_FOLLOW_IDLE_MS=300 WORKTRUNK_CONFIG_PATH=$AT/worktrunk.toml WORKTRUNK_APPROVALS_PATH=$AT/worktrunk-approvals.toml
export _ZO_DATA_DIR=$AT/zoxide EDITOR=nvim VISUAL=nvim TERM=xterm-256color
# No views (gh-dash, linear-tui) but where a test asks for them.
export WEAVER_VIEWS=
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
# weaver: the binary built from this checkout, its files unpacked here.
(cd "$ROOT" && go build -o "$AT/bin/wvr" ./cmd/wvr) || exit 1
export XDG_DATA_HOME=$AT/data
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
		return 1
	fi
}
tx() { tmux -S "$SOCK" "$@"; }
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
	python3 -I "$HERE/pty_drive.py" "$AT/keys" "$AT/screen.log" env "$@" wvr &
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
# hookj PANE JSON [--async]: the agent in PANE reports, as Claude Code would.
hookj() {
	printf '%s' "$2" | CLAUDE_PID=$(tx display -p -t "%$1" '#{pane_pid}') TMUX="$(tx display -p '#{socket_path},#{pid}'),0" \
		TMUX_PANE="%$1" WEAVER_TMUX_SESSION=$S wvr hook ${3:-} >/dev/null
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
ok "weaver: the server runs from /, never a worktree that may go" \
	"$(lsof -a -p "$(tx display -p '#{pid}')" -d cwd -Fn 2>/dev/null | sed -n 's/^n//p')" "/"

# --- a thread, as Alt N's form makes it (wvr new, here)
tx run-shell -t "=$S:" "cd '$AT/repo' && wvr new --here 'say hello' >/dev/null 2>&1"
waitfor '[ "$(panes 1 | wc -l | tr -d " ")" = 2 ]'
NV=$(pane_at 1 0) AG=$(pane_at 1 1)
waitfor '[ -f "$AT/args.$AG" ] && [ -f "$SD/tmux-${S}_$AG.nvim" ]'
ok "new: window 1, nvim on the left, the agent on the right at its least width" \
	"$(windows)|$(panes 1 | awk '{ printf "%s:%s ", $1, $3 }')|$(panes 1 | grep -c ' nvim --fresh ')|$(panes 1 | grep -c ' _agent ')" \
	"0 1 |0:155 1:44 |1|1"
ok "new: the agent took the task, and its nvim is bound to it" \
	"$(tail -n 1 "$AT/args.$AG")|$(head -n 1 "$SD/tmux-${S}_$AG.nvim")" "say hello|$NV"
ok "new: on a session id weaver chose, kept in the thread record, named for the thread" \
	"$(sed -n '1p;3p;4p' "$AT/args.$AG" | tr '\n' ' ')|$(sed -n 2p "$AT/args.$AG")" \
	"--session-id -n say-hello |$(jq -r .session_id "$SD/tmux-${S}_$AG.thread")"

ok "new: its window has its label at once" "$(tx display -p -t "=$S:1" '#{window_name}')" "01 say hello · repo/main"

# --- hooks: the agent's state under the session's tmux key and its pane
hookj "$AG" "$(ev SessionStart ',"source":"startup"')"
hookj "$AG" "$(ev UserPromptSubmit ',"prompt":"say hello"')"
ok "hook: working, turn 1, its nvim" "$(jq -r '"\(.state) \(.turn) \(.nvim_pane)"' "$SD/tmux-${S}_$AG.json" 2>/dev/null)" "working 1 $NV"
# Async hooks land in any order: a tool event of a turn that has ended, late,
# leaves it done (even past the 3 s that cover events without a prompt id).
hookj "$AG" "$(ev Stop ',"prompt_id":"p1","last_assistant_message":"hello"')"
jq -c '.since -= 10' "$SD/tmux-${S}_$AG.json" >"$AT/st.json" && mv "$AT/st.json" "$SD/tmux-${S}_$AG.json"
hookj "$AG" "$(ev PostToolUse ',"prompt_id":"p1","tool_name":"Bash","tool_input":{"command":"ls"},"tool_response":{}')" --async
ok "hook: a late tool event of the turn that ended leaves it done" "$(jq -r .state "$SD/tmux-${S}_$AG.json")" "done"
hookj "$AG" "$(ev UserPromptSubmit ',"prompt_id":"p2","prompt":"go on"')"
ok "...and the next prompt is working again" "$(jq -r .state "$SD/tmux-${S}_$AG.json")" "working"
waitfor '[ "$(tx display -p -t "=$S:1" "#{window_name}")" = "01 say hello · repo/main" ]'
ok "hook: the thread's window takes its label" "$(tx display -p -t "=$S:1" '#{window_name}')" "01 say hello · repo/main"
waitfor '[ -n "$(wvr status --tmux "$S")" ]'
ok "status: the counts for tmux's status line" "$(wvr status --tmux "$S")" \
	"#[fg=#616161][#[fg=#999999]/#[fg=#616161]]#[fg=#999999] 1  "
ok "...set on the session for its status line to show, nothing polled" \
	"$(tx show -v -t "=$S:" @weaver-counts)|$(tx show -gv status-interval)" \
	"#[fg=#616161][#[fg=#999999]/#[fg=#616161]]#[fg=#999999] 1  |0"

# --- seen when you look: a thread done while you are elsewhere stays new until
# you stay on its window a moment (tmux's pane-focus-in hook)
tx select-window -t "=$S:0"
hookj "$AG" "$(ev Stop ',"prompt_id":"p2","last_assistant_message":"hello again"')"
waitfor '[ "$(tx show -wv -t "=$S:1" @weaver-unseen 2>/dev/null)" = 1 ]'
ok "seen: a thread done while you are elsewhere is flagged, and new" \
	"$(tx show -wv -t "=$S:1" @weaver-unseen 2>/dev/null)|$(jq -r .seen "$SD/tmux-${S}_$AG.json")" "1|false"
# (The Stop's own push done first: it counts what you look at as seen.)
waitfor 'case $(tx show -v -t "=$S:" @weaver-counts) in *39d97a*) true ;; *) false ;; esac'
tx select-window -t "=$S:1"
sleep 0.3
tx select-window -t "=$S:0"
sleep 2.5
ok "...passing through its window, it stays new" "$(jq -r .seen "$SD/tmux-${S}_$AG.json")" "false"
tx select-window -t "=$S:1"
waitfor '[ "$(jq -r .seen "$SD/tmux-${S}_$AG.json")" = true ] && [ -z "$(tx show -wv -t "=$S:1" @weaver-unseen 2>/dev/null)" ]' 60
ok "...staying on it, it is seen and the flag goes" \
	"$(jq -r .seen "$SD/tmux-${S}_$AG.json")|$(tx show -wv -t "=$S:1" @weaver-unseen 2>/dev/null)" "true|"

# --- review notes from its nvim reach its prompt (wvr send), as typed
tx run-shell -t "=$S:" "printf 'Review notes: look at line 3' | wvr send $AG >/dev/null 2>&1"
waitfor '[ "$(tx capture-pane -p -t "%$AG" | grep -c "Review notes: look at line 3")" -gt 0 ]'
ok "send: the notes at the agent's prompt, entered" \
	"$([ "$(tx capture-pane -p -t "%$AG" | grep -c 'Review notes: look at line 3')" -gt 0 ] && echo there)" "there"

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
tx run-shell -t "=$S:" "printf 'a dialog answer' | wvr send $AG >$AT/send.out 2>&1; echo \$? >>$AT/send.out"
sleep 0.5
ok "send: not while it waits on a dialog" \
	"$(tr '\n' '|' <"$AT/send.out")|$(tx capture-pane -p -t "%$AG" | grep -c 'a dialog answer')" \
	"weaver: the agent waits on a dialog: answer it first (or --force)|1||0"
key 1b79
waitfor '[ "$(where | cut -d " " -f 1)" = 0 ]'
n=$(grep -c 'weaver: \[1/1\] 01 say hello · needs you' "$AT/screen.log")
key 1b31 # Alt 1
waitfor '[ "$(where)" = "1 %$AG" ]'
ok "Alt 1: to the agent that needs you" "$(where)" "1 %$AG"
waitfor '[ "$(grep -c "weaver: \[1/1\] 01 say hello · needs you" "$AT/screen.log")" -gt "$n" ]'
ok "...saying where it landed" "$([ "$(grep -c 'weaver: \[1/1\] 01 say hello · needs you' "$AT/screen.log")" -gt "$n" ] && echo said)" "said"
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
    os.execvp('tmux', ['tmux', '-S', os.environ['WEAVER_TMUX_SOCKET'], 'attach', '-t', '=' + os.environ['WEAVER_SESSION']])
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
FORM=$(ls "$SD"/form.* 2>/dev/null | grep -v '\.' | head -n 1)
[ -n "$FORM" ] || FORM=$(ls "$SD"/form.* 2>/dev/null | head -n 1)
ok "form: its fields, from the focused thread's checkout" "$(sed -n '1p;4p;5p' "$FORM" 2>/dev/null | tr '\n' ' ')" "$(cd "$AT/repo" && pwd -P) worktree claude "
hdr=$(FZF_COLUMNS=96 wvr _form header "$FORM" "Fix the flaky test!" | sed 's/\x1b\[[0-9;]*m//g')
ok "form: the header names the repo, a new worktree, the branch from the task, its base and the agent" \
	"$(printf '%s\n' "$hdr" | awk '{ print $1 }' | tr '\n' ' ')|$(printf '%s\n' "$hdr" | grep -c 'branch  fix-the-flaky-test ')" \
	"repo where branch base agent |1"
key 09 # tab: this checkout
waitfor '[ "$(sed -n 4p "$FORM")" = here ]'
ok "form: tab works in this checkout" "$(sed -n 4p "$FORM")|$(FZF_COLUMNS=96 wvr _form header "$FORM" x | sed 's/\x1b\[[0-9;]*m//g' | awk '$1 == "where" { print $2, $3 }')" \
	"here|this checkout"
key 1b # esc: the form goes, nothing made
sleep 1
ok "...esc: nothing made" "$(windows)" "0 1 "

# --- a thread in a worktree of its own (its branch from the task), then
# closed with its worktree, which is clean and merged
tx run-shell -t "=$S:" "cd '$AT/repo' && wvr new --no-focus 'Fix the flaky test' >'$AT/new2.out' 2>&1"
waitfor '[ "$(panes 2 2>/dev/null | wc -l | tr -d " ")" = 2 ]' 100
AG2=$(pane_at 2 1)
waitfor '[ -f "$SD/tmux-${S}_$AG2.thread" ]'
WT2=$(jq -r .worktree "$SD/tmux-${S}_$AG2.thread" 2>/dev/null)
ok "new: a worktree for the task's branch, made by weaver, its window 2" \
	"$(jq -r '"\(.branch) \(.worktree_created) \(.here) \(.num)"' "$SD/tmux-${S}_$AG2.thread" 2>/dev/null)|$(git -C "$WT2" symbolic-ref --short HEAD 2>/dev/null)|$(windows)" \
	"fix-the-flaky-test true false 2|fix-the-flaky-test|0 1 2 " ||
	sed 's/^/  new: /' "$AT/new2.out"
tx run-shell -t "=$S:" "wvr close --yes --remove-worktree $AG2 >'$AT/close2.out' 2>&1"
waitfor '[ "$(windows)" = "0 1 " ] && [ ! -d "$WT2" ]' 100
ok "close: its window, its files and its worktree go; its branch stays" \
	"$(windows)|$(ls "$SD" | grep -c "^tmux-${S}_$AG2\.")|$([ -d "$WT2" ] && echo kept || echo gone)|$(git -C "$AT/repo" branch --list fix-the-flaky-test | tr -d ' *')" \
	"0 1 |0|gone|fix-the-flaky-test" ||
	sed 's/^/  close: /' "$AT/close2.out"

# --- the server crashes: the board lists the lost thread, and enter resumes it
crash
attach WEAVER_RESTORE=0
ok "crash: the lost thread waits on the board (WEAVER_RESTORE=0)" "$(windows)|$(ls "$SD/lost")" "0 |tmux-${S}_sid-1.json"
ok "...and the status line no longer counts what the session that went ran" "$(wvr status --tmux "$S")" ""
key 0d # enter, on the lost thread
waitfor '[ "$(panes 1 | wc -l | tr -d " ")" = 2 ]'
AG=$(pane_at 1 1)
waitfor '[ -f "$AT/args.$AG" ]'
ok "board: enter resumes it in its window, its conversation resumed" \
	"$(windows)|$(tr '\n' ' ' <"$AT/args.$AG" 2>/dev/null)|$(ls "$SD/lost" | wc -l | tr -d ' ')" "0 1 |--resume sid-1 |0"
ok "...its turns carry on" "$(jq -r '"\(.turn) \(.session_id)"' "$SD/tmux-${S}_$AG.json")" "2 sid-1" ||
	{ echo "  state: $(cat "$SD/tmux-${S}_$AG.json")"; tail -n 5 "$SD/log/sid-1.jsonl" 2>/dev/null | sed 's/^/  log: /'; }
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
	"$(windows)|$(tr '\n' ' ' <"$AT/args.$AG" 2>/dev/null)|$(where | cut -d ' ' -f 1)" "0 1 |--resume sid-1 |0" ||
	{ ls "$SD/lost" | sed 's/^/  lost: /'; tail -n 8 "$SD/hook.log" 2>/dev/null | sed 's/^/  log: /'; }

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

# --- a thread whose worktree has gone resumes from its repository (Claude Code
# finds the session in any of its worktrees)
waitfor '! kill -0 "$driver" 2>/dev/null'
fresh_args
mkdir -p "$SD/lost"
jq -n --arg skey "tmux-$S" --arg gone "$AT/gone" --arg repo "$AT/repo" --argjson now "$(date +%s)" \
	'{lost: $now, skey: $skey,
	  state: {pane: 9, agent: "claude", state: "ended", session_id: "sid-g", turn: 1, cwd: $gone, root: $gone, start_cwd: $gone},
	  thread: {num: 2, name: "gone-wt", task: "the old one", repo: $repo, worktree: $gone, agent: "claude", cmd: "claude"}}' \
	>"$SD/lost/tmux-${S}_sid-g.json"
attach
waitfor '[ "$(panes 2 2>/dev/null | wc -l | tr -d " ")" = 2 ]'
AG=$(pane_at 2 1)
waitfor '[ -f "$AT/args.$AG" ]'
ok "resume: its worktree gone, from its repository" \
	"$(windows)|$(tx display -p -t "%$AG" '#{pane_current_path}')|$(tr '\n' ' ' <"$AT/args.$AG" 2>/dev/null)" \
	"0 2 |$(cd "$AT/repo" && pwd -P)|--resume sid-g " ||
	{ tx list-windows -t "=$S" -F '#{window_index} #{window_name}' | sed 's/^/  window: /'; panes 1 | sed 's/^/  pane: /'; ls "$SD" "$SD/lost" | sed 's/^/  state: /'; }

# --- work items and views: a pull request's review and thread, an issue
# investigated, and gh-dash and linear-tui in the session's views. Fakes: gh
# answers for o/r, a repository here whose remote reads as github.com/o/r.
key 11 # Ctrl q
waitfor "! tx has-session -t =$S 2>/dev/null"
waitfor '! kill -0 "$driver" 2>/dev/null'
fresh_args
gitc() { git -C "$AT/r" -c user.email=t@e -c user.name=t "$@"; }
git init -q -b main "$AT/r" && gitc commit -q --allow-empty -m init
gitc checkout -q -b add-flag && echo flag >"$AT/r/flag" && gitc add flag && gitc commit -q -m flag && gitc checkout -q main
git clone -q --bare "$AT/r" "$AT/o-r.git" && gitc branch -q -D add-flag
gitc remote add origin https://github.com/o/r.git && gitc config url."$AT/o-r.git".insteadOf https://github.com/o/r.git
gitc fetch -q origin
cat >"$AT/bin/gh" <<'GH'
#!/bin/bash
case "$1 $2" in
"pr view") echo '{"number":45,"title":"Add a flag","body":"Adds --name.","url":"https://github.com/o/r/pull/45","author":{"login":"alice"},"state":"OPEN","headRefName":"add-flag","baseRefName":"main","isCrossRepository":false,"comments":[{"author":{"login":"carol"},"body":"Looks good"}]}' ;;
"api graphql") echo '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"isResolved":false,"isOutdated":false,"path":"flag","line":1,"comments":{"nodes":[{"author":{"login":"bob"},"body":"Name it --who"}]}},{"isResolved":true,"isOutdated":false,"path":"x","line":2,"comments":{"nodes":[{"author":{"login":"bob"},"body":"a resolved one"}]}}]}}}}}' ;;
"issue view") echo '{"number":12,"title":"Fix the flaky test","body":"It fails on Mondays.","url":"https://github.com/o/r/issues/12","author":{"login":"dan"},"state":"OPEN","comments":[]}' ;;
*) echo "gh (fake): $*" >&2; exit 1 ;;
esac
GH
# gh-dash: on Alt N or Alt R, the command its config binds for prs, on o/r#45.
cat >"$AT/bin/gh-dash" <<GHD
#!/bin/bash
run() {
	c=\$(awk -v k="key: \$1" '/^ *prs:/ { p = 1; next } /^ *issues:/ { p = 0 }
		p && index(\$0, k) { f = 1; next } p && f && /command:/ { sub(/^ *command: /, ""); print; exit }' "\$GH_DASH_CONFIG")
	c=\${c#\\'}; c=\${c%\\'}; c=\${c//\{\{.RepoName\}\}/o/r}; c=\${c//\{\{.PrNumber\}\}/45}
	echo "\$c" >>"$AT/ghdash.ran"
	sh -c "\$c"
}
stty -echo -icanon
while IFS= read -rsn1 ch; do
	[ "\$ch" = \$'\e' ] || continue
	IFS= read -rsn1 -t 1 d
	case \$d in N) run alt+N ;; R) run alt+R ;; esac
done
GHD
cat >"$AT/bin/linear-tui" <<'LT'
#!/bin/bash
case "$1" in
context) echo '{"workspace":"w","running":true,"snapshot":{"version":1,"screen":"issue_list","rows":[{"kind":"issue","identifier":"ENG-1","title":"a"},{"kind":"issue","identifier":"ENG-42","title":"Fix it"}],"selected_row":1}}' ;;
issue) echo '{"identifier":"ENG-42","title":"Fix it","url":"https://linear.app/acme/issue/ENG-42/fix-it","description":"Broken.","branch_name":"t/eng-42-fix-it","state":{"name":"Todo"},"creator":{"name":"Eve"},"comments":[]}' ;;
*) exec sleep 100000 ;;
esac
LT
chmod +x "$AT/bin/gh" "$AT/bin/gh-dash" "$AT/bin/linear-tui"
printf '%s\n' "$(cd "$AT/r" && pwd -P)" >"$SD/repos"
# The thread the last session lost stays out of this one.
rm -rf "$SD/lost" "$SD/tmux-${S}_"*
attach WEAVER_VIEWS="github linear"
waitfor '[ "$(windows)" = "0 900 901 " ]'
ok "views: GITHUB and LINEAR beside home, out of the thread numbers" \
	"$(tx list-windows -t "=$S" -F '#{window_index}:#{window_name}:#{@weaver-view}' | tr '\n' ' ')" "0:home: 900:GITHUB:github 901:LINEAR:linear "
ok "...gh-dash's config: weaver's keys and colours" "$(grep -c '_from github' "$SD/gh-dash.yml")|$(grep -c '#39d97a' "$SD/gh-dash.yml")" "4|1"
ok "...the strip of them on home, home in ink" "$(tx display -p -t "=$S:0" '#{T:status-left}')" \
	" #[fg=#ffffff]THREADS   #[fg=#616161]GITHUB   #[fg=#616161]LINEAR"

# A review of the pull request: on its head, read-only, its nvim on the review.
tx run-shell -t "=$S:" "cd '$AT/r' && wvr new --no-focus --review --pr 45 'Review #45' >$AT/new.out 2>&1"
waitfor '[ "$(windows)" = "0 1 900 901 " ]'
AG=$(pane_at 1 1)
waitfor '[ -f "$AT/args.$AG" ]'
WT=$(tx display -p -t "%$AG" '#{pane_current_path}')
ok "pr --review: a thread on its branch, the agent in plan mode" \
	"$(git -C "$WT" symbolic-ref --short HEAD)|$(sed -n '1,2p' "$AT/args.$AG" | tr '\n' ' ')" "add-flag|--permission-mode plan "
ok "...its prompt: the task, read-only, the open review comment fenced, not the resolved one" \
	"$(grep -c '^Review #45$' "$AT/args.$AG")|$(grep -c 'change no files' "$AT/args.$AG")|$(grep -c '^bob: Name it --who' "$AT/args.$AG")|$(grep -c 'a resolved one' "$AT/args.$AG")|$(grep -c '^===== BEGIN UNTRUSTED GITHUB' "$AT/args.$AG")" \
	"1|1|1|0|1"
ok "...its record and its base for reviews" "$(jq -r '"\(.item.ref) \(.mode)"' "$SD/tmux-${S}_$AG.thread")|$(git -C "$AT/r" config branch.add-flag.weaverbase)" \
	"o/r#45 review|origin/main"
NS=/tmp/weaver-$(id -u)/tmux-${S}_$AG.sock
waitfor '[ "$(nvim --server "$NS" --remote-expr "tabpagenr(\"\$\")" 2>/dev/null)" = 2 ]' 100
ok "...its nvim opens on the review" "$(nvim --server "$NS" --remote-expr 'tabpagenr("$")' 2>/dev/null)" "2"
tx run-shell -t "=$S:" "cd '$AT/r' && wvr new --pr 45 'again' >$AT/new.out 2>&1"
ok "pr: its branch already has a thread: to it" "$(cat "$AT/new.out")|$(windows)" \
	"weaver: o/r#45 already has a thread (add-flag): went to it|0 1 900 901 "

# An issue investigated: its own branch, plan mode.
tx run-shell -t "=$S:" "cd '$AT/r' && wvr new --no-focus --review --issue o/r#12 'Look into #12' >$AT/new.out 2>&1"
waitfor '[ "$(windows)" = "0 1 2 900 901 " ]'
AG2=$(pane_at 2 1)
waitfor '[ -f "$AT/args.$AG2" ]'
ok "issue --review: N-title branch, plan mode, investigate" \
	"$(git -C "$(tx display -p -t "%$AG2" '#{pane_current_path}')" symbolic-ref --short HEAD)|$(sed -n 2p "$AT/args.$AG2")|$(grep -c 'without changing any files' "$AT/args.$AG2")" \
	"12-fix-the-flaky-test|plan|1"

# Alt y: a thread, home, each view, and back; Alt j and Alt k pass the views.
tx select-window -t "=$S:2"
seq=""
for i in 1 2 3 4; do
	key 1b79
	sleep 0.4
	seq="$seq$(where | cut -d ' ' -f 1) "
done
ok "Alt y: home, GITHUB, LINEAR, back to the thread" "$seq" "0 900 901 2 "
key 1b6a # Alt j, from the last thread
waitfor '[ "$(where | cut -d " " -f 1)" = 0 ]'
ok "Alt j: past the views, round to home" "$(where | cut -d ' ' -f 1)" "0"
key 1b6b # Alt k
waitfor '[ "$(where | cut -d " " -f 1)" = 2 ]'
ok "Alt k: back past them" "$(where | cut -d ' ' -f 1)" "2"

# gh-dash's Alt N on the pull request: weaver's command, then its thread.
tx select-window -t "=$S:900"
key 1b4e # Alt N
waitfor '[ "$(where | cut -d " " -f 1)" = 1 ]'
ok "GITHUB: Alt N runs weaver's command for the row, and goes to its thread" \
	"$(tail -n 1 "$AT/ghdash.ran" 2>/dev/null | sed 's/^.* _from/_from/')|$(where | cut -d ' ' -f 1)" "_from github pr o/r 45|1"

# linear-tui's Alt N: the form, for the issue under its cursor.
tx select-window -t "=$S:901"
key 1b4e
waitfor '[ -n "$(ls "$SD" | grep -E "^form\.[^.]+\.item$")" ]'
FORM=$SD/$(ls "$SD" | grep -E '^form\.[^.]+$' | head -n 1)
hdr=$(FZF_COLUMNS=96 wvr _form header "$FORM" x | sed 's/\x1b\[[0-9;]*m//g')
ok "LINEAR: Alt N, the form for the issue under the cursor" \
	"$(sed -n '1p;3p;8p' "$FORM" | tr '\n' ' ')|$(printf '%s\n' "$hdr" | awk '{ print $1 }' | tr '\n' ' ')" \
	"$(cd "$AT/r" && pwd -P) t/eng-42-fix-it ENG-42 |from mode repo where branch base agent "
waitfor '[ "$(grep -c "Implement ENG-42" "$AT/screen.log")" -gt 0 ]'
key 0d # enter
waitfor '[ "$(windows)" = "0 1 2 3 900 901 " ]'
AG3=$(pane_at 3 1)
waitfor '[ -f "$AT/args.$AG3" ]'
ok "...enter: its thread, on Linear's branch" \
	"$(git -C "$(tx display -p -t "%$AG3" '#{pane_current_path}')" symbolic-ref --short HEAD)|$(grep -c '^Implement ENG-42$' "$AT/args.$AG3")|$(grep -c '^Linear issue ENG-42 by Eve' "$AT/args.$AG3")" \
	"t/eng-42-fix-it|1|1"

echo "-- $pass passed, $fail failed"
exit $fail
