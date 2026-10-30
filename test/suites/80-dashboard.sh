#!/bin/bash
# The dashboard (wvr ls): rows and the resume card rendered at fixed widths
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
		>"$SD/${SK}_$1.json"
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
jq -c --argjson s $((NOW - 90)) '.since = $s | .title = "Add a README"' "$SD/${SK}_1.json" >"$AT/s.json" &&
	mv "$AT/s.json" "$SD/${SK}_1.json"
put 2 '{state: "working", since: ($now - 630), title: "Refine the dashboard", turn: 4, root: "'"$T/dash"'",
	prompt: "group the rows by state with headings",
	subagents: [{id: "x1", type: "Explore", since: ($now - 630)}, {id: "x2", type: "general-purpose", since: ($now - 150)}]}'

# Golden renderings (test/golden/; WEAVER_GOLDEN_UPDATE=1 rewrites them, to
# be read and committed).
for w in 200 120 80 60; do
	golden "rows-$w.txt" "$(LS_MODE=plain COLUMNS=$w wvr _rows)"
done
# For fzf: id, pane, tab, then for a heading or gap the list positions of the
# agents around it (the cursor skips to them), the card's title, the row (a
# TAB after the glyph: search sees only what follows).
golden items-120.txt "$(FZF_COLUMNS=120 FZF_LINES=40 FZF_INPUT_STATE=hidden wvr _rows | strip)"
# A read-only thread (a review): in REVIEWS, below the rest, by its state
# there; the title's counts are by state still.
jq -nc --arg w "$T/dash-readme" '{name: "readme", task: "review the README", worktree: $w, agent: "claude", mode: "review"}' \
	>"$SD/${SK}_1.thread"
golden rows-review-120.txt "$(LS_MODE=plain COLUMNS=120 wvr _rows)"
rm -f "$SD/${SK}_1.thread"
for p in 0 1 2; do
	golden "card-$p-60.txt" "$(FZF_PREVIEW_COLUMNS=60 wvr _preview $p "$(FZF_COLUMNS=120 wvr _rows | awk -F '\t' -v id=a$p '$1 == id { print $6 }')" | strip)"
done
golden help.txt "$(FZF_SOCK=$AT/x.sock wvr _view help >/dev/null && FZF_SOCK=$AT/x.sock wvr _preview 0 | strip)"

# Past the open threads you mean to have (WEAVER_WIP), the board's count
# says so, and the form for a new thread on its spare line: a nudge, never
# a block.
count() { LS_MODE=plain COLUMNS=120 wvr _rows | head -n 1 | grep -o 'THREADS \] [0-9/]*'; }
ok "WIP: past WEAVER_WIP the board's count is against it, else as ever" \
	"$(WEAVER_WIP=2 count)|$(count)|$(WEAVER_WIP=0 count)" "THREADS ] 3/2|THREADS ] 3|THREADS ] 3"
jq -nc --arg w "$T/dash" '{name: "t", task: "t", worktree: $w, agent: "claude", num: 8}' >"$SD/${SK}_0.thread"
jq -nc --arg w "$T/dash" '{name: "t", task: "t", worktree: $w, agent: "claude", num: 7}' >"$SD/${SK}_2.thread"
ok "...and the form, its spare line telling how to close or hold one" \
	"$(WEAVER_WIP=1 wvr _form header "$AT/wip.form" "" | strip | tr -s ' ')|$(wvr _form header "$AT/wip.form" "")" \
	" 2 threads open, past your 1: h on the board holds one, x closes one| "
# A held thread: the form says it waits, to look at before starting another.
mkdir -p "$SD/lost"
jq -nc --arg k "$SK" --arg w "$T/dash" '{lost: 1, held: 1, note: "try again", skey: $k, state: {session_id: "sid-held"},
	thread: {name: "h", task: "h", worktree: $w, agent: "claude"}}' >"$SD/lost/${SK}_sid-held.json"
ok "...and what is held, past the limit and within it" \
	"$(WEAVER_WIP=1 wvr _form header "$AT/wip.form" "" | strip | tr -s ' ')|$(wvr _form header "$AT/wip.form" "" | strip | tr -s ' ')" \
	" 2 threads open, past your 1: h on the board holds one, x closes one · 1 held under HELD on the board| 1 held under HELD on the board, waiting: ⏎ on one there resumes it"
rm -f "$SD/lost/${SK}_sid-held.json"

# Many held: HELD shows its three newest and folds the rest into a row of
# their own; enter there (wvr _fold) shows them all, and folds them again.
for i in 1 2 3 4 5; do
	jq -nc --arg k "$SK" --arg w "$T/dash" --argjson h $((NOW - i * 60)) --arg n "next $i" \
		'{lost: $h, held: $h, note: $n, skey: $k, state: {session_id: ("sid-h" + ($h | tostring))},
		thread: {name: "h", task: "h", worktree: $w, agent: "claude"}}' >"$SD/lost/${SK}_h$i.json"
