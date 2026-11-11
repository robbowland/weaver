#!/bin/bash
# weaver on Rex, end to end, smoke: `wvr` starts its session on a
# throwaway Rex server of its own, a thread opens in a Rex session of its
# own with its review panel (a fake tuicr) and its agent, the agent's hooks
# reach its state under the session's Rex key, and quitting ends the
# sessions. Its own server, state, config, Claude Code dir
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
# view [SESSION]: the session's view (home's).
view() { R api call session.view "{\"session_id\":\"${1:-$SID}\"}"; }
# windows [SESSION]: each window's label and how many terminals it has.
windows() { view "$@" | jq -r '[.windows[] | "\(.label)/\([.layers[] | select(.kind == "tiled") | .blocks[]] | length)"] | join(" ")'; }
# block_at SESSION N I: the I-th block (from the left) of window N (from 1).
block_at() { view "$1" | jq -r --argjson n "$2" --argjson i "$3" '.windows[$n - 1].layers[] | select(.kind == "tiled") | .layout.split | [.before.block_id, .after.block_id][$i]'; }
num() { head -n 1 "$SD/rex/u/$(printf '%s' "$1" | tr ':' '_')"; }
# threads: the sessions Rex runs that weaver's record makes threads of
# home's (by id: Rex's labels are anyone's to change).
threads() {
	local live f
	live=$(R api call session.list '{}' | jq -r '.sessions[].session_id')
	for f in "$SD"/rex/sessions/*; do
		[ "$(sed -n 4p "$f")" = "$SID" ] || continue
		f=$(basename "$f" | sed 's/_/:/')
		printf '%s\n' "$live" | grep -qx "$f" && echo "$f"
	done
}
label() { R api call session.list '{}' | jq -r --arg s "$1" '.sessions[] | select(.session_id == $s) | .label'; }

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
	"$(grep -c "local wvr = \"$AT/bin/wvr\"" "$AT/weaver.lua")|$(grep -c 'require("weaver")' "$AT/start.out")|$(R api call client.keymap '{}' | jq '[.bindings[] | select(.source | startswith("weaver.lua"))] | length')" "1|0|11"

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
waitfor '[ -n "$(threads)" ] && [ "$(windows "$(threads)")" = "01 say hello · repo/main/2" ]' 100
T=$(threads)
ok "new: a session of its own, labelled with its name; its window, its review panel on the left and the agent on the right; home as it was" \
	"$(threads | wc -l | tr -d ' ')|$(label "$T")|$(windows "$T")|$(windows)" "1|say-hello|01 say hello · repo/main/2|home/1"
PB=$(block_at "$T" 1 0) AGB=$(block_at "$T" 1 1)
waitfor '[ -f "$AT/args.${AGB#block:}" ]' 100
AG=$(num "$AGB") PN=$(num "$PB")
waitfor '[ -f "$SD/rex-${S}_$AG.panel" ]' 100
ok "new: the agent took the task, and its review panel is bound to it" \
	"$(tail -n 1 "$AT/args.${AGB#block:}")|$(head -n 1 "$SD/rex-${S}_$AG.panel" 2>/dev/null)" "say hello|$PN"
# --- the panel: tuicr on the branch, again each time it ends, as the
# branch is then: nothing while the branch has nothing of its own (tuicr
# refuses an empty range), the work tree once there is an edit, then the
# commits since the fork point, by SHA, and nvim for its e.
waitfor 'R capture -s "$T" -b "$PB" | grep -q "no changes on this branch yet"' 50
ok "panel: a branch with nothing of its own says so, and runs no tuicr" \
	"$(R capture -s "$T" -b "$PB" | grep -c 'no changes on this branch yet')|$([ -f "$AT/tuicr.runs" ] && echo ran || echo none)" "1|none"
printf 'hello\n' >"$AT/repo/hello.txt"
waitfor 'grep -q -- "^-w " "$AT/tuicr.runs" 2>/dev/null' 60
ok "...an edit not yet committed: tuicr on the work tree, with nvim for its e" \
	"$(tail -n 1 "$AT/tuicr.runs" 2>/dev/null)|$(cat "$AT/tuicr.editor" 2>/dev/null)" "-w --theme micrographics --no-update-check|nvim"
git -C "$AT/repo" add hello.txt && git -C "$AT/repo" -c user.email=t@e -c user.name=t commit -q -m hello
waitfor 'grep -q -- "^-r " "$AT/tuicr.runs" 2>/dev/null' 60
ok "...a commit: its commits since the fork point, by SHA, with the work tree" \
	"$(tail -n 1 "$AT/tuicr.runs" 2>/dev/null)" "-r $FORK..HEAD -w --theme micrographics --no-update-check"
# alt+v on the agent with its panel closed: a panel again, on its left.
R api call block.close "{\"session_id\":\"$T\",\"block_id\":\"$PB\"}" >/dev/null
waitfor '[ "$(windows "$T")" = "01 say hello · repo/main/1" ]' 50
R api call session.focus_block "{\"session_id\":\"$T\",\"block_id\":\"$AGB\"}" >/dev/null
k=$(R api call client.key "{\"session_id\":\"$T\",\"key\":{\"action\":\"press\",\"key\":\"key_v\",\"mods\":\"alt\",\"text\":\"\",\"unshifted_codepoint\":118}}" | jq -r .handled)
waitfor '[ "$(windows "$T")" = "01 say hello · repo/main/2" ]' 50
PB=$(block_at "$T" 1 0) PN=$(num "$PB")
ok "alt+v: a closed review panel opens again on the agent's left, bound to it" \
	"$k|$(windows "$T")|$(block_at "$T" 1 1)|$(head -n 1 "$SD/rex-${S}_$AG.panel" 2>/dev/null)" "true|01 say hello · repo/main/2|$AGB|$PN"
# The width passes after a change (settleWidths' `wvr _width`) put back a
# split the Rex app evened out, and leave one you moved since.
ratio() { view "$T" | jq -r '.windows[0].layers[] | select(.kind == "tiled") | .layout.split.ratio'; }
split_at() { R api call session.resize_pane "{\"session_id\":\"$T\",\"before_block_id\":\"$PB\",\"after_block_id\":\"$AGB\",\"direction\":\"horizontal\",\"ratio\":$1}" >/dev/null; }
split_at 0.5
REX_SESSION=$SID wvr _width "$AG"
k=$(ratio)
split_at 0.6
REX_SESSION=$SID wvr _width "$AG"
ok "widths: a split evened out put back, one you moved left be" "$([ "$k" != 0.5 ] && echo back)|$(ratio)" "back|0.6"
# --- weaver's keys, pressed as the Rex app sends a bound key (its bindings
# first), though wvr runs with the server's environment
active() { view | jq -r '.active_window_id as $a | .windows[] | select(.window_id == $a) | .label'; }
alt() { R api call client.key "{\"session_id\":\"$SID\",\"key\":{\"action\":\"press\",\"key\":\"key_$1\",\"mods\":\"alt\",\"text\":\"\",\"unshifted_codepoint\":$(printf '%d' "'$1")}}" | jq -r .handled; }
# Alt j and Alt k are the app's next and previous session, in weaver's
# sessions (home's, a thread's); in another the terminal's.
ok "keys: Alt j and Alt k weaver's in its sessions, not in another" \
	"$(alt j)|$(SID=$T alt k)|$(OTHER=$(R api call session.create '{"label":"other"}' | jq -r .session_id) && SID=$OTHER alt j && R api call session.destroy "{\"session_id\":\"$OTHER\"}" >/dev/null)" "true|true|false"
# In one of weaver's popups (here Ctrl q's card) its keys are the popup's
# program's, as a popup takes every key: Alt j stays in the card.
popup() { view | jq -r '[.windows[].layers[] | select(.kind != "tiled") | .layer_id][0] // ""'; }
(REX_SESSION=$SID wvr _popup quit >/dev/null 2>&1 &)
waitfor '[ -n "$(popup)" ]' 50
# (The app focuses the popup's block; a server without one leaves it.)
R api call session.focus_block "{\"session_id\":\"$SID\",\"block_id\":\"$(view | jq -r '[.windows[].layers[] | select(.kind != "tiled") | .blocks[0].block_id][0]')\"}" >/dev/null
k=$(alt j)
sleep 0.5
ok "keys: in a popup, Alt j is the popup's" "$k|$(active)|$([ -n "$(popup)" ] && echo open)" "false|home|open" ||
	view | jq -c '.active_window_id as $a | .windows[] | {label, active: (.window_id == $a), f: .focused_block_id[-6:], layers: [.layers[] | {kind, b: [.blocks[].block_id[-6:]]}]}' | sed 's/^/  view: /'
R api call session.close_layer "{\"session_id\":\"$SID\",\"layer_id\":\"$(popup)\"}" >/dev/null 2>&1
waitfor '[ -z "$(popup)" ]' 50
ok "new: its thread record, under the session's Rex key and the agent's number" \
	"$(jq -r .num "$SD/rex-${S}_$AG.thread" 2>/dev/null)" "1"

# --- hooks: the agent's state under the session's Rex key and its number
hookj() {
	printf '%s' "$1" | CLAUDE_PID=$(cat "$AT/pid.${AGB#block:}") REX_SESSION=$T REX_BLOCK=$AGB wvr hook >/dev/null
}
ev() { printf '{"session_id":"sid-1","transcript_path":"/dev/null","cwd":"%s","hook_event_name":"%s"%s}' "$AT/repo" "$1" "${2:-}"; }
hookj "$(ev SessionStart ',"source":"startup"')"
hookj "$(ev UserPromptSubmit ',"prompt":"say hello"')"
ok "hook: working, turn 1, under home's key in the thread's session's life" \
	"$(jq -r '"\(.state) \(.turn) \(.rex_session)"' "$SD/rex-${S}_$AG.json" 2>/dev/null)" "working 1 $T"
ok "status: one working, from home or the thread" "$(REX_SESSION=$SID wvr status)|$(REX_SESSION=$T wvr status)" "[/] 1|[/] 1"

hookj "$(ev Stop ',"prompt_id":"p1","last_assistant_message":"hello"')"
seen() { jq -r '"\(.state) \(.seen)"' "$SD/rex-${S}_$AG.json"; }
k=$(seen)
REX_SESSION=$SID wvr next >/dev/null 2>&1
ok "seen: a thread done is new until alt+1 takes you to it" "$k|$(seen)" "done false|done true"

# --- the board's edits: rename (as its enter does it), and hold
REX_SESSION=$SID wvr _modedone "rename 01 › " "Greeting" >/dev/null 2>&1
waitfor '[ "$(windows "$T")" = "01 Greeting · repo/main/2" ]' 50
ok "rename in place: the thread its prompt names takes the name, and its window; its session keeps its label" \
	"$(windows "$T")|$(label "$T")|$(jq -r .title "$SD/rex-${S}_$AG.thread" 2>/dev/null)" "01 Greeting · repo/main/2|say-hello|Greeting"
# Rex's labels are anyone's (Claude Code's agents rename their sessions):
# weaver goes on finding the thread by id.
R session rename "$T" demo-greet >/dev/null
ok "a thread's session renamed in Rex: still the thread, its agent still followed" \
	"$(threads)|$(REX_SESSION=$SID wvr ls 2>/dev/null | grep -c Greeting)" "$T|1"
# --- a helper: an agent the thread's agent starts in a window of the
# thread's session (the weaver skill's way), reporting as agents do
HS=aaaaaaaa-0000-4000-8000-000000000001
mkdir -p "$AT/claude/projects/repo"
printf '{"type":"user","cwd":"%s","gitBranch":"main","message":{"role":"user","content":"check the tests"}}\n' "$AT/repo" \
	>"$AT/claude/projects/repo/$HS.jsonl"
HB=$(R window new -s "$T" helper --focus=false -c "$AT/repo" --shell=none --json -- "$AT/bin/claude" "check the tests" | jq -r '.block_ids[0]')
waitfor '[ -f "$AT/pid.${HB#block:}" ]' 100
helperj() {
	printf '%s' "$1" | CLAUDE_PID=$(cat "$AT/pid.${HB#block:}") REX_SESSION=$T REX_BLOCK=$HB wvr hook >/dev/null
}
hev() { printf '{"session_id":"%s","transcript_path":"/dev/null","cwd":"%s","hook_event_name":"%s"%s}' "$HS" "$AT/repo" "$1" "${2:-}"; }
helperj "$(hev SessionStart ',"source":"startup"')"
helperj "$(hev UserPromptSubmit ',"prompt":"check the tests"')"
helperj "$(hev Stop ',"prompt_id":"h1","last_assistant_message":"tests pass"')"
HP=$(num "$HB")
ok "helper: a window of the thread's session, weaver following its agent" \
	"$(windows "$T")|$(jq -r '"\(.state) \(.rex_session)"' "$SD/rex-${S}_$HP.json" 2>/dev/null)" "01 Greeting · repo/main/2 helper/1|done $T"
# The board of every thread lists threads: the helper waiting on a
# permission makes its thread need you. The thread's own board (alt+Y's)
# lists its agent and the helper, by its window's label; alt+1 goes to the
# helper's window.
helperj "$(hev UserPromptSubmit ',"prompt":"run them"')"
helperj "$(hev PermissionRequest ',"tool_name":"Bash","tool_input":{"command":"go test ./..."}')"
board() { REX_SESSION=$SID COLUMNS=160 wvr ls 2>/dev/null; }
ok "boards: every thread's has the thread, needing you for its helper; the thread's own has both" \
	"$(board | grep -c 'helper: ')|$(board | grep -c 'check the tests')|$(LS_SESSION=$T board | grep -c 'Greeting')|$(LS_SESSION=$T board | grep -c 'helper')" "1|0|1|1"
REX_SESSION=$SID wvr next >/dev/null 2>&1
focused() { view "$T" | jq -r '.active_window_id as $a | .windows[] | select(.window_id == $a) | .focused_block_id'; }
waitfor '[ "$(focused)" = "$HB" ]' 50
ok "alt+1: to the helper that needs you, in its window" "$(focused)" "$HB"
helperj "$(hev Stop ',"prompt_id":"h2","last_assistant_message":"tests pass"')"
# Adopted: its agent ends with its window, its conversation resumes as a
# thread in a session of its own.
REX_SESSION=$SID wvr adopt "$HP" >"$AT/adopt.out" 2>&1
waitfor '[ "$(threads | wc -l | tr -d " ")" = 2 ]' 100
A=$(threads | grep -vx "$T")
AB=$(block_at "$A" 1 1)
waitfor '[ -f "$AT/args.${AB#block:}" ]' 100
ok "adopt: the helper's window goes, its conversation resumes in a thread's session of its own" \
	"$(windows "$T")|$(label "$A")|$(windows "$A")|$(tr '\n' ' ' <"$AT/args.${AB#block:}" 2>/dev/null)|$([ -f "$SD/rex-${S}_$HP.json" ] && echo kept)" \
	"01 Greeting · repo/main/2|check-the-tests|02 check the tests · repo/main/2|--resume $HS |" ||
	sed 's/^/  adopt: /' "$AT/adopt.out"
REX_SESSION=$SID wvr close --yes "$(num "$AB")" >/dev/null 2>&1
waitfor '[ "$(threads)" = "$T" ]' 100
ok "close: the adopted thread's session goes, the other stays" "$(threads)|$(label "$A")" "$T|"
held() { for f in "$SD"/lost/*.json; do [ -f "$f" ] && jq -r 'select(.held > 0) | "\(.note)|\(.state.session_id)"' "$f"; done; }
REX_SESSION=$SID wvr hold "$AG" "write the reply" >"$AT/hold.out" 2>&1
waitfor '[ -z "$(threads)" ]' 100
ok "hold: its session goes, home stays, the thread kept as held with your note" "$(threads)|$(windows)|$(held)" "|home/1|write the reply|sid-1" ||
	sed 's/^/  hold: /' "$AT/hold.out"
REX_SESSION=$SID wvr resume --all >/dev/null 2>&1
sleep 1
ok "...resume --all (what the session lost) leaves it waiting" "$(threads)|$(held | wc -l | tr -d ' ')" "|1"
REX_SESSION=$SID wvr resume sid-1 >"$AT/resume.out" 2>&1
waitfor '[ -n "$(threads)" ] && [ "$(windows "$(threads)")" = "01 Greeting · repo/main/2" ]' 100
T=$(threads) AGB=$(block_at "$T" 1 1)
waitfor '[ -f "$AT/args.${AGB#block:}" ]' 100
ok "...resumed, it comes back in a session of its own, named and on its conversation" \
	"$(windows "$T")|$(tr '\n' ' ' <"$AT/args.${AGB#block:}" 2>/dev/null)|$(held | wc -l | tr -d ' ')" "01 Greeting · repo/main/2|--resume sid-1 |0" ||
	sed 's/^/  resume: /' "$AT/resume.out"
AG=$(num "$AGB")
hookj "$(ev SessionStart ',"source":"resume"')"

# --- Rex restarts: it brings the sessions back, their ids and all, but a
# shell in each terminal, what ran there gone. wvr starts home again, the
# thread back on its conversation in a session of its own, and ends what
# Rex brought back.
OLD=$SID OLDT=$T
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
	waitfor '[ -n "$(threads)" ] && [ "$(windows "$(threads)")" = "01 Greeting · repo/main/2" ]' 200
	T=$(threads) AGB=$(block_at "$(threads)" 1 1)
	waitfor '[ -f "$AT/args.${AGB#block:}" ]' 100
	gone=$(R api call session.list '{}' | jq -r --arg o "$OLD" --arg t "$OLDT" '[.sessions[] | select(.session_id == $o or .session_id == $t)] | length')
	ok "restart: home and the thread's session made again, the thread back on its conversation, Rex's husks gone" \
		"$(sid | wc -l | tr -d ' ')|$([ "$SID" != "$OLD" ] && [ "$T" != "$OLDT" ] && echo new)|$(windows)|$(windows "$T")|$(tr '\n' ' ' <"$AT/args.${AGB#block:}" 2>/dev/null)|$gone" \
		"1|new|home/1|01 Greeting · repo/main/2|--resume sid-1 |0" ||
		sed 's/^/  restart: /' "$AT/restart.out"
fi

# --- quit: the sessions end, the thread kept
T=$(threads)
REX_SESSION=$SID wvr quit --yes >/dev/null 2>&1
waitfor '[ -z "$(sid)" ]' 50
ok "quit: home and the thread's session have gone, the thread kept" \
	"$([ -z "$(sid)" ] && echo gone)|$(label "$T")|$(ls "$SD"/rex-"${S}"_*.thread 2>/dev/null | wc -l | tr -d ' ')" "gone||1"

echo "-- $pass passed, $fail failed"
[ "$fail" -eq 0 ]
