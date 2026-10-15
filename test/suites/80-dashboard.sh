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
		  worktree_created: true, agent: $a, created: $c, pane: $p}' >"$SD/${SK}_$NP.thread"
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
rm -f "$SD/${SK}_$NP.thread"

done_suite
