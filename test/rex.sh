#!/bin/bash
# weaver on Rex, end to end, smoke: `wvr` starts its session on a
# throwaway Rex server of its own, a thread opens with its review panel
# (a fake tuicr) and its agent, the agent's hooks reach its state under the session's Rex key, and
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
unset WEAVER_RESTORE LINEAR_API_KEY
# Every Rex variable but where rex is (REX_BIN_DIR) is the caller's: gone.
# Rex keeps a server's runtime files under REX_RUNTIME_DIR: here, never in
# your Rex's directory, and rex never falls back to your server.
for v in $(compgen -e | grep '^REX_' | grep -v '^REX_BIN_DIR$'); do unset "$v"; done
export REX_RUNTIME_DIR=$AT/run REX_SERVER=unix://$AT/r.sock REX_CONFIG=$AT/init.lua WEAVER_SESSION=$S
export XDG_STATE_HOME=$AT/state XDG_CONFIG_HOME=$AT/config XDG_DATA_HOME=$AT/data CLAUDE_CONFIG_DIR=$AT/claude
export WEAVER_VIEWS= WEAVER_SELF_SETUP=0 TERM=xterm-256color
SD=$XDG_STATE_HOME/weaver
mkdir -p "$AT/bin" "$SD" "$AT/claude/sessions" "$XDG_CONFIG_HOME"
# Rex's configuration loads weaver's keys, as the guide says, from before
# wvr has first written them (the server reads it as it starts).
printf 'pcall(require, "weaver")\n' >"$AT/init.lua"
# A fake agent: notes its arguments and pid by its block, then waits as a shell.
cat >"$AT/bin/claude" <<EOF
#!/bin/bash
b=\${REX_BLOCK#block:}
printf '%s\n' "\$@" >"$AT/args.\$b"
echo \$\$ >"$AT/pid.\$b"
exec bash --noprofile --norc -i
EOF
chmod +x "$AT/bin/claude"
# A fake tuicr: notes how the review panel ran it, and what its e would
# edit with, then ends, as q does.
cat >"$AT/bin/tuicr" <<EOF
#!/bin/bash
printf '%s\n' "\$*" >>"$AT/tuicr.runs"
printf '%s\n' "\$EDITOR" >"$AT/tuicr.editor"
sleep 1
EOF
chmod +x "$AT/bin/tuicr"
(cd "$ROOT" && go build -o "$AT/bin/wvr" ./cmd/wvr) || exit 1
export PATH="$AT/bin:$PATH" WEAVER_CLAUDE="$AT/bin/claude" WEAVER_TUICR="$AT/bin/tuicr"
git init -q -b main "$AT/repo" && git -C "$AT/repo" -c user.email=t@e -c user.name=t commit -q --allow-empty -m init
# Its review base: origin's main, where main left it.
git -C "$AT/repo" update-ref refs/remotes/origin/main HEAD
FORK=$(git -C "$AT/repo" rev-parse HEAD)
# The server's terminals take its environment: PATH has this build's wvr
# first, else home starts again as the wvr installed (installs.go).
# Rex's server runs weaver's keys' commands with its own environment: the
# app's, with no rex on PATH and no REX_BIN_DIR. As here: PATH without any
# directory that has a rex (its terminals have theirs: Rex sets REX_BIN_DIR
# in them).
REXCMD=$(command -v rex)
NOREX=$(printf '%s' "$PATH" | tr ':' '\n' | while read -r d; do [ -x "$d/rex" ] || printf '%s:' "$d"; done)
serve() {
	env -u REX_BIN_DIR PATH="${NOREX%:}" \
		"$REXCMD" server run --listen "$REX_SERVER" --state-dir "$AT/rexstate" --config "$AT/init.lua" >>"$AT/server.log" 2>&1 &
	server=$!
}
serve
cleanup() {
	kill "$server" 2>/dev/null
	wait "$server" 2>/dev/null
	rm -f /tmp/weaver-$(id -u)/rex-${S}[._]*
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

# --- weaver: home first with the board
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
ok "start: weaver's keys written beside Rex's init.lua, and Rex told to read them" \
	"$(grep -c "local wvr = \"$AT/bin/wvr\"" "$AT/weaver.lua")|$(grep -c 'require("weaver")' "$AT/start.out")|$(R api call client.keymap '{}' | jq '[.bindings[] | select(.source | startswith("weaver.lua"))] | length')" "1|0|10"

# --- wvr in a Rex session weaver did not make (any Rex terminal's):
# weaver's session, shown, never a board of that one
OTHER=$(R api call session.create '{"label":"other"}' | jq -r .session_id)
REX_SESSION=$OTHER perl -e 'alarm 10; exec @ARGV' wvr >"$AT/other.out" 2>&1
ok "start in another Rex session: weaver's shown, no second one, no board there" \
	"$(sid | wc -l | tr -d ' ')|$(ls /tmp/weaver-$(id -u)/rex-$S.fzf.*.sock /tmp/weaver-$(id -u)/rex-${OTHER#session:}.fzf.*.sock 2>/dev/null | wc -l | tr -d ' ')|$(grep -c "the $S session runs in Rex" "$AT/other.out")" \
	"1|1|1" || sed 's/^/  other: /' "$AT/other.out"
R api call session.destroy "{\"session_id\":\"$OTHER\"}" >/dev/null 2>&1

# --- a thread (wvr new, here), as Alt N's form makes it
(cd "$AT/repo" && REX_SESSION=$SID wvr new --here 'say hello' >"$AT/new.out" 2>&1)
waitfor '[ "$(windows)" = "home/1 01 say hello · repo/main/2" ]' 100
ok "new: window 2, its label, its review panel on the left and the agent on the right" "$(windows)" "home/1 01 say hello · repo/main/2"
PB=$(block_at 2 0) AGB=$(block_at 2 1)
waitfor '[ -f "$AT/args.${AGB#block:}" ]' 100
AG=$(num "$AGB") PN=$(num "$PB")
waitfor '[ -f "$SD/rex-${S}_$AG.panel" ]' 100
ok "new: the agent took the task, and its review panel is bound to it" \
	"$(tail -n 1 "$AT/args.${AGB#block:}")|$(head -n 1 "$SD/rex-${S}_$AG.panel" 2>/dev/null)" "say hello|$PN"
# --- the panel: tuicr on the branch, again each time it ends, as the
# branch is then: nothing while the branch has nothing of its own (tuicr
# refuses an empty range), the work tree once there is an edit, then the
# commits since the fork point, by SHA, and nvim for its e.
waitfor 'R capture -s "$SID" -b "$PB" | grep -q "no changes on this branch yet"' 50
ok "panel: a branch with nothing of its own says so, and runs no tuicr" \
	"$(R capture -s "$SID" -b "$PB" | grep -c 'no changes on this branch yet')|$([ -f "$AT/tuicr.runs" ] && echo ran || echo none)" "1|none"
printf 'hello\n' >"$AT/repo/hello.txt"
waitfor 'grep -q -- "^-w " "$AT/tuicr.runs" 2>/dev/null' 60
ok "...an edit not yet committed: tuicr on the work tree, with nvim for its e" \
	"$(tail -n 1 "$AT/tuicr.runs" 2>/dev/null)|$(cat "$AT/tuicr.editor" 2>/dev/null)" "-w --theme micrographics --no-update-check|nvim"
git -C "$AT/repo" add hello.txt && git -C "$AT/repo" -c user.email=t@e -c user.name=t commit -q -m hello
waitfor 'grep -q -- "^-r " "$AT/tuicr.runs" 2>/dev/null' 60
ok "...a commit: its commits since the fork point, by SHA, with the work tree" \
	"$(tail -n 1 "$AT/tuicr.runs" 2>/dev/null)" "-r $FORK..HEAD -w --theme micrographics --no-update-check"
# alt+v on the agent with its panel closed: a panel again, on its left.
R api call block.close "{\"session_id\":\"$SID\",\"block_id\":\"$PB\"}" >/dev/null
waitfor '[ "$(windows)" = "home/1 01 say hello · repo/main/1" ]' 50
R api call session.focus_block "{\"session_id\":\"$SID\",\"block_id\":\"$AGB\"}" >/dev/null
k=$(R api call client.key "{\"session_id\":\"$SID\",\"key\":{\"action\":\"press\",\"key\":\"key_v\",\"mods\":\"alt\",\"text\":\"\",\"unshifted_codepoint\":118}}" | jq -r .handled)
waitfor '[ "$(windows)" = "home/1 01 say hello · repo/main/2" ]' 50
PB=$(block_at 2 0) PN=$(num "$PB")
ok "alt+v: a closed review panel opens again on the agent's left, bound to it" \
	"$k|$(windows)|$(block_at 2 1)|$(head -n 1 "$SD/rex-${S}_$AG.panel" 2>/dev/null)" "true|home/1 01 say hello · repo/main/2|$AGB|$PN"
# The width passes after a change (settleWidths' `wvr _width`) put back a
# split the Rex app evened out, and leave one you moved since.
ratio() { view | jq -r '.windows[1].layers[] | select(.kind == "tiled") | .layout.split.ratio'; }
split_at() { R api call session.resize_pane "{\"session_id\":\"$SID\",\"before_block_id\":\"$PB\",\"after_block_id\":\"$AGB\",\"direction\":\"horizontal\",\"ratio\":$1}" >/dev/null; }
split_at 0.5
REX_SESSION=$SID wvr _width "$AG"
k=$(ratio)
split_at 0.6
REX_SESSION=$SID wvr _width "$AG"
ok "widths: a split evened out put back, one you moved left be" "$([ "$k" != 0.5 ] && echo back)|$(ratio)" "back|0.6"
# --- weaver's keys, pressed as the Rex app sends a bound key (its bindings
# first): Alt k home, Alt j back to the thread, though wvr runs with the
# server's environment
active() { view | jq -r '.active_window_id as $a | .windows[] | select(.window_id == $a) | .label'; }
alt() { R api call client.key "{\"session_id\":\"$SID\",\"key\":{\"action\":\"press\",\"key\":\"key_$1\",\"mods\":\"alt\",\"text\":\"\",\"unshifted_codepoint\":$(printf '%d' "'$1")}}" | jq -r .handled; }
k=$(alt k)
waitfor '[ "$(active)" = home ]' 50
k="$k|$(active)|$(alt j)"
waitfor '[ "$(active)" != home ]' 50
ok "keys: Alt k, home; Alt j, the thread again" "$k|$(active)" "true|home|true|01 say hello · repo/main" ||
	{ grep -i weaver "$AT/server.log" | tail -5 | sed 's/^/  server: /'; }
# Rex starts each key's wvr at once, so a press can come while the last is
# at work: every press moves one window on from where the last left you, as
# a next window should (two from the thread: home, then the thread).
k="$(alt j)$(alt j)"
sleep 1.5
ok "keys: Alt j twice at once, two windows on" "$k|$(active)" "truetrue|01 say hello · repo/main"
# In one of weaver's popups (here Ctrl q's card) its keys are the popup's
# program's, as a popup takes every key: Alt j stays in the card.
popup() { view | jq -r '[.windows[].layers[] | select(.kind != "tiled") | .layer_id][0] // ""'; }
(REX_SESSION=$SID wvr _popup quit >/dev/null 2>&1 &)
waitfor '[ -n "$(popup)" ]' 50
# (The app focuses the popup's block; a server without one leaves it.)
R api call session.focus_block "{\"session_id\":\"$SID\",\"block_id\":\"$(view | jq -r '[.windows[].layers[] | select(.kind != "tiled") | .blocks[0].block_id][0]')\"}" >/dev/null
k=$(alt j)
sleep 0.5
ok "keys: in a popup, Alt j is the popup's" "$k|$(active)|$([ -n "$(popup)" ] && echo open)" "false|01 say hello · repo/main|open" ||
	view | jq -c '.active_window_id as $a | .windows[] | {label, active: (.window_id == $a), f: .focused_block_id[-6:], layers: [.layers[] | {kind, b: [.blocks[].block_id[-6:]]}]}' | sed 's/^/  view: /'
R api call session.close_layer "{\"session_id\":\"$SID\",\"layer_id\":\"$(popup)\"}" >/dev/null 2>&1
waitfor '[ -z "$(popup)" ]' 50
ok "new: its thread record, under the session's Rex key and the agent's number" \
	"$(jq -r .num "$SD/rex-${S}_$AG.thread" 2>/dev/null)" "1"

# --- hooks: the agent's state under the session's Rex key and its number
hookj() {
	printf '%s' "$1" | CLAUDE_PID=$(cat "$AT/pid.${AGB#block:}") REX_SESSION=$SID REX_BLOCK=$AGB wvr hook >/dev/null
}
ev() { printf '{"session_id":"sid-1","transcript_path":"/dev/null","cwd":"%s","hook_event_name":"%s"%s}' "$AT/repo" "$1" "${2:-}"; }
hookj "$(ev SessionStart ',"source":"startup"')"
hookj "$(ev UserPromptSubmit ',"prompt":"say hello"')"
ok "hook: working, turn 1" "$(jq -r '"\(.state) \(.turn)"' "$SD/rex-${S}_$AG.json" 2>/dev/null)" "working 1"
ok "status: one working" "$(REX_SESSION=$SID wvr status)" "[/] 1"

# --- seen when you look: Rex has no pane-focus-in hook; home's board follows
# the session's events, so a thread done while you are elsewhere stays new
# until you stay on its window a moment, however you came (here the server's
# own focus_window, as the app's tabs change it)
winat() { view | jq -r --argjson n "$1" '.windows[$n - 1].window_id'; }
look() { R api call session.focus_window "{\"session_id\":\"$SID\",\"window_id\":\"$(winat "$1")\"}" >/dev/null; }
look 1
hookj "$(ev Stop ',"prompt_id":"p1","last_assistant_message":"hello"')"
ok "seen: a thread done while you are elsewhere is new" "$(jq -r '"\(.state) \(.seen)"' "$SD/rex-${S}_$AG.json")" "done false"
look 2
sleep 0.3
look 1
sleep 2.5
ok "...passing through its window, it stays new" "$(jq -r .seen "$SD/rex-${S}_$AG.json")" "false"
look 2
waitfor '[ "$(jq -r .seen "$SD/rex-${S}_$AG.json")" = true ]' 60
ok "...staying on it, it is seen" "$(jq -r .seen "$SD/rex-${S}_$AG.json")" "true"

# --- the board's edits: rename (as its enter does it), and hold
REX_SESSION=$SID wvr _modedone "rename 01 › " "Greeting" >/dev/null 2>&1
waitfor '[ "$(windows)" = "home/1 01 Greeting · repo/main/2" ]' 50
ok "rename in place: the thread its prompt names takes the name, and its window" \
	"$(windows)|$(jq -r .title "$SD/rex-${S}_$AG.thread" 2>/dev/null)" "home/1 01 Greeting · repo/main/2|Greeting"
held() { for f in "$SD"/lost/*.json; do [ -f "$f" ] && jq -r 'select(.held > 0) | "\(.note)|\(.state.session_id)"' "$f"; done; }
REX_SESSION=$SID wvr hold "$AG" "write the reply" >"$AT/hold.out" 2>&1
waitfor '[ "$(windows)" = "home/1" ]' 100
ok "hold: its window goes, the thread kept as held with your note" "$(windows)|$(held)" "home/1|write the reply|sid-1" ||
	sed 's/^/  hold: /' "$AT/hold.out"
REX_SESSION=$SID wvr resume --all >/dev/null 2>&1
sleep 1
ok "...resume --all (what the session lost) leaves it waiting" "$(windows)|$(held | wc -l | tr -d ' ')" "home/1|1"
REX_SESSION=$SID wvr resume sid-1 >"$AT/resume.out" 2>&1
waitfor '[ "$(windows)" = "home/1 01 Greeting · repo/main/2" ]' 100
AGB=$(block_at 2 1)
waitfor '[ -f "$AT/args.${AGB#block:}" ]' 100
ok "...resumed, it comes back named and on its conversation" \
	"$(windows)|$(tr '\n' ' ' <"$AT/args.${AGB#block:}" 2>/dev/null)|$(held | wc -l | tr -d ' ')" "home/1 01 Greeting · repo/main/2|--resume sid-1 |0" ||
	sed 's/^/  resume: /' "$AT/resume.out"
hookj "$(ev SessionStart ',"source":"resume"')"

# --- Rex restarts: it brings the session back, its ids and all, but a
# shell in each terminal, what ran there gone. wvr starts the session
# again, the thread back on its conversation, and ends what Rex brought back.
OLD=$SID
kill "$server"
wait "$server" 2>/dev/null
serve
waitfor 'R api call server.status "{}" >/dev/null 2>&1' 100
if [ "$(sid)" != "$OLD" ]; then
	echo "SKIP restart: this Rex did not bring the session back"
else
	rm -f "$AT"/args.*
	wvr >"$AT/restart.out" 2>&1
	waitfor '[ -n "$(sid)" ] && [ "$(sid)" != "$OLD" ]' 100
	SID=$(sid)
	waitfor '[ "$(windows)" = "home/1 01 Greeting · repo/main/2" ]' 200
	AGB=$(block_at 2 1)
	waitfor '[ -f "$AT/args.${AGB#block:}" ]' 100
	ok "restart: a session of its own again, the thread back on its conversation, Rex's husk gone" \
		"$(sid | wc -l | tr -d ' ')|$([ "$SID" != "$OLD" ] && echo new)|$(windows)|$(tr '\n' ' ' <"$AT/args.${AGB#block:}" 2>/dev/null)" \
		"1|new|home/1 01 Greeting · repo/main/2|--resume sid-1 " ||
		sed 's/^/  restart: /' "$AT/restart.out"
fi

# --- quit: the session ends, its thread kept
REX_SESSION=$SID wvr quit --yes >/dev/null 2>&1
waitfor '[ -z "$(sid)" ]' 50
ok "quit: the session has gone, its thread kept" "$([ -z "$(sid)" ] && echo gone)|$(ls "$SD"/rex-"${S}"_*.thread 2>/dev/null | wc -l | tr -d ' ')" "gone|1"

echo "-- $pass passed, $fail failed"
[ "$fail" -eq 0 ]
