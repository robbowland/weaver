#!/bin/bash
# weaver on Rex, end to end, smoke: `WEAVER_MUX=rex wvr` starts its session
# on a throwaway Rex server of its own, a thread opens with its nvim and its
# agent, the agent's hooks reach its state under the session's Rex key, and
# quitting ends the session. Its own server, state, config, Claude Code dir
# and PATH, with a fake agent: never your Rex, agents or keys. Skipped
# without rex.
#   test/rex.sh
set -u
HERE=$(cd "$(dirname "$0")" && pwd -P)
ROOT=${HERE%/test}
command -v rex >/dev/null || { echo "SKIP rex is not installed"; exit 0; }
# A Unix socket's path has room for about 100 bytes: not $TMPDIR's.
AT=$(mktemp -d /tmp/weaver-rex.XXXXXX)
S=wr$$
unset TMUX TMUX_PANE REX_SESSION REX_BLOCK WEAVER_TMUX_SESSION WEAVER_RESTORE LINEAR_API_KEY
export REX_SERVER=unix://$AT/r.sock WEAVER_MUX=rex WEAVER_SESSION=$S
export XDG_STATE_HOME=$AT/state XDG_CONFIG_HOME=$AT/config XDG_DATA_HOME=$AT/data CLAUDE_CONFIG_DIR=$AT/claude
export WEAVER_VIEWS= WEAVER_SELF_SETUP=0 EDITOR=nvim VISUAL=nvim TERM=xterm-256color
[ -f "$HERE/nvim/init.lua" ] && export VIMINIT="lua dofile('$HERE/nvim/init.lua')"
SD=$XDG_STATE_HOME/weaver
mkdir -p "$AT/bin" "$SD" "$AT/claude/sessions" "$XDG_CONFIG_HOME"
printf -- '-- nothing\n' >"$AT/init.lua"
# A fake agent: notes its arguments and pid by its block, then waits as a shell.
cat >"$AT/bin/claude" <<EOF
#!/bin/bash
b=\${REX_BLOCK#block:}
printf '%s\n' "\$@" >"$AT/args.\$b"
echo \$\$ >"$AT/pid.\$b"
exec bash --noprofile --norc -i
EOF
chmod +x "$AT/bin/claude"
(cd "$ROOT" && go build -o "$AT/bin/wvr" ./cmd/wvr) || exit 1
export PATH="$AT/bin:$PATH" WEAVER_CLAUDE="$AT/bin/claude"
git init -q -b main "$AT/repo" && git -C "$AT/repo" -c user.email=t@e -c user.name=t commit -q --allow-empty -m init
# The server's terminals take its environment: PATH has this build's wvr
# first, else home starts again as the wvr installed (installs.go).
rex server run --listen "$REX_SERVER" --state-dir "$AT/rexstate" --config "$AT/init.lua" >"$AT/server.log" 2>&1 &
server=$!
cleanup() {
	kill "$server" 2>/dev/null
	wait "$server" 2>/dev/null
	pkill -f -- "--listen /tmp/weaver-$(id -u)/rex-${S}_" 2>/dev/null
	rm -rf "$AT"
}
trap cleanup EXIT

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
waitfor() {
	local i
	for ((i = 0; i < ${2:-50}; i++)); do
		eval "$1" && return 0
		sleep 0.1
	done
	return 1
}
R() { rex --autostart=false "$@"; }
waitfor 'R api call server.status "{}" >/dev/null 2>&1' 100
sid() { R api call session.list '{}' | jq -r --arg s "$S" '.sessions[] | select(.label == $s) | .session_id'; }
view() { R api call session.view "{\"session_id\":\"$SID\"}"; }
# windows: each window's label and how many terminals it has.
windows() { view | jq -r '[.windows[] | "\(.label)/\([.layers[] | select(.kind == "tiled") | .blocks[]] | length)"] | join(" ")'; }
# block_at N I: the I-th block (from the left) of window N (from 1).
block_at() { view | jq -r --argjson n "$1" --argjson i "$2" '.windows[$n - 1].layers[] | select(.kind == "tiled") | .layout.split | [.before.block_id, .after.block_id][$i]'; }
num() { head -n 1 "$SD/rex/u/$(printf '%s' "$1" | tr ':' '_')"; }

# --- weaver: on Rex with WEAVER_MUX=rex, home first with the board
wvr >"$AT/start.out" 2>&1
SID=$(sid)
waitfor 'ls /tmp/weaver-$(id -u)/rex-$S.fzf.*.sock >/dev/null 2>&1' 100
ok "start: a Rex session of its name, home in its first window, the board in it" \
	"$([ -n "$SID" ] && echo session)|$(windows)|$(ls /tmp/weaver-$(id -u)/rex-$S.fzf.*.sock 2>/dev/null | wc -l | tr -d ' ')" \
	"session|home/1|1" ||
	{
		echo "  home shows:"
		R capture -s "$SID" -b "$(view | jq -r '.windows[0].layers[0].blocks[0].block_id')" --trim | sed 's/^/  | /'
	}
ok "start: weaver's keys for Rex, written for init.lua to load" \
	"$(grep -c "local wvr = \"$AT/bin/wvr\"" "$XDG_CONFIG_HOME/rex/weaver.lua")|$(grep -c 'require("weaver")' "$AT/start.out")" "1|1"