done
held_rows() { FZF_SOCK=$AT/fold.sock FZF_COLUMNS=120 wvr _rows | strip | awk -F '\t' '$1 ~ /^[lm]/ { print substr($1, 1, 1) ":" $NF }' | sed 's/  */ /g'; }
ok "HELD folds past three, the rest on a row of their own" \
	"$(held_rows | cut -c1-2 | tr '\n' ' ')|$(held_rows | grep -c '2 more held · ⏎ shows them')" "l: l: l: m: |1"
FZF_SOCK=$AT/fold.sock wvr _fold
ok "...enter there shows them all, and folds them again" \
	"$(held_rows | cut -c1-2 | tr '\n' ' ')|$(held_rows | grep -c 'folds them again')" "l: l: l: l: l: m: |1"
FZF_SOCK=$AT/fold.sock wvr _fold
ok "...folded again; the board as text lists every one" \
	"$(held_rows | cut -c1-2 | tr '\n' ' ')|$(LS_MODE=plain COLUMNS=120 wvr _rows | grep -c '\[=\]')" "l: l: l: m: |5"
rm -f "$SD/lost/${SK}"_h?.json
# The footer: ⏎ opens whatever the row is (a thread, a lost or held one);
# holding one mid-turn says it stops it, as you type the note; and one just
# held is on its way until its agent has closed.
foot() { FZF_COLUMNS=160 wvr _footer | strip; }
ok "footer: ⏎ opens, h holds" "$(foot | grep -o '⏎ OPEN')|$(foot | grep -o 'h HOLD')" "⏎ OPEN|h HOLD"
ok "...holding a thread mid-turn says it stops it; one waiting on you, not" \
	"$(FZF_PROMPT='hold 07 › ' foot | grep -c 'MID-TURN')|$(FZF_PROMPT='hold 08 › ' foot | grep -c 'MID-TURN')" "1|0"
printf '07\n' >"$AT/hb.holding"
ok "...one just held, while its agent closes" "$(FZF_SOCK=$AT/hb.sock foot | grep -o 'HOLDING 07')" "HOLDING 07"
rm -f "$SD/${SK}_0.thread" "$SD/${SK}_2.thread"
ok "...and not once it has" "$(FZF_SOCK=$AT/hb.sock foot | grep -c 'HOLDING')|$([ -f "$AT/hb.holding" ] || echo gone)" "0|gone"
golden empty-card.txt "$(wvr _preview '' | strip)"

# A thread whose agent has not reported yet: starting, then not reporting.
NP=$(Z new-pane --name starting -- bash --noprofile --norc)
NP=${NP#terminal_}
thread() {
	jq -nc --argjson p "$NP" --argjson c $(($(date +%s) - $1)) --arg a "$2" --arg w "$T/dash-flag" \
		'{name: "flaky", task: "Fix the flaky test", repo: "", branch: "flaky", worktree: $w, here: false,
		  worktree_created: true, agent: $a, created: $c, pane: $p}' >"$SD/${SK}_$NP.thread"
}
thread 70 claude
ok "a new thread, before its agent reports: working, starting" \
	"$(LS_MODE=plain wvr _rows | grep -c '^\[/\]  Fix the flaky test  *starting ')" "1"
working() { wvr status | grep -o '\[/\] [0-9]*' | cut -d ' ' -f 2; }
w70=$(working)
ok "...and its card says what it was asked" \
	"$(wvr _preview "$NP" | strip | sed -n '1p;3p')" "starting · 1m
asked      Fix the flaky test"
thread 300 codex
ok "a Codex thread that never reports: its hooks need trusting" \
	"$(LS_MODE=plain wvr _rows | grep -c '^\[ \]  Fix the flaky test  *no hooks: trust them in codex /hooks')" "1"
w300=$(working)
ok "...counted as nothing, by the status line as by the board, and its card says so" \
	"$((${w70:-0} - ${w300:-0}))|$(wvr _preview "$NP" | strip | sed -n '1p' | cut -d ' ' -f 1-2)" "1|not reporting"
Z close-pane --pane-id "terminal_$NP"
sleep 0.3
ok "...and gone with its pane" "$(LS_MODE=plain wvr _rows | grep -c 'flaky')" "0"
rm -f "$SD/${SK}_$NP.thread"

# An idle agent (seen, quiet) gets the idle cell; an ended one goes.
put 2 '{state: "idle", since: ($now - 630), seen: true, title: "Refine the dashboard", turn: 4, root: "'"$T/dash"'"}'
ok "an idle agent: the idle cell" "$(LS_MODE=plain wvr _rows | grep -c '^\[ \]  Refine the dashboard')" "1"
put 2 '{state: "ended", since: ($now - 630), seen: true, title: "Refine the dashboard", turn: 4, root: "'"$T/dash"'"}'
ok "...and an ended one is not listed" "$(LS_MODE=plain wvr _rows | grep -c 'Refine the dashboard')" "0"

done_suite
