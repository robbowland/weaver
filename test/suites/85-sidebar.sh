#!/bin/bash
# The thread sidebar (weaver sidebar) and the home board (weaver home): the
# threads in their stable order, the sidebar's own thread inverted, digits by
# thread number, live through the hooks; jumping leaves both running.
. "$(dirname "$0")/../lib.sh"
fresh

strip() { sed $'s/\x1b\\[[0-9;]*m//g'; }
GOLD=$(cd "$(dirname "$0")/../golden" && pwd -P)
golden() {
	[ "${WEAVER_GOLDEN_UPDATE:-0}" = 1 ] && printf '%s\n' "$2" >"$GOLD/$1"
	ok "golden $1" "$2" "$(cat "$GOLD/$1" 2>/dev/null)"
}
NOW=$(date +%s)
put() {
	jq -nc --argjson now "$NOW" "{pane: $1, agent: \"claude\", session_id: \"sid-$1\", ts: \$now} + ($2)" \
		>"$SD/${SESSION}_$1.json"
}
# thread PANE NUM: a thread record with its stable number.
thread() {
	jq -nc --argjson p "$1" --argjson n "$2" --argjson c "$NOW" \
		'{name: "t\($n)", task: "", repo: "", branch: "", worktree: "", here: false, worktree_created: false,
		  agent: "claude", created: $c, pane: $p, num: $n}' >"$SD/${SESSION}_$1.thread"
}

# Thread 1 works (pane 2), thread 2 needs you (pane 0), thread 3 is done (pane 1).
put 2 '{state: "working", since: ($now - 630), title: "Refine the dashboard", root: "'"$T/repo"'"}'
put 0 '{state: "needs-you", since: ($now - 270), title: "Add a CLI flag", waiting: "Bash: python app.py --name rob", root: "'"$T/repo"'"}'
put 1 '{state: "done", since: ($now - 90), title: "Add a README", root: "'"$T/repo"'"}'
thread 2 1
thread 0 2
thread 1 3
TAB0=$(Z list-panes -a -j | jq -r '.[] | select((.is_plugin|not) and .id == 0) | .tab_id')

side() { FZF_COLUMNS=$1 FZF_LINES=20 WEAVER_SIDE_TAB=$TAB0 weaver _side | tr '\0' '\n' | cut -f4- | sed $'s/\x1b\\[7m/[/; s/\x1b\\[27m/]/' | strip; }
golden side-26.txt "$(side 26)"
golden side-rail.txt "$(side 12)"
ok "sidebar: in thread order, whatever the state" "$(side 26 | grep -oE '^ ?\[?[0-9]' | tr -d ' [' | tr -d '\n')" "123"

