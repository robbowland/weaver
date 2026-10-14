#!/bin/bash
# The dashboard (weaver ls): rows and the resume card rendered at fixed widths
# (golden), then the real thing in a floating pane, driven with its keys and
# watched through fzf's own socket: the cursor never rests on a heading or a
# gap, 1-9 jump, / searches, s shows the screen, reloads keep the selection.
. "$(dirname "$0")/../lib.sh"
fresh

strip() { sed $'s/\x1b\\[[0-9;]*m//g'; }
GOLD=$(cd "$(dirname "$0")/../golden" && pwd -P)
golden() {
	[ "${WEAVER_GOLDEN_UPDATE:-0}" = 1 ] && printf '%s\n' "$2" >"$GOLD/$1"
	ok "golden $1" "$2" "$(cat "$GOLD/$1" 2>/dev/null)"
}
NOW=$(date +%s)
# put PANE FIELDS: an agent's state file, FIELDS a jq object ($now: NOW).
put() {
	jq -nc --argjson now "$NOW" "{pane: $1, agent: \"claude\", session_id: \"sid-$1\", ts: \$now} + ($2)" \
		>"$SD/${SESSION}_$1.json"
}

# --- the scene, in a repository of its own (the other suites leave changes
# in theirs): A needs you, B finished (2 files in its last turn), C works.
D=$T/dash
if [ ! -d "$D" ]; then
	git init -q -b main "$D" && git -C "$D" config user.email test@example.com && git -C "$D" config user.name test &&
		printf 'app\n' >"$D/app.py" && git -C "$D" add . && git -C "$D" commit -qm init &&
		git -C "$D" worktree add -q -b flag ../dash-flag && git -C "$D" worktree add -q -b readme ../dash-readme
fi
put 0 '{state: "needs-you", since: ($now - 270), title: "Add a CLI flag", turn: 3,
	waiting: "Bash: python app.py --name rob", root: "'"$T/dash-flag"'",
	prompt: "add a CLI flag to app.py, then run it to check it works",
	last_message: "Added --name; running it to check",
	tasks: [{id: "1", subject: "parse", done: true}, {id: "2", subject: "wire", done: true}, {id: "3", subject: "run", done: false}],
	files: ["'"$T/dash-flag/app.py"'"]}'
hook 1 SessionStart "$T/dash-readme"
hook 1 UserPromptSubmit "$T/dash-readme" "" "write a README and a guide"
settle # its start snapshot
printf 'readme\nmore\n' >"$T/dash-readme/README.md"
printf 'guide\n' >"$T/dash-readme/GUIDE.md"
hook 1 PostToolUse "$T/dash-readme" "$T/dash-readme/README.md"
hook 1 PostToolUse "$T/dash-readme" "$T/dash-readme/GUIDE.md"
hookj 1 '{"hook_event_name":"Stop","cwd":"'"$T/dash-readme"'","session_id":"sid-1","last_assistant_message":"Wrote README.md and GUIDE.md."}' >/dev/null
settle
jq -c --argjson s $((NOW - 90)) '.since = $s | .title = "Add a README"' "$SD/${SESSION}_1.json" >"$AT/s.json" &&
	mv "$AT/s.json" "$SD/${SESSION}_1.json"
put 2 '{state: "working", since: ($now - 630), title: "Refine the dashboard", turn: 4, root: "'"$T/dash"'",
	prompt: "group the rows by state with headings",
	subagents: [{id: "x1", type: "Explore", since: ($now - 630)}, {id: "x2", type: "general-purpose", since: ($now - 150)}]}'

# Golden renderings (test/golden/; WEAVER_GOLDEN_UPDATE=1 rewrites them, to
# be read and committed).
for w in 200 120 80 60; do
	golden "rows-$w.txt" "$(LS_MODE=plain COLUMNS=$w weaver _rows)"
done
# For fzf: id, pane, tab, then for a heading or gap the list positions of the
# agents around it (the cursor skips to them), the card's title, the row (a
# TAB after the glyph: search sees only what follows).
golden items-120.txt "$(FZF_COLUMNS=120 FZF_LINES=40 FZF_INPUT_STATE=hidden weaver _rows | strip)"
for p in 0 1 2; do
	golden "card-$p-60.txt" "$(FZF_PREVIEW_COLUMNS=60 weaver _preview $p "$(FZF_COLUMNS=120 weaver _rows | awk -F '\t' -v id=a$p '$1 == id { print $6 }')" | strip)"
