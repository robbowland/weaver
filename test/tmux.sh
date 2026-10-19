#!/bin/bash
# weaver on tmux, end to end: `weaver` starts its session on a throwaway tmux
# server of its own, in a pseudo-terminal as Ghostty would run it, and the
# tests press its keys there. Threads (nvim on the left, the agent at its
# width), hooks keyed by tmux pane, Alt y, the board's digits, Alt v, Alt 1,
# Cmd+D (as Ghostty sends it), Alt N, the mouse (a hint's click, the status
# line's buttons, right-click menus), the server crashing and its threads
# resumed (by the board's enter, and by themselves), the board's x, Ctrl q,
# agents and conversations adopted as threads, and threads from work items
# in the GITHUB and LINEAR views. Its own socket,
# session name, state, Claude Code dir, nvim config and PATH, with fake
# agents, gh, Keychain and Linear API: never your tmux, agents, keys or
# accounts.
#   test/tmux.sh
set -u
HERE=$(cd "$(dirname "$0")" && pwd -P)
ROOT=${HERE%/test}
AT=$(mktemp -d /tmp/weaver-tmux.XXXXXX)
# Gone however the run ends (cleanup, below, takes over once there is a
# server to end).
trap 'rm -rf "$AT"' EXIT
SOCK=$AT/tmux.sock
S=wt$$
unset TMUX TMUX_PANE ZELLIJ ZELLIJ_SESSION_NAME ZELLIJ_PANE_ID WEAVER_TMUX_SESSION WEAVER_RESTORE LINEAR_API_KEY
export WEAVER_SESSION=$S XDG_STATE_HOME=$AT/state WEAVER_TMUX_SOCKET=$SOCK CLAUDE_CONFIG_DIR=$AT/claude
export WEAVER_FOLLOW_IDLE_MS=300 WORKTRUNK_CONFIG_PATH=$AT/worktrunk.toml WORKTRUNK_APPROVALS_PATH=$AT/worktrunk-approvals.toml
export _ZO_DATA_DIR=$AT/zoxide EDITOR=nvim VISUAL=nvim TERM=xterm-256color
# No views (GITHUB, LINEAR) but where a test asks for them.
export WEAVER_VIEWS=
# wvr sets itself up as it starts: not here (it would fetch plugins).
export WEAVER_SELF_SETUP=0
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
linear=""
cleanup() {
	tx kill-server >/dev/null 2>&1
	[ -n "$linear" ] && kill "$linear" 2>/dev/null
	[ -n "$driver" ] && kill "$driver" 2>/dev/null
	pkill -f -- "--listen /tmp/weaver-$(id -u)/tmux-${S}_" 2>/dev/null
	rm -f /tmp/weaver-$(id -u)/tmux-${S}[._]*
	rm -rf "$AT"
}
trap cleanup EXIT
trap 'exit 130' INT TERM HUP
# Keys go through a FIFO to the terminal (pty_drive.py), held open both ways so
# a key never blocks.
mkfifo "$AT/keys"
exec 3<>"$AT/keys"
key() { printf '%s\n' "$1" >&3; }
# click X Y [BUTTON]: a press and release at column X, line Y (from 1), as the
# terminal reports them (SGR); hover X Y: the pointer moving there.
sgr() { printf '\033[<%s;%s;%s%s' "$1" "$2" "$3" "$4" | xxd -p | tr -d '\n'; }
click() {
	key "$(sgr "${3:-0}" "$1" "$2" M)"
	sleep 0.1
	key "$(sgr "${3:-0}" "$1" "$2" m)"
}
hover() { key "$(sgr 35 "$1" "$2" M)"; }
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
ok "status: the counts for tmux's status line, working a button home" "$(wvr status --tmux "$S")" \
	"#[range=user|home]#[fg=#616161][#[fg=#999999]/#[fg=#616161]]#[fg=#999999] 1#[norange]  "