# --- a sidebar pane, live
SB=$(Z new-pane --name sidebar -- weaver sidebar)
SB=${SB#terminal_}
sleep 2
S=/tmp/weaver-$(id -u)/${SESSION}.side.$SB.sock
fz() { curl -s -m 2 --unix-socket "$S" "http://localhost?limit=100" | jq -r "$1"; }
ok "sidebar: listening for the hooks" "$([ -S "$S" ] && echo yes)" "yes"
SBTAB=$(Z list-panes -a -j | jq -r ".[] | select(.id == $SB) | .tab_id")
own=$(Z list-panes -a -j | jq -r "[.[] | select((.is_plugin|not) and .tab_id == $SBTAB and (.id == 0 or .id == 1 or .id == 2)) | .id] | min")
ok "sidebar: three threads, the cursor on its own tab's" "$(fz '.matches | length')|$(fz '.current.text | split("\t")[0]')" "3|a$own"
put 2 '{state: "done", since: ($now - 1), title: "Refine the dashboard", root: "'"$T/repo"'"}'
weaver status --push
sleep 1
ok "sidebar: reloaded when an agent changes" "$(fz '.matches[0].text' | strip | grep -c '✓')" "1"
ok "...from the rows push_status keeps for every sidebar" "$([ -s "$SD/${SESSION}.rows" ] && echo yes)" "yes"
tabname() { Z list-tabs -j | jq -r ".[] | select(.tab_id == $1) | .name"; }
TAB2=$(Z list-panes -a -j | jq -r '.[] | select((.is_plugin|not) and .id == 2) | .tab_id')
ok "outside the weaver session, tab names are left alone" "$(tabname "$TAB2")" "two"
echo weaver >"$SD/${SESSION}.layout"
weaver status --push
ok "in the weaver session, a thread's tab names it for the status bar" "$(tabname "$TAB2")" "1 Refine the dashboard · repo/main"
PLAIN=$(Z new-tab --name plain --cwd "$T/repo" -- bash --noprofile --norc)
sleep 0.5
ok "in the weaver session, a tab without an agent is a faint row of its own" \
	"$(FZF_COLUMNS=26 FZF_LINES=20 weaver _side | tr '\0' '\n' | cut -f4- | strip | grep -c '^ *·  plain')" "1"
Z close-tab-by-id "$PLAIN" >/dev/null 2>&1
rm -f "$SD/${SESSION}.layout"
Z rename-tab-by-id "$TAB2" two
focus "$SB"
key 33 # 3
sleep 2.5
ok "sidebar: 3 goes to thread 3" "$(client)" "terminal_1"
ok "...and the sidebar stays" "$(Z list-panes -a -j | jq -r ".[] | select(.id == $SB) | .exited")" "false"
Z close-pane --pane-id "terminal_$SB"
sleep 1.5
ok "sidebar: its files go with its pane" "$(ls /tmp/weaver-$(id -u)/ | grep -c "^${SESSION}\.side\.$SB\.")" "0"
ok "...and so do its processes (its tick too)" "$(pgrep -f "$AT/bin/weaver sidebar" | wc -l | tr -d ' ')" "0"

# --- the home board: the dashboard, numbered by thread, and it stays
HM=$(Z new-pane --name home -- weaver home)
HM=${HM#terminal_}
sleep 2.5
H=$(ls /tmp/weaver-$(id -u)/${SESSION}.fzf.*.sock 2>/dev/null | head -n 1)
hz() { curl -s -m 2 --unix-socket "$H" "http://localhost?limit=100" | jq -r "$1"; }
ok "home: rows carry the threads' numbers" \
	"$(hz '[.matches[].text | select(startswith("a")) | split("\t")[6] | gsub("\u001b\\[[0-9;]*m"; "") | ltrimstr(" ") | .[0:1]] | join("")')" "213"
focus "$HM"
key 32 # 2: thread 2, wherever its row is
sleep 2.5
ok "home: 2 goes to thread 2" "$(client)" "terminal_0"
ok "...and the home board stays" "$(Z list-panes -a -j | jq -r ".[] | select(.id == $HM) | .exited")|$([ -S "$H" ] && echo listening)" "false|listening"
Z close-pane --pane-id "terminal_$HM"
sleep 1.5
ok "home: its processes go with its pane" "$(pgrep -f "$AT/bin/weaver home" | wc -l | tr -d ' ')" "0"

# --- home's keys: n on an empty board opens the form; q goes back, as Alt y
mkdir -p "$T/aside"
mv "$SD/${SESSION}"_*.json "$SD/${SESSION}"_*.thread "$T/aside/"
BACK=$(Z list-tabs -j | jq -r '.[] | select(.active) | .tab_id')
HT=$(Z new-tab --name home --cwd "$T/repo" -- weaver home)
sleep 2.5
HP=$(Z list-panes -a -j | jq -r ".[] | select((.is_plugin|not) and .tab_id == $HT) | .id" | head -n 1)
key 6e # n
sleep 2.5
ok "home: n on an empty board opens the new-thread form" \
	"$(Z dump-screen --pane-id "terminal_$HP" | grep -c 'what should the agent do')" "1"
key 1b # esc: the form cancels, the board comes back
sleep 2.5
printf '%s\n' "$BACK" >"$SD/${SESSION}.last"
key 71 # q
sleep 1.5
ok "home: q goes back to the tab you came from" "$(Z list-tabs -j | jq -r '.[] | select(.active) | .tab_id')" "$BACK"
Z close-tab-by-id "$HT" >/dev/null 2>&1
mv "$T/aside/"* "$SD/"

done_suite
