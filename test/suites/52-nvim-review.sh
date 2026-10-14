#!/bin/bash
# The review flow end to end, through the real engine: a turn made of real
# hook events leaves snapshots and an activity log, and the agent's nvim
# reviews that turn, traces a line back to its prompt, and shows the log.
# Also: Alt v pressed twice opens one nvim. Independent of the other suites.
. "$(dirname "$0")/../lib.sh"

typ() { printf '%s' "$1" | xxd -p | tr -d '\n'; }
eventually() {
	local name=$1 want=$2 got i
	shift 2
	for i in $(seq 60); do
		got=$("$@")
		[ "$got" = "$want" ] && break
		sleep 0.1
	done
	ok "$name" "$got" "$want"
}
if type fresh >/dev/null 2>&1; then
	fresh
else
	for s in /tmp/weaver-"$(id -u)"/"${SESSION}"_*.sock; do
		[ -S "$s" ] && nvim --server "$s" --remote-send '<C-\><C-n>:qa!<CR>' >/dev/null 2>&1
	done
	sleep 0.5
fi

W=$T/repo-wt-a
git -C "$W" checkout -q . && git -C "$W" clean -qfd
Z new-tab --name review-test >/dev/null
AG=$(Z new-pane --name agent-review --cwd "$W" -- bash --noprofile --norc -i)
AG=${AG#terminal_}
SID=sid-$AG
REF=refs/worktree/weaver/sessions/$SID

# --- Alt v twice, quickly: one nvim for the agent
hook "$AG" SessionStart "$W"
if type focus >/dev/null 2>&1; then
	focus "$AG"
else
	Z focus-pane-id "terminal_$AG" >/dev/null
fi
key 1b76
key 1b76
sleep 4
# nvim panes in the agent's tab
nvims() {
	Z list-panes -a -j | jq -r --argjson ag "$AG" '
		map(select(.is_plugin | not)) as $p
		| ($p | map(select(.id == $ag)) | .[0].tab_id) as $tab
		| [$p[] | select(.tab_id == $tab and (.exited | not) and (.title | startswith("nvim: ")))] | length'
}
ok "Alt v twice: one nvim for the agent" "$(nvims)" "1"
q() { nvq "$AG" "$1"; }
unstick "$AG"
q "execute('let g:weaver_follow_idle_ms = 300')" >/dev/null

point() { git -C "$W" log -1 --format='%(trailers:key=Weaver-Point,valueonly,separator=)' "$REF" 2>/dev/null | tr -d '\n'; }
bar() { q "luaeval('(function() for _, w in ipairs(vim.api.nvim_tabpage_list_wins(0)) do if vim.wo[w].winbar ~= \"\" then return vim.api.nvim_eval_statusline(vim.wo[w].winbar, { winid = w, use_winbar = true }).str end end return \"\" end)()')"; }
bar_done() { bar | grep -c '^✓ done · turn 1 · 2 files'; }
panel() { q "luaeval('(function() for _, w in ipairs(vim.api.nvim_list_wins()) do local b = vim.api.nvim_win_get_buf(w); if vim.bo[b].filetype == \"DiffviewFiles\" then return table.concat(vim.api.nvim_buf_get_lines(b, 0, -1, false), \" \") end end return \"\" end)()')"; }
panel_has() { panel | grep -c "$1"; }
log_lines() { q 'join(getline(1, "$"), "|")' | tr '|' '\n'; }
log_has_prompt() { log_lines | grep -c 'turn 1 · .* · make the app loud'; }

# --- one turn, through the real hooks
hook "$AG" UserPromptSubmit "$W" "" "make the app loud"
eventually "a prompt leaves a start snapshot" "start" point
printf 'line one\nLINE TWO, LOUD\nline three\n' >"$W/src/app.txt"
hook "$AG" PostToolUse "$W" "$W/src/app.txt"
printf 'made by the agent\n' >"$W/src/created.txt"
hook "$AG" PostToolUse "$W" "$W/src/created.txt"
hook "$AG" Stop "$W"
eventually "Stop leaves an end snapshot" "end" point

eventually "the bar: done, turn 1, two files to review" "1" bar_done

# --- review the turn: both files, the new untracked one included
q "execute('AgentReview turn')" >/dev/null
eventually "AgentReview turn lists the edited file" "1" panel_has 'app\.txt'
ok "...and the file it created (untracked)" "$(panel | grep -c 'created\.txt')" "1"
ok "...labelled as the turn" "$(q 'get(t:, "diffview_context", "")')" "turn 1"
ok "your index is untouched" "$(git -C "$W" diff --cached --name-only | wc -l | tr -d ' ')" "0"
q "execute('AgentReview turn')" >/dev/null
eventually "the same key closes it" "0" q "luaeval('require(\"weaver.diffview\").current() and 1 or 0')"

# --- why is this line here: its turn and prompt
q "execute('edit $W/src/app.txt | 2')" >/dev/null
q "execute('messages clear')" >/dev/null
q "luaeval('(function() local o = vim.notify; _G.__why = {}; vim.notify = function(m, ...) table.insert(_G.__why, m); return o(m, ...) end; return 1 end)()')" >/dev/null
q "execute('AgentWhy')" >/dev/null
sleep 0.5
ok "AgentWhy: the line's turn and prompt" \
	"$(q "luaeval('table.concat(_G.__why, \" \") .. vim.fn.execute(\"messages\")')" | tr '\n' ' ' | grep -c 'turn 1.*make the app loud')" "1"

# --- the activity log: the prompt and both edits
q "execute('AgentLog')" >/dev/null
eventually "AgentLog shows the turn's prompt" "1" log_has_prompt
ok "...and both edits" "$(log_lines | grep -c -E 'edit .*(app|created)\.txt')" "2"

git -C "$W" checkout -q . && git -C "$W" clean -qfd
Z go-to-tab-name review-test >/dev/null 2>&1 && Z close-tab >/dev/null 2>&1
done_suite