# --- a thread (wvr new, here), as Alt N's form makes it
(cd "$AT/repo" && REX_SESSION=$SID wvr new --here 'say hello' >"$AT/new.out" 2>&1)
waitfor '[ "$(windows)" = "home/1 01 say hello · repo/main/2" ]' 100
ok "new: window 2, its label, nvim on the left and the agent on the right" "$(windows)" "home/1 01 say hello · repo/main/2"
NVB=$(block_at 2 0) AGB=$(block_at 2 1)
waitfor '[ -f "$AT/args.${AGB#block:}" ]' 100
AG=$(num "$AGB") NV=$(num "$NVB")
waitfor '[ -f "$SD/rex-${S}_$AG.nvim" ]' 100
ok "new: the agent took the task, and its nvim is bound to it" \
	"$(tail -n 1 "$AT/args.${AGB#block:}")|$(head -n 1 "$SD/rex-${S}_$AG.nvim" 2>/dev/null)" "say hello|$NV"
ok "new: its thread record, under the session's Rex key and the agent's number" \
	"$(jq -r .num "$SD/rex-${S}_$AG.thread" 2>/dev/null)" "1"

# --- hooks: the agent's state under the session's Rex key and its number
hookj() {
	printf '%s' "$1" | CLAUDE_PID=$(cat "$AT/pid.${AGB#block:}") REX_SESSION=$SID REX_BLOCK=$AGB wvr hook >/dev/null
}
ev() { printf '{"session_id":"sid-1","transcript_path":"/dev/null","cwd":"%s","hook_event_name":"%s"%s}' "$AT/repo" "$1" "${2:-}"; }
hookj "$(ev SessionStart ',"source":"startup"')"
hookj "$(ev UserPromptSubmit ',"prompt":"say hello"')"
ok "hook: working, turn 1, its nvim" "$(jq -r '"\(.state) \(.turn) \(.nvim_pane)"' "$SD/rex-${S}_$AG.json" 2>/dev/null)" "working 1 $NV"
ok "status: one working" "$(REX_SESSION=$SID wvr status)" "[/] 1"

# --- quit: the session ends, its thread kept
REX_SESSION=$SID wvr quit --yes >/dev/null 2>&1
waitfor '[ -z "$(sid)" ]' 50
ok "quit: the session has gone, its thread kept" "$([ -z "$(sid)" ] && echo gone)|$(ls "$SD"/rex-"${S}"_*.thread 2>/dev/null | wc -l | tr -d ' ')" "gone|1"

echo "-- $pass passed, $fail failed"
[ "$fail" -eq 0 ]
