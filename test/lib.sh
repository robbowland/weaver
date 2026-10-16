# Helpers for the suites in test/suites/; sourced. Run the suites through
# test/hooks.sh, which sets AT and starts the session.
: "${AT:?run the suites through test/hooks.sh}"

T=$AT/t
SD=$AT/state/weaver
SESSION=$(cat "$AT/session")
# The session's key (its state files' prefix): tmux-<session>.
SK="tmux-$SESSION"
TSOCK=$(cat "$AT/socket")
export WEAVER_TMUX_SOCKET=$TSOCK WEAVER_TMUX_SESSION=$SESSION TMUX="$TSOCK,0,0" \
	XDG_STATE_HOME=$AT/state PATH="$AT/bin:$PATH"
unset TMUX_PANE
# as_pane PANE CMD...: CMD as run from pane PANE ($TMUX_PANE); pane_env PANE:
# the same as an assignment, for a command line.
as_pane() {
	local p=$1
	shift
	TMUX_PANE="%$p" "$@"
}
pane_env() { printf 'TMUX_PANE=%%%s' "$1"; }
pass=0 fail=0
# weaver's state glyphs (G_NEED, G_DONE, ...), from its own definition.
eval "$(sed -nE 's/^	G(Need|Done|Seen|Work|Idle|Int|End) += "(.*)"$/\1 \2/p' "$(dirname "${BASH_SOURCE[0]}")/../internal/wvr/board.go" |
	while read -r n g; do printf "G_%s='%s'\n" "$(echo "$n" | tr '[:lower:]' '[:upper:]')" "$g"; done)"

ok() {
	if [ "$2" = "$3" ]; then
		echo "PASS $1"
		pass=$((pass + 1))
	else
		echo "FAIL $1: got [$2] want [$3]"
		fail=$((fail + 1))
	fi
}
done_suite() {
	echo "-- $pass passed, $fail failed"
	exit $fail
}

# Z ARGS: the few multiplexer actions the suites ask, in the shape they read
# (the pane list as JSON: id, tab_id, title, exited; closing a pane; a new
# one).
Z() {
	case "$1 ${2:-}" in
	"list-panes -a")
		tmux -S "$TSOCK" list-panes -s -t "=$SESSION" -F '#{pane_id}	#{window_id}	#{pane_title}	#{pane_dead}' |
			jq -R -s 'split("\n") | map(select(length > 0) | split("\t")
				| {id: (.[0][1:] | tonumber), tab_id: (.[1][1:] | tonumber), title: .[2], exited: (.[3] == "1"),
				   is_plugin: false, is_floating: false})'
		;;
	"close-pane --pane-id") tmux -S "$TSOCK" kill-pane -t "%${3#terminal_}" ;;
	"new-pane --name")
		# Z new-pane --name NAME -- CMD...: a pane beside agent a, as terminal_N.
		shift 3
		[ "${1:-}" = -- ] && shift
		printf 'terminal_%s\n' "$(tmux -S "$TSOCK" split-window -d -P -F '#{pane_id}' -t "%0" -- "$@" | tr -d '%')"
		;;
	*)
		echo "Z $*: not on tmux" >&2
		return 1
		;;
	esac
}
# key HEX: type bytes into the client (1b65 = Alt e, 0d = Enter, ...)
key() { printf '%s\n' "$1" >"$AT/keys.fifo"; }
# client: the pane the client is on (as terminal_N).
client() { printf 'terminal_%s\n' "$(tmux -S "$TSOCK" list-clients -F '#{pane_id}' | tail -n 1 | tr -d '%')"; }
# status_line: the counts as the session's status line shows them.
status_line() { wvr status --tmux "$SESSION"; }
# ring PANE: the shell in agent pane PANE rings the bell; bell_mark TAB: is
# tab (window) TAB marked for it ("true")?
ring() { tmux -S "$TSOCK" send-keys -t "%$1" "printf '\\a'" Enter; }
bell_mark() { [ "$(tmux -S "$TSOCK" display -p -t "@$1" '#{window_bell_flag}')" = 1 ] && echo true || echo false; }
ms() { perl -MTime::HiRes=time -e 'my $t=time; system(@ARGV); printf "%d", (time-$t)*1000' -- "$@"; }

# The events Claude Code sends to the async hook (config/claude-hooks.json).
async_event() {
	case $1 in
	PostToolUse | PostToolUseFailure | PostToolBatch | SubagentStart | SubagentStop | CwdChanged | \
		TaskCreated | TaskCompleted) return 0 ;;
	esac
	return 1
}
# hook PANE EVENT CWD [FILE] [PROMPT] [SID]: fake one Claude Code hook event,
# through the sync or async hook as Claude Code would.
hook() {
	local mode=""
	async_event "$2" && mode=--async
	jq -nc --arg e "$2" --arg c "$3" --arg f "${4:-}" --arg p "${5:-}" --arg s "${6:-sid-$1}" \
		'{session_id:$s, transcript_path:"/dev/null", cwd:$c, hook_event_name:$e}
		+ (if $e == "SessionStart" then {source:"startup"} else {} end)
		+ (if $f != "" then {tool_name:"Edit", tool_input:{file_path:$f}, tool_response:{}} else {} end)
		+ (if $p != "" then {prompt:$p} else {} end)' |
		as_pane "$1" wvr hook $mode
}
# hookj PANE JSON [ARGS]: fake a hook event from raw JSON (sync unless --async).
hookj() {
	local pane=$1 json=$2
	shift 2
	printf '%s' "$json" | as_pane "$pane" wvr hook "$@"
}
# hooka PANE JSON: the same through whichever hook Claude Code would use.
hooka() {
	local ev
	ev=$(printf '%s' "$2" | jq -r '.hook_event_name')
	if async_event "$ev"; then hookj "$1" "$2" --async; else hookj "$1" "$2"; fi
}
# Wait (up to ~5s) until the background work of earlier hooks is done.
settle() {
	local i
	for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25; do
		# The hook's background work.
		pgrep -f "$AT/.*wvr hook" >/dev/null 2>&1 || break
		sleep 0.2
	done
	sleep 0.2
}
st() { jq -r ".$2" "$SD/${SK}_$1.json"; }

