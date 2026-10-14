#!/bin/bash
# The weaver session: `weaver` starts zellij with weaver's layout and keys. In
# it a thread gets a number, a tab in its number's order and the thread
# sidebar, which collapses to a rail on a narrow window; Alt y goes home and
# back. The test session stands in for one: its .layout file says weaver.
. "$(dirname "$0")/../lib.sh"
ROOT=$(cd "$(dirname "$0")/../.." && pwd -P)
fresh

for a in claude codex; do
	cat >"$AT/bin/$a" <<EOF
#!/bin/bash
printf '%s\n' "\$@" >"$AT/args.$a.\${ZELLIJ_PANE_ID:-none}"
[ -t 0 ] && exec bash --noprofile --norc -i
exit 0
EOF
	chmod +x "$AT/bin/$a"
done
waitfor() {
	local i
	for i in $(seq 50); do
		eval "$1" && return 0
		sleep 0.1
	done
	return 1
}
columns() { Z list-panes -a -j | jq -r ".[] | select((.is_plugin | not) and .id == $1) | .pane_columns"; }
position() { Z list-tabs -j | jq -r ".[] | select(.tab_id == $1) | .position"; }
# The panes of tab $1: its sidebar's into SB, its nvim's into NVP, its agent's into AG.
tab_panes() {
	local j
	j=$(Z list-panes -a -j)
	SB=$(printf '%s' "$j" | jq -r --argjson t "$1" '[.[] | select((.is_plugin | not) and .tab_id == $t
		and ((.terminal_command // "") | test(" sidebar$")))] | .[0].id // empty')
	NVP=$(printf '%s' "$j" | jq -r --argjson t "$1" '[.[] | select((.is_plugin | not) and .tab_id == $t
		and ((.terminal_command // "") | test(" nvim --thread ")))] | .[0].id // empty')
	AG=$(printf '%s' "$j" | jq -r --argjson t "$1" '[.[] | select((.is_plugin | not) and .tab_id == $t
		and ((.terminal_command // "") | test(" _agent ")))] | .[0].id // empty')
}
# weaver new --no-focus --here TASK in $T/repo; its tab's id into TAB.
thread() {
	local out
	out=$(cd "$T/repo" && weaver new --no-focus --here "$1" 2>&1)
	TAB=$(printf '%s' "$out" | sed -n 's/.*(tab \([0-9]*\))$/\1/p')
	tab_panes "$TAB"
	waitfor "[ -f '$SD/${SESSION}_$AG.thread' ]"
}
num() { jq -r .num "$SD/${SESSION}_$1.thread" 2>/dev/null; }

# --- weaver alone, outside zellij: start, attach, resurrect
mkdir -p "$AT/fakez2" "$AT/zcfg"
cat >"$AT/fakez2/zellij" <<'EOF'
#!/bin/sh
[ "$1" = list-sessions ] && { printf '%b' "${FAKE_SESSIONS:-}"; exit 0; }
echo "zellij $*"
EOF
chmod +x "$AT/fakez2/zellij"
out() {
	env -u ZELLIJ_SESSION_NAME -u ZELLIJ PATH="$AT/fakez2:$PATH" ZELLIJ_CONFIG_FILE="$AT/zellij.kdl" \
		ZELLIJ_CONFIG_DIR="$AT/zcfg" XDG_STATE_HOME="$AT/st2" "$@" weaver 2>&1
}
rm -rf "$AT/st2"
o=$(out)
ok "weaver: no session yet: zellij with weaver's config and layout" \
	"$o|$(cat "$AT/st2/weaver/weaver.layout" 2>/dev/null)" \
	"zellij --config $AT/st2/weaver/zellij-session.kdl --session weaver --new-session-with-layout $ROOT/config/weaver-layout.kdl|weaver"
ok "weaver: its config is yours plus its keys, well defined" \
	"$(zellij --config "$AT/st2/weaver/zellij-session.kdl" setup --check 2>&1 | grep -c 'Well defined')|$(grep -c 'bind "Alt j" { GoToNextTab; }' "$AT/st2/weaver/zellij-session.kdl")" "1|1"
ok "weaver: its keys come last in your keybinds, so they win" \
	"$(awk '/bind "Alt N"/ { a = NR } /GoToNextTab/ { b = NR } END { print (a < b) ? "after" : "before" }' "$AT/st2/weaver/zellij-session.kdl")" "after"
ok "weaver: a running session: attach" "$(out FAKE_SESSIONS='weaver [Created 1s ago] \n')" "zellij attach weaver"
ok "weaver: weaver's exited session: resurrect it, with its config" \
	"$(out FAKE_SESSIONS='weaver [Created 1h ago] (EXITED - attach to resurrect)\n')" \
	"zellij --config $AT/st2/weaver/zellij-session.kdl attach weaver"
rm -f "$AT/st2/weaver/weaver.layout"
o=$(out FAKE_SESSIONS='weaver [Created 1h ago] (EXITED - attach to resurrect)\n')
ok "weaver: an exited session from before its layout: says how to clear it" \
	"$(printf '%s' "$o" | grep -c 'zellij delete-session weaver')" "1"
mkdir -p "$AT/zcfg/layouts"
printf 'layout { tab name="home" { pane; }; }\n' >"$AT/zcfg/layouts/weaver.kdl"
ok "weaver: your layouts/weaver.kdl when you have one" \
	"$(out | grep -c -- "--new-session-with-layout $AT/zcfg/layouts/weaver.kdl")" "1"

# --- in the weaver session: home first, then the threads by number
echo weaver >"$SD/${SESSION}.layout"
home=$(Z new-tab --name home -- bash --noprofile --norc -i)
while [ "$(position "$home")" -gt 0 ]; do Z move-tab -t "$home" left; done
thread alpha
T1=$TAB A1=$AG S1=$SB N1=$NVP
thread beta
T2=$TAB A2=$AG
thread gamma
T3=$TAB A3=$AG
ok "session: threads are numbered in turn" "$(num "$A1")$(num "$A2")$(num "$A3")" "123"
ok "session: their tabs follow home, in their numbers' order" \
	"$(position "$home")$(position "$T1")$(position "$T2")$(position "$T3")" "0123"
ok "session: a thread's tab has the sidebar, nvim and the agent" \
	"$(columns "$S1")|$([ "$(columns "$N1")" -gt 100 ] && echo wide)|$(columns "$A1")" "26|wide|44"
weaver close --yes "$A2" >/dev/null 2>&1
ok "session: closing a thread takes its whole tab, sidebar and all" \
	"$(Z list-tabs -j | jq "[.[] | select(.tab_id == $T2)] | length")" "0"
thread delta
T4=$TAB A4=$AG
ok "session: the next thread takes the free number, and its place" \
	"$(num "$A4")|$(position "$T4")|$(position "$T3")" "2|2|3"
hook 2 SessionStart "$T/repo"
ok "session: its order: threads, then other tabs, their agents or their names" \
	"$(weaver _order | awk -F "$(printf '\037')" '{ print $1 ":" $2 ":" $5 }' | tr '\n' ' ')" \
	"thread:1:alpha thread:2:delta thread:3:gamma tab::one agent:: "

# Alt y: home, and back to where you were.
focus "$A1"
weaver go home
ok "Alt y: home" "$(Z list-tabs -j | jq -r '.[] | select(.active) | .tab_id')" "$home"
weaver go home
ok "Alt y again: back to the thread you came from" "$(Z list-tabs -j | jq -r '.[] | select(.active) | .tab_id')" "$T1"

# The sidebar to the window: open from 160 columns; your toggle holds.
ZELLIJ_PANE_ID=$S1 weaver _sidefit
ok "sidebar: open at 200 columns" "$(columns "$S1")" "26"
Z next-swap-layout -t "$T1"
sleep 0.3
ok "sidebar: Alt ] (next swap layout) folds it to a rail, the agent keeps its width" \
	"$(columns "$S1")|$(columns "$A1")" "6|44"
ZELLIJ_PANE_ID=$S1 weaver _sidefit
ok "sidebar: your rail holds while the window stays wide" "$(columns "$S1")" "6"
echo rail >"$SD/${SESSION}.side.$T1"
ZELLIJ_PANE_ID=$S1 weaver _sidefit
sleep 0.3
ok "sidebar: a window grown past 160 opens it again" "$(columns "$S1")" "26"

# Alt v brings nvim back between the sidebar and the agent.
Z close-pane --pane-id "terminal_$N1"
sleep 0.5
weaver edit "$A1" >/dev/null 2>&1
nv=$(nvim_pane "$A1")
a=$(columns "$A1")
ok "Alt v: nvim back between the sidebar and the agent, the split restored" \
	"$(Z list-panes -a -j | jq -r "[.[] | select((.is_plugin | not) and .tab_id == $T1)] | sort_by(.pane_x) | map(.id | tostring) | join(\",\")")|$([ "$a" -ge 39 ] && [ "$a" -le 49 ] && echo yes || echo "$a")" \
	"$S1,$nv,$A1|yes"

for t in "$T1" "$T3" "$T4" "$home"; do Z close-tab-by-id "$t" >/dev/null 2>&1; done
rm -f "$SD/${SESSION}.layout"
sleep 0.5
done_suite