done
golden help.txt "$(FZF_SOCK=$AT/x.sock weaver _view help >/dev/null && FZF_SOCK=$AT/x.sock weaver _preview 0 | strip)"
golden empty-card.txt "$(weaver _preview '' | strip)"

# A thread whose agent has not reported yet: starting, then not reporting.
NP=$(Z new-pane --name starting -- bash --noprofile --norc)
NP=${NP#terminal_}
thread() {
	jq -nc --argjson p "$NP" --argjson c $(($(date +%s) - $1)) --arg a "$2" --arg w "$T/dash-flag" \
		'{name: "flaky", task: "Fix the flaky test", repo: "", branch: "flaky", worktree: $w, here: false,
		  worktree_created: true, agent: $a, created: $c, pane: $p}' >"$SD/${SESSION}_$NP.thread"
}
thread 70 claude
ok "a new thread, before its agent reports: working, starting" \
	"$(LS_MODE=plain weaver _rows | grep -c '^\[/\]  Fix the flaky test  *starting ')" "1"
ok "...and its card says what it was asked" \
	"$(weaver _preview "$NP" | strip | sed -n '1p;3p')" "starting · 1m
asked      Fix the flaky test"
thread 300 codex
ok "a Codex thread that never reports: its hooks need trusting" \
	"$(LS_MODE=plain weaver _rows | grep -c '^\[ \]  Fix the flaky test  *no hooks: trust them in codex /hooks')" "1"
Z close-pane --pane-id "terminal_$NP"
sleep 0.3
ok "...and gone with its pane" "$(LS_MODE=plain weaver _rows | grep -c 'flaky')" "0"
rm -f "$SD/${SESSION}_$NP.thread"

# The card gets what the list leaves, and only when that is 6 lines or more.
room() { bash -c 'WEAVER_SOURCED=1 . "$(command -v weaver)"; LS_COLS=$1 LS_LINES=$2; ls_card_room $3; echo "$LS_PW${LS_PHIDE:+ hidden}"' _ "$@"; }
ok "card: below the list, in what it leaves" "$(room 72 30 12)" "down,13,border-top,wrap"
ok "card: hidden under 6 lines (s still shows it)" "$(room 72 22 12)" "down,50%,border-top,wrap,hidden hidden"
ok "card: beside the list from 150 columns" "$(room 160 20 12)" "right,74,border-left,wrap"

# --- the dashboard itself
focus 2
key 1b79 # Alt y
sleep 2.5
S=$(ls /tmp/weaver-$(id -u)/${SESSION}.fzf.*.sock 2>/dev/null | head -n 1)
FL=$(Z list-panes -a -j | jq -r '.[] | select((.is_plugin|not) and .is_floating and (.exited|not)) | .id')
fz() { curl -s -m 2 --unix-socket "$S" "http://localhost?limit=100" | jq -r "$1"; }
cur() { fz '.current.text // "" | split("\t")[0]'; }
press() {
	key "$1"
	sleep "${2:-0.4}"
}
ok "Alt y: the dashboard opens on the first agent" "$(cur)" "a0"
ok "Alt y: a borderless pane named weaver, fitted to the list and the card beside it" \
	"$(Z list-panes -a -j | jq -r ".[] | select(.id == $FL) | \"\\(.title) \\(.pane_rows == .pane_content_rows) \\(.pane_rows) \\(.pane_columns < 180)\"")" \
	"weaver true 14 true"
ok "...with the title, headings and counts on screen" \
	"$(Z dump-screen --pane-id "terminal_$FL" | grep -cE '\[ WEAVER :: THREADS \] 3|NEEDS YOU · 1|DONE · 1|WORKING · 1')" "4"
press 6b # k at the top
ok "k at the top stays on the first agent" "$(cur)" "a0"
press 6a # j
ok "j skips the gap and the heading" "$(cur)" "a1"
press 1b5b42 # down
ok "down too" "$(cur)" "a2"
press 1b5b41 # up
press 1b5b41
ok "up skips them going up" "$(cur)" "a0"
press 47 # G
ok "G: the last agent" "$(cur)" "a2"
press 1b5b357e # page up
ok "page up lands on an agent, not the top heading" "$(cur)" "a0"
press 1b5b367e # page down
ok "page down lands on an agent" "$(cur)" "a2"
press 67 # g
ok "g: the first agent" "$(cur)" "a0"