sock() { printf '/tmp/weaver-%s/%s_%s.sock' "$(id -u)" "$SK" "$1"; }
# nvq AGENT EXPR: evaluate EXPR in the agent's nvim (3s timeout).
nvq() {
	perl -e 'alarm 3; exec @ARGV' nvim --server "$(sock "$1")" --remote-expr "$2" </dev/null || printf 'TIMEOUT'
	echo
}
nvim_pane() { sed -n 1p "$SD/${SK}_$1.nvim"; }

# The standard scene: A started in the main checkout but edits worktree a;
# B waits for permission in worktree b; C (tab two) writes a new file in b.
feed() {
	hook 0 SessionStart "$T/repo"
	hook 0 UserPromptSubmit "$T/repo" "" "Refactor the app in worktree A"
	hook 0 PostToolUse "$T/repo" "$T/repo-wt-a/src/app.txt"
	hook 0 Stop "$T/repo"
	hook 1 SessionStart "$T/repo-wt-b"
	hook 1 UserPromptSubmit "$T/repo-wt-b" "" "fix the readme"
	hookj 1 "{\"hook_event_name\":\"Notification\",\"cwd\":\"$T/repo-wt-b\",\"session_id\":\"sid-1\",\"notification_type\":\"permission_prompt\",\"message\":\"Claude needs your permission to use Bash\"}" >/dev/null
	hook 2 SessionStart "$T/repo"
	hook 2 UserPromptSubmit "$T/repo" "" "add a new module"
	hook 2 PostToolUse "$T/repo" "$T/repo-wt-b/newdir/sub/new.txt"
}

# Start a suite from a clean slate: no agent state, no agent nvims, no logs,
# whatever the suites before it did. The three agent panes stay.
fresh() {
	local f p
	for f in "$SD/${SK}"_*.nvim; do
		[ -f "$f" ] || continue
		read -r p <"$f"
		case $p in '' | *[!0-9]*) ;; *) Z close-pane --pane-id "terminal_$p" >/dev/null 2>&1 ;; esac
	done
	settle
	rm -rf "$SD"
	mkdir -p "$SD"
	rm -f "/tmp/weaver-$(id -u)/${SK}"_*.sock*
	rm -f "$AT/claude/sessions/"*.json
	sleep 0.3
}

# fake_nvim PANE: a headless nvim bound to agent PANE (socket and .nvim file,
# as `weaver nvim` would make), whose WeaverEvent only records what it gets.
# Its pane is the first agent pane that isn't PANE (only renamed, never used).
fake_nvim() {
	local s p
	s=$(sock "$1")
	mkdir -p "${s%/*}" && chmod 700 "${s%/*}"
	rm -f "$s"
	nvim --clean --headless --listen "$s" \
		-c 'lua _G.ev = {}; function WeaverEvent(j) table.insert(_G.ev, vim.json.decode(j)); return 0 end' \
		</dev/null >/dev/null 2>&1 &
	p=$!
	for _ in 1 2 3 4 5 6 7 8 9 10; do [ -S "$s" ] && break; sleep 0.2; done
	printf '%s\n%s\n' "${2:-99}" "$p" >"$SD/${SK}_$1.nvim"
	FAKE_NVIM_PID=$p
}
# nvim_events PANE [FIELD]: what the agent's fake nvim was told, one event per
# line: "ev" or "ev:FIELD-value".
nvim_events() {
	nvq "$1" "luaeval('table.concat(vim.tbl_map(function(e) return e.ev .. (\"${2:-}\" ~= \"\" and (\":\" .. tostring(e[\"${2:-}\"] or \"\")) or \"\") end, _G.ev), \",\")')"
}

# A process that stays alive, as a fake agent's pid; LIVE_PID.
live_pid() {
	sleep 100000 </dev/null >/dev/null 2>&1 &
	LIVE_PID=$!
	disown "$LIVE_PID" 2>/dev/null
}
# Its start time, as Claude Code writes it.
proc_start() { TZ=UTC LC_ALL=C ps -o lstart= -p "$1" | sed 's/ *$//'; }
# claude_session PID SID STATUS [UPDATED-MS] [WAITING-FOR]: fake the
# ~/.claude/sessions/<pid>.json file of a running Claude Code.
claude_session() {
	local upd=${4:-$(($(date +%s) * 1000))}
	jq -nc --argjson pid "$1" --arg sid "$2" --arg st "$3" --argjson u "$upd" --arg w "${5:-}" \
		--arg ps "$(proc_start "$1")" \
		'{pid: $pid, sessionId: $sid, cwd: "/", procStart: $ps, status: $st, statusUpdatedAt: $u}
		+ (if $w != "" then {waitingFor: $w} else {} end)' >"$AT/claude/sessions/$1.json"
}
# Focus pane N, going to its window.
focus() {
	tmux -S "$TSOCK" select-window -t "%$1" \; select-pane -t "%$1"
	sleep 0.2
}