ok "...set on the session for its status line to show, nothing polled" \
	"$(tx show -v -t "=$S:" @weaver-counts)|$(tx show -gv status-interval)" \
	"#[range=user|home]#[fg=#616161][#[fg=#999999]/#[fg=#616161]]#[fg=#999999] 1#[norange]  |0"

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
ok "form: its fields, from the focused thread's checkout" "$(jq -r '"\(.repo) \(.where) \(.agent)"' "$FORM" 2>/dev/null)" "$(cd "$AT/repo" && pwd -P) worktree claude"
hdr=$(wvr _form fields "$FORM" "Fix the flaky test!")
ok "form: the header names the repo, a new worktree, the branch from the task, its base and the agent" \
	"$(printf '%s\n' "$hdr" | awk '{ print $1 }' | tr '\n' ' ')|$(printf '%s\n' "$hdr" | grep -c 'branch  fix-the-flaky-test$')" \
	"repo where branch base agent |1"
key 1b77 # alt-w: where it works, picked
waitfor '[ "$(grep -c "a checkout that is there" "$AT/screen.log")" -gt 0 ]'
key 6d61696e # main
sleep 0.3
key 0d
waitfor '[ "$(jq -r .where "$FORM")" = here ]'
ok "form: alt-w picks where it works: here, the main checkout" "$(jq -r .where "$FORM")|$(wvr _form fields "$FORM" x | awk '$1 == "where" { print $2 }')" \
	"here|main"
ok "...and its rows stay (branch and base say why they do not apply), so none moves" \
	"$(wvr _form fields "$FORM" x | awk '{ print $1 }' | tr '\n' ' ')" "repo where branch base agent "
key 1b # esc: the form goes, nothing made
sleep 1
ok "...esc: nothing made" "$(windows)" "0 1 "