# A live reload (an agent changed) keeps the cursor on the same agent, though
# it moved.
press 6a
press 6a
ok "(on C)" "$(cur)" "a2"
put 0 '{state: "working", since: ($now - 1), title: "Add a CLI flag", root: "'"$T/dash-flag"'"}'
weaver status --push
sleep 1
ok "a reload keeps the selection on the same agent" "$(cur)|$(fz '[.matches[].text | split("\t")[0]] | join(" ")')" \
	"a2|h1 a1 g2 h2 a2 a0"

# s (and ctrl-s): the agent's screen in place of the card, and back.
press 73 0.8
ok "s: the screen instead of the card" "$(cat "${S%.sock}.view" 2>/dev/null)" "screen"
ok "...on screen, under whose it is" "$(Z dump-screen --pane-id "terminal_$FL" | grep -cE 'FAKE AGENT|screen · Refine the dashboard')" "2"
press 09 0.8 # tab
ok "tab: back to the card" "$(cat "${S%.sock}.view" 2>/dev/null)|$(Z dump-screen --pane-id "terminal_$FL" | grep -c 'asked      group the rows')" "|1"
press 3f 0.8 # ?
ok "?: the keys" "$(Z dump-screen --pane-id "terminal_$FL" | grep -cE 'this help|jump to the thread')" "2"
press 3f 0.8

# / searches: the agents alone; esc goes back to browsing. The input takes
# the blank line under the border: the list does not move.
row() { Z dump-screen --pane-id "terminal_$FL" | grep -n "$1" | head -n 1 | cut -d: -f1; }
before=$(row 'DONE 1')
press 2f 0.8
ok "/: the list stays where it was" "$(row 'DONE 1')" "$before"
press 63 0.2 # c
press 6c 0.2 # l
press 69 0.6 # i
ok "/ cli: the input shows, matches without headings" "$(fz '.query')|$(fz '[.matches[].text | split("\t")[0]] | join(" ")')" "cli|a0"
press 1b 1
ok "esc: back to browsing, query cleared, headings back" "$(fz '.query')|$(fz '.matches | length')|$(cur)" "|6|a0"

# 3: the third row's agent (A, working now), at once.
press 33 2.5
ok "3 jumps to the third row's agent and closes the dashboard" \
	"$(client)|$(Z list-panes -a -j | jq '[.[] | select((.is_plugin|not) and .is_floating and (.exited|not))] | length')" "terminal_0|0"
ok "...and its socket goes with it" "$(ls /tmp/weaver-$(id -u)/ | grep -c "^${SESSION}\.fzf\.")" "0"

# --- an empty board says how to start, and stays
fresh
key 1b79
sleep 2.5
FL=$(Z list-panes -a -j | jq -r '.[] | select((.is_plugin|not) and .is_floating and (.exited|not)) | .id')
ok "empty board: stays open and says how to start a thread" \
	"$(Z dump-screen --pane-id "terminal_$FL" | grep -cE 'No threads yet\. n starts one here|n NEW THREAD  q QUIT')" "2"
S=$(ls /tmp/weaver-$(id -u)/${SESSION}.fzf.*.sock 2>/dev/null | head -n 1)
ok "...in a small pane, with nothing to select" \
	"$(Z list-panes -a -j | jq -r ".[] | select(.id == $FL) | .pane_rows")|$(fz '.matches | length')" "7|0"
press 71 1 # q
ok "q closes it" "$(Z list-panes -a -j | jq '[.[] | select((.is_plugin|not) and .is_floating and (.exited|not))] | length')" "0"

# The first thread of the day: its record alone, no agent state anywhere yet.
jq -nc --argjson c $(($(date +%s) - 5)) --arg w "$T/dash-flag" \
	'{name: "first", task: "The first thread", worktree: $w, agent: "claude", created: $c, pane: 2}' >"$SD/${SESSION}_2.thread"
ok "a thread's record alone is on the board" "$(LS_MODE=plain weaver _rows | grep -c '^\[/\]  The first thread  *starting')" "1"
rm -f "$SD/${SESSION}_2.thread"

done_suite