# --- the mouse: a key's hint is its button, the status line's counts go to
# their agents, and a right click opens the menu of what is under it
tx select-window -t "=$S:0"
waitfor '[ "$(where | cut -d " " -f 1)" = 0 ]'
foot=$(tx capture-pane -p -t "=$S:0" | grep -n 'n NEW' | head -n 1)
n=$(grep -c 'what should the agent do' "$AT/screen.log")
click "$(python3 -I -c 'import sys; print(sys.argv[1].index("n NEW") + 3)' "${foot#*:}")" "${foot%%:*}"
waitfor '[ "$(grep -c "what should the agent do" "$AT/screen.log")" -gt "$n" ]'
ok "mouse: a click on the board's n NEW, as n" "$([ "$(grep -c 'what should the agent do' "$AT/screen.log")" -gt "$n" ] && echo form)" "form"
key 1b
# (The form is a popup over the board: gone once its process is.)
waitfor '! pgrep -f "$AT/bin/wvr new" >/dev/null'
click 189 50 # the status line's [!] 1
waitfor '[ "$(where)" = "1 %$AG" ]'
ok "...one on the status line's count of agents that need you, to it" "$(where)" "1 %$AG"
n=$(grep -c 'close thread' "$AT/screen.log")
click 190 10 2 # the agent's pane: a shell, which takes no mouse
waitfor '[ "$(grep -c "close thread" "$AT/screen.log")" -gt "$n" ]'
ok "...a right click on the agent, its thread's menu, not tmux's" "$([ "$(grep -c 'close thread' "$AT/screen.log")" -gt "$n" ] && echo menu)" "menu"
key 1b # esc: the menu goes (before the window changes, which would close it, and the board would take the esc)
sleep 0.5
tx select-window -t "=$S:0"
waitfor '[ "$(where | cut -d " " -f 1)" = 0 ]'
row=$(tx capture-pane -p -t "=$S:0" | grep -n ' 01 ' | head -n 1 | cut -d : -f 1)
n=$(grep -c 'jump to it' "$AT/screen.log")
click 30 "$row" 2
waitfor '[ "$(grep -c "jump to it" "$AT/screen.log")" -gt "$n" ]'
ok "...one on a board row, its menu where the click was" \
	"$([ "$(grep -c 'jump to it' "$AT/screen.log")" -gt "$n" ] && echo menu)|$(tx show -pv -t "=$S:0" @weaver-mouse | cut -d ' ' -f 2-)" "menu|29 $((row - 1))"
hover 33 $((row + 3)) # nvim, its second item
click 33 $((row + 3))
waitfor '[ "$(where)" = "1 %$NV" ]'
ok "...whose items are the row's keys: nvim" "$(where)" "1 %$NV"
tx select-pane -t "%$AG"

# --- gc: an nvim its server left behind (its parent gone) ends
(nvim --headless -u NONE --listen "/tmp/weaver-$(id -u)/tmux-${S}_98.sock" </dev/null >/dev/null 2>&1 & echo $! >"$AT/orphan")
waitfor '[ -S "/tmp/weaver-$(id -u)/tmux-${S}_98.sock" ]'
wvr gc >/dev/null 2>&1
waitfor '! kill -0 "$(cat "$AT/orphan")" 2>/dev/null'
ok "gc: an nvim its tmux server left behind ends; the threads' stay" \
	"$(kill -0 "$(cat "$AT/orphan")" 2>/dev/null && echo running || echo ended)|$(tx display -p -t "%$NV" '#{pane_current_command}')" "ended|nvim"

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

# --- Ctrl q asks while an agent is mid-turn; the board's x closes it (y to
# confirm); Ctrl q quits
waitfor '[ "$(board_rows)" = 1 ]'
hookj "$AG" "$(ev UserPromptSubmit ',"prompt_id":"pq","prompt":"go on"')"
waitfor '[ "$(tx display -p -t "=$S:" "#{@weaver-busy}")" = 1 ]'
n=$(grep -c 'an agent is mid-turn' "$AT/screen.log")
key 11 # Ctrl q
waitfor '[ "$(grep -c "an agent is mid-turn" "$AT/screen.log")" -gt "$n" ]'
key 6e # n
sleep 0.5
ok "Ctrl q mid-turn: asks first, and n keeps the session" \
	"$([ "$(grep -c 'an agent is mid-turn' "$AT/screen.log")" -gt "$n" ] && echo asked)|$(tx has-session -t "=$S" 2>/dev/null && echo running || echo gone)" "asked|running"
n=$(grep -c 'the agent, its nvim and its window' "$AT/screen.log")
key 78 # x
waitfor '[ "$(grep -c "the agent, its nvim and its window" "$AT/screen.log")" -gt "$n" ]'
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

# --- transitions: home from a thread has the board's cursor on it, and
# closing the thread you are in lands home, whatever tmux showed before
tx run-shell -t "=$S:" "cd '$AT/repo' && wvr new --here --no-focus 'one more' >/dev/null 2>&1"
waitfor '[ "$(windows)" = "0 1 2 " ]'
AG1=$(pane_at 1 1)
cursor() { curl -s -m 2 --unix-socket "$(board)" http://localhost | jq -r .current.text | cut -f 1; }
for w in 1 2; do
	tx select-window -t "=$S:$w"
	waitfor '[ "$(where | cut -d " " -f 1)" = '"$w"' ]'
	key 1b79 # Alt y
	waitfor '[ "$(where | cut -d " " -f 1)" = 0 ]'
	a=$AG
	[ $w = 1 ] && a=$AG1
	waitfor '[ "$(cursor)" = "a'"$a"'" ]'
	ok "transitions: Alt y from thread $w, the board's cursor on its row" "$(cursor)" "a$a"
done
tx select-window -t "=$S:2"
waitfor '[ "$(where | cut -d " " -f 1)" = 2 ]'
tx run-shell -b -t "%$AG" "wvr close --yes $AG >/dev/null 2>&1"
waitfor '[ "$(windows)" = "0 1 " ]'
ok "...closing the thread you are in: home, not the window tmux showed before" "$(where | cut -d ' ' -f 1)" "0"
tx run-shell -b -t "%$AG1" "wvr close --yes $AG1 >/dev/null 2>&1"
waitfor '[ "$(windows)" = "0 " ]'

# --- adopt: an agent you started by hand (here, beside home's board) becomes
# a thread where it runs: a window of its own at its number, an nvim beside
# it, never restarted; kept as threads are. A conversation that runs nowhere
# is resumed as one, in the directory it started in.
HA=$(tx split-window -d -P -F '#{pane_id}' -t "=$S:0" -c "$AT/repo" "$AT/bin/claude" | tr -d %)
waitfor '[ -f "$AT/args.$HA" ]'
hookj "$HA" "$(printf '{"session_id":"sid-h","transcript_path":"/dev/null","cwd":"%s","hook_event_name":"SessionStart","source":"startup"}' "$AT/repo")"
pid=$(tx display -p -t "%$HA" '#{pane_pid}')
tx run-shell -t "=$S:" "wvr adopt $HA >'$AT/adopt.out' 2>&1"
waitfor '[ "$(panes 1 2>/dev/null | wc -l | tr -d " ")" = 2 ]'
ok "adopt: out of home into window 1, an nvim on its left, the agent never restarted" \
	"$(windows)|$(panes 0 | wc -l | tr -d ' ')|$(pane_at 1 1)|$(tx display -p -t "%$HA" '#{pane_pid}')" "0 1 |1|$HA|$pid" ||
	sed 's/^/  adopt: /' "$AT/adopt.out"
ok "...its record: thread 1, in the checkout it runs in, its conversation known" \
	"$(jq -r '"\(.num) \(.here) \(.worktree_created) \(.session_id) \(.pane)"' "$SD/tmux-${S}_$HA.thread" 2>/dev/null)" "1 true false sid-h $HA"
tx run-shell -t "=$S:" "wvr adopt $HA >'$AT/adopt.out' 2>&1"
ok "...adopted again: it says it is a thread already" "$(grep -c 'is thread 1 already' "$AT/adopt.out")" "1"
crash
attach
waitfor '[ "$(panes 1 2>/dev/null | wc -l | tr -d " ")" = 2 ]'
AG=$(pane_at 1 1)
waitfor '[ -f "$AT/args.$AG" ]'
ok "...the session goes: it comes back as threads do, its conversation resumed" \
	"$(windows)|$(tr '\n' ' ' <"$AT/args.$AG" 2>/dev/null)" "0 1 |--resume sid-h "
CID=0f0f0f0f-1111-4222-8333-444444444444
mkdir -p "$AT/claude/projects/-repo"
printf '%s\n' '{"type":"user","cwd":"'"$AT/repo"'","gitBranch":"main","message":{"role":"user","content":"tidy the parser"}}' \
	'{"type":"ai-title","aiTitle":"Tidy the parser"}' >"$AT/claude/projects/-repo/$CID.jsonl"
tx run-shell -t "=$S:" "wvr adopt 0f0f0f0f >'$AT/adopt.out' 2>&1"
waitfor '[ "$(panes 2 2>/dev/null | wc -l | tr -d " ")" = 2 ]'
AG2=$(pane_at 2 1)
waitfor '[ -f "$AT/args.$AG2" ]'
ok "adopt: a conversation that runs nowhere, resumed as thread 2 where it started" \
	"$(windows)|$(tr '\n' ' ' <"$AT/args.$AG2" 2>/dev/null)|$(tx display -p -t "%$AG2" '#{pane_current_path}')|$(jq -r '"\(.num) \(.name)"' "$SD/tmux-${S}_$AG2.thread" 2>/dev/null)" \
	"0 1 2 |--resume $CID |$(cd "$AT/repo" && pwd -P)|2 tidy-the-parser" ||
	sed 's/^/  adopt: /' "$AT/adopt.out"
for a in "$AG" "$AG2"; do tx run-shell -t "=$S:" "wvr close --yes $a >/dev/null 2>&1"; done
waitfor '[ "$(windows)" = "0 " ]'

# --- work items and views: a pull request's review and thread, an issue
# investigated, and the GITHUB and LINEAR views. Fakes: gh answers for o/r,
# a repository here whose remote reads as github.com/o/r, and its searches
# find #45 (review requested) and #12 (assigned).
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
"api graphql")
	case "$*" in
	*'review-requested:@me'*) echo '{"data":{"s":{"issueCount":1,"nodes":[{"number":45,"title":"Add a flag","body":"Adds --name.","url":"https://github.com/o/r/pull/45","state":"OPEN","isDraft":false,"author":{"login":"alice"},"updatedAt":"2026-10-16T10:00:00Z","repository":{"nameWithOwner":"o/r"},"headRefName":"add-flag","baseRefName":"main","isCrossRepository":false,"reviewDecision":"REVIEW_REQUIRED","mergeable":"MERGEABLE","additions":1,"deletions":0,"changedFiles":1,"commits":{"totalCount":1},"comments":{"totalCount":1},"labels":{"nodes":[]},"assignees":{"nodes":[]},"reviewRequests":{"nodes":[]},"last":{"nodes":[]}}]}}}'; exit ;;
	*'is:issue archived:false is:open assignee:@me'*) echo '{"data":{"s":{"issueCount":1,"nodes":[{"number":12,"title":"Fix the flaky test","body":"It fails on Mondays.","url":"https://github.com/o/r/issues/12","state":"OPEN","author":{"login":"dan"},"updatedAt":"2026-10-16T10:00:00Z","repository":{"nameWithOwner":"o/r"},"comments":{"totalCount":0},"labels":{"nodes":[]},"assignees":{"nodes":[]}}]}}}'; exit ;;
	*'search(type: ISSUE'*) echo '{"data":{"s":{"issueCount":0,"nodes":[]}}}'; exit ;;
	esac
	echo '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"isResolved":false,"isOutdated":false,"path":"flag","line":1,"comments":{"nodes":[{"author":{"login":"bob"},"body":"Name it --who"}]}},{"isResolved":true,"isOutdated":false,"path":"x","line":2,"comments":{"nodes":[{"author":{"login":"bob"},"body":"a resolved one"}]}}]}}}}}' ;;
"issue view") echo '{"number":12,"title":"Fix the flaky test","body":"It fails on Mondays.","url":"https://github.com/o/r/issues/12","author":{"login":"dan"},"state":"OPEN","comments":[]}' ;;
"auth token") echo "gho_fake" ;;
*) echo "gh (fake): $*" >&2; exit 1 ;;
esac
GH
# The Keychain: weaver's Linear key in $AT/linear-key.
printf '#!/bin/bash\nK=%q\n' "$AT/linear-key" >"$AT/bin/security"
cat >>"$AT/bin/security" <<'SEC'
case "$1" in
find-generic-password) [ -f "$K" ] && cat "$K" && exit 0; exit 44 ;;
-i) read -r l; set -- $l; while [ $# -gt 0 ]; do [ "$1" = -w ] && printf '%s\n' "$2" >"$K"; shift; done ;;
esac
SEC
echo lin_fake >"$AT/linear-key"
# Linear's API: ENG-42 assigned to you.
python3 -I "$HERE/fake_linear.py" "$AT/linear-port" &
linear=$!
waitfor '[ -s "$AT/linear-port" ]'
export WEAVER_LINEAR_URL=http://127.0.0.1:$(cat "$AT/linear-port")/graphql
chmod +x "$AT/bin/gh" "$AT/bin/security"
printf '%s\n' "$(cd "$AT/r" && pwd -P)" >"$SD/repos"
# The thread the last session lost stays out of this one.
rm -rf "$SD/lost" "$SD/tmux-${S}_"*
attach WEAVER_VIEWS="github linear"
waitfor '[ "$(windows)" = "0 900 901 " ]'
ok "views: GITHUB and LINEAR beside home, out of the thread numbers" \
	"$(tx list-windows -t "=$S" -F '#{window_index}:#{window_name}:#{@weaver-view}' | tr '\n' ' ')" "0:home: 900:GITHUB:github 901:LINEAR:linear "
waitfor 'tx capture-pane -p -t "=$S:900" | grep -q "Add a flag"'
ok "GITHUB: weaver's own, what GitHub finds for you, a section under its heading" \
	"$(tx capture-pane -p -t "=$S:900" | grep -c 'QUEUE · 1')|$(tx capture-pane -p -t "=$S:900" | grep -c 'Add a flag')|$(tx capture-pane -p -t "=$S:900" | grep -c 'none yet: ⏎ starts one on add-flag')" "1|2|1" ||
	{ tx capture-pane -p -t "=$S:900" | sed 's/^/  github: /' | head -20; tx display -p -t "=$S:900" '#{pane_start_command} #{pane_dead}' | sed 's/^/  pane: /'; }
ok "...the strip of them on home, home in ink" "$(tx display -p -t "=$S:0" '#{T:status-left}')" \
	" #[range=user|home]#[fg=#ffffff]THREADS#[norange]   #[range=user|github]#[fg=#616161]GITHUB#[norange]   #[range=user|linear]#[fg=#616161]LINEAR#[norange]"
click 14 50 # GITHUB, on the status line
waitfor '[ "$(where | cut -d " " -f 1)" = 900 ]'
ok "...each a button: GITHUB" "$(where | cut -d ' ' -f 1)" "900"
click 3 50
waitfor '[ "$(where | cut -d " " -f 1)" = 0 ]'
ok "...THREADS: home" "$(where | cut -d ' ' -f 1)" "0"
seen=""
for k in 09 09 09 1b5b5a; do # tab, tab, tab, shift-tab
	key $k
	sleep 0.8
	seen="$seen$(where | cut -d ' ' -f 1) "
done
ok "tab: the next view, round as a tab bar goes; shift-tab the previous" "$seen" "900 901 0 901 "
key 09
waitfor '[ "$(where | cut -d " " -f 1)" = 0 ]'


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
key 1b79 # Alt y: home
waitfor '[ "$(where | cut -d " " -f 1)" = 0 ]'
sleep 0.5
key 71 # q
waitfor '[ "$(where | cut -d " " -f 1)" = 2 ]'
ok "home's q: back to the thread, not on to the views" "$(where | cut -d ' ' -f 1)" "2"
key 1b79
waitfor '[ "$(where | cut -d " " -f 1)" = 0 ]'
key 1b79
waitfor '[ "$(where | cut -d " " -f 1)" = 900 ]'
sleep 0.5
key 1b # esc
waitfor '[ "$(where | cut -d " " -f 1)" = 2 ]'
ok "a view's esc: back to the thread" "$(where | cut -d ' ' -f 1)" "2"

# The pull request has a thread now (its review, above): the GITHUB view
# shows it, and Alt N and enter go there.
tx select-window -t "=$S:900"
waitfor 'tx capture-pane -p -t "=$S:900" | grep -q "goes there"'
ok "GITHUB: a pull request's row and card show the thread it has" \
	"$(tx capture-pane -p -t "=$S:900" | grep -c '01 \[')|$(tx capture-pane -p -t "=$S:900" | grep -c '⏎ goes there')" "2|1"
key 1b4e # Alt N
waitfor '[ "$(where | cut -d " " -f 1)" = 1 ]'
ok "...Alt N goes to its thread" "$(where | cut -d ' ' -f 1)" "1"
tx select-window -t "=$S:900"
waitfor '[ "$(where | cut -d " " -f 1)" = 900 ]'
key 0d # enter
waitfor '[ "$(where | cut -d " " -f 1)" = 1 ]'
ok "...and enter the same: a pull request there is for its thread" "$(where | cut -d ' ' -f 1)" "1"

# LINEAR: what Linear finds for you; enter, the form for the issue.
tx select-window -t "=$S:901"
waitfor 'tx capture-pane -p -t "=$S:901" | grep -q "none yet"'
ok "LINEAR: weaver's own, what Linear finds for you, its card" \
	"$(tx capture-pane -p -t "=$S:901" | grep -c 'QUEUE · 1')|$(tx capture-pane -p -t "=$S:901" | grep -c 'Fix it')|$(tx capture-pane -p -t "=$S:901" | grep -c 'none yet: ⏎ starts one on t/eng-42-fix-it')|$(tx capture-pane -p -t "=$S:901" | grep -c 'STEPS')" "1|2|1|1" ||
	tx capture-pane -p -t "=$S:901" | sed 's/^/  linear: /' | head -30
key 0d # enter
waitfor 'f=$(ls "$SD" | grep -E "^form\.[^.]+$" | head -n 1); [ -n "$f" ] && [ "$(jq -r .item.linear.id "$SD/$f" 2>/dev/null)" = ENG-42 ]'
FORM=$SD/$(ls "$SD" | grep -E '^form\.[^.]+$' | head -n 1)
hdr=$(wvr _form fields "$FORM" x)
ok "...enter, the form for the issue, in the repository used last" \
	"$(jq -r '"\(.repo) \(.branch) \(.item.linear.id) "' "$FORM")|$(printf '%s\n' "$hdr" | awk '{ print $1 }' | tr '\n' ' ')" \
	"$(cd "$AT/r" && pwd -P) t/eng-42-fix-it ENG-42 |from mode repo where branch base agent "
waitfor '[ "$(grep -c "nothing: it waits for you" "$AT/screen.log")" -gt 0 ]'
ok "...no task written for you" "$(grep -c 'Implement ENG-42' "$AT/screen.log")" "0"
key 0d # enter
waitfor '[ "$(windows)" = "0 1 2 3 900 901 " ]'
AG3=$(pane_at 3 1)
waitfor '[ -f "$AT/args.$AG3" ]'
waitfor '[ "$(tx display -p -t "=$S:3" "#{window_name}")" = "03 ENG-42 Fix it · r/t/eng-42-fix-it" ]'
ok "...enter: its thread, on Linear's branch, named for the issue, its agent told nothing" \
	"$(git -C "$(tx display -p -t "%$AG3" '#{pane_current_path}')" symbolic-ref --short HEAD)|$(tx display -p -t "=$S:3" '#{window_name}')|$(grep -c 'ENG-42' "$AT/args.$AG3")" \
	"t/eng-42-fix-it|03 ENG-42 Fix it · r/t/eng-42-fix-it|0"
tx select-window -t "=$S:901"
waitfor 'tx capture-pane -p -t "=$S:901" | grep -q "goes there"'
ok "...the view shows the thread it has" "$(tx capture-pane -p -t "=$S:901" | grep -c '03 \[')|$(tx capture-pane -p -t "=$S:901" | grep -c '⏎ goes there')" "2|1"

# A view not set up: a dot in the strip, its window says what it needs and
# what fixes it; enter runs that, and the window becomes the view.
tx kill-window -t "=$S:901"
rm -f "$AT/linear-key" "$SD/linear-auth"
tx run-shell -t "=$S:" "wvr view linear"
waitfor 'tx capture-pane -p -t "=$S:901" 2>/dev/null | grep -q "wvr linear login"'
ok "a view not signed in: a dot in the strip, and its window says what it needs" \
	"$(tx display -p -t "=$S:0" '#{T:status-left}' | grep -c 'LINEAR#\[norange\] #\[fg=#616161\]·')|$(tx capture-pane -p -t "=$S:901" | grep -c 'needs.*no Linear API key')" "1|1"
tx send-keys -t "=$S:901" Enter
waitfor 'tx capture-pane -p -t "=$S:901" | grep -q "personal API key"'
tx send-keys -t "=$S:901" -l lin_wrong
tx send-keys -t "=$S:901" Enter
waitfor 'tx capture-pane -p -t "=$S:901" | grep -q "did not sign in"'
ok "...enter asks for a key, and says when Linear refuses it" "$(tx capture-pane -p -t "=$S:901" | grep -c 'did not sign in')|$([ -f "$AT/linear-key" ] && echo kept || echo none)" "1|none"
tx send-keys -t "=$S:901" -l lin_fake
tx send-keys -t "=$S:901" Enter
waitfor 'tx capture-pane -p -t "=$S:901" | grep -q "LINEAR \]"' 100
ok "...a good one is kept, and the window becomes the view" \
	"$(cat "$AT/linear-key" 2>/dev/null)|$(tx display -p -t "=$S:0" '#{T:status-left}' | grep -c '·')" "lin_fake|0"

echo "-- $pass passed, $fail failed"
exit $fail
