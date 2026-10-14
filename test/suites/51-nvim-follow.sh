#!/bin/bash
# Following in a real zellij session, end to end: hook events from a fake
# agent reach its nvim, which follows them politely. Keys are typed through
# the terminal, so "your keystrokes are never taken" is tested for real.
# Independent of the other suites: it makes its own agent pane and nvim.
. "$(dirname "$0")/../lib.sh"

# typ TEXT: TEXT as the hex that `key` types.
typ() { printf '%s' "$1" | xxd -p | tr -d '\n'; }
# eventually NAME WANT CMD...: run CMD until it prints WANT (at most 4s).
eventually() {
	local name=$1 want=$2 got i
	shift 2
	for i in $(seq 40); do
		got=$("$@")
		[ "$got" = "$want" ] && break
		sleep 0.1
	done
	ok "$name" "$got" "$want"
}
A=$T/repo-wt-a
B=$T/repo-wt-b
q() { nvq "$AG" "$1"; }
pending() { q "luaeval('(function() for _, w in ipairs(vim.api.nvim_tabpage_list_wins(0)) do local s = vim.wo[w].winbar ~= \"\" and vim.api.nvim_eval_statusline(vim.wo[w].winbar, { winid = w, use_winbar = true }).str or \"\"; local p = s:match(\"→ (%S+)\"); if p then return p end end return \"\" end)()')"; }
focus_nvim() {
	Z focus-pane-id "terminal_$NV" >/dev/null 2>&1
	eventually "  (nvim pane focused)" "terminal_$NV" client >/dev/null
}

# Start clean: another suite's nvim holding these worktrees' files would make
# ours open them read-only (their swap files).
if type fresh >/dev/null 2>&1; then
	fresh
else
	for s in /tmp/weaver-"$(id -u)"/"${SESSION}"_*.sock; do
		[ -S "$s" ] && nvim --server "$s" --remote-send '<C-\><C-n>:qa!<CR>' >/dev/null 2>&1
	done
	sleep 0.5
fi

# A fake agent in a tab of its own, and its nvim beside it.
Z new-tab --name follow-test >/dev/null
AG=$(Z new-pane --name agent-follow --cwd "$T/repo" -- bash --noprofile --norc -i)
AG=${AG#terminal_}
hook "$AG" SessionStart "$T/repo"
hook "$AG" UserPromptSubmit "$T/repo" "" "work on worktree a"
NV=$(Z new-pane --close-on-exit --cwd "$T/repo" -- "$AT/bin/weaver" nvim "$AG" "$T/repo/README.md")
NV=${NV#terminal_}
for _ in $(seq 40); do [ "$(q 1 2>/dev/null)" = 1 ] && break; sleep 0.25; done
unstick "$AG"
q "execute('let g:weaver_follow_idle_ms = 300')" >/dev/null
ok "the agent's nvim has weaver's event entry point" "$(q "luaeval('type(WeaverEvent)')")" "function"

# --- an edit while you are idle: followed, into the worktree's own tab
sleep 0.5
hook "$AG" PostToolUse "$T/repo" "$A/src/app.txt"
eventually "idle: agent edit followed" "repo-wt-a|app.txt" nvst "$AG"
ok "...in a tab of its own" "$(tabcwds "$AG")" "repo,repo-wt-a"

# --- typing: every key lands where you type it
focus_nvim
key "$(typ ':tabfirst')0d"
key "$(typ 'Ohello ')"
eventually "  (typing in insert mode)" "i" q 'mode()' >/dev/null
hook "$AG" PostToolUse "$T/repo" "$A/README.md"
key "$(typ 'world')"
eventually "typing: every keystroke in your buffer" "hello world" q 'getline(1)'
ok "...still in your file, still in insert mode" "$(q 'tabpagenr() . "|" . expand("%:t") . "|" . mode()')" "1|README.md|i"
ok "...the agent's edit waits in the winbar" "$(pending)" "README.md"
eventually "...on screen" "1" sh -c "zellij action dump-screen --pane-id terminal_$NV | grep -c '→ README.md'"
key 1b
eventually "after Esc and a moment: followed to the agent's tab" "2|repo-wt-a/README.md" \
	q 'tabpagenr() . "|" . expand("%:p:h:t") . "/" . expand("%:t")'
ok "...your unsaved line kept in its own tab" \
	"$(q 'getbufline(bufnr("'"$T"'/repo/README.md"), 1)[0] . "|" . getbufvar(bufnr("'"$T"'/repo/README.md"), "&modified")')" "hello world|1"
q "execute('call setbufvar(bufnr(\"$T/repo/README.md\"), \"&modified\", 0)')" >/dev/null

# --- unsaved changes in the agent's tab: never replaced; followed once saved
q 'nvim_buf_set_lines(0, 0, 1, v:false, ["MY UNSAVED EDIT"])' >/dev/null
hook "$AG" PostToolUse "$T/repo" "$A/src/app.txt"
sleep 0.8
ok "unsaved buffer not replaced" "$(q 'expand("%:t") . "|" . getline(1) . "|" . &modified')" "README.md|MY UNSAVED EDIT|1"
ok "...the edit stays pending" "$(pending)" "app.txt"
q "execute('write')" >/dev/null
eventually "after saving: followed" "repo-wt-a|app.txt" nvst "$AG"
git -C "$A" checkout -q README.md

# --- reloading
printf 'REWRITTEN\n' >"$A/src/app.txt"
eventually "an outside rewrite reloads" "REWRITTEN" q 'getline(1)'
git -C "$A" checkout -q src/app.txt

# --- the agent moves: a tab per worktree; snapping back to the start dir keeps it
hook "$AG" UserPromptSubmit "$T/repo" "" "back at the start dir"
sleep 0.6
ok "cwd snapped back to the start dir: nvim stays put" "$(nvst "$AG")" "repo-wt-a|app.txt"
hook "$AG" UserPromptSubmit "$B" "" "now feature b"
eventually "agent moves to wt-b: its own tab" "repo-wt-b|repo,repo-wt-a,repo-wt-b" \
	sh -c "printf '%s|%s' \"\$(nvim --server '$(sock "$AG")' --remote-expr 'fnamemodify(getcwd(), \":t\")')\" \"\$(nvim --server '$(sock "$AG")' --remote-expr 'join(map(range(1, tabpagenr(\"\$\")), {_, n -> fnamemodify(getcwd(-1, n), \":t\")}), \",\")')\""

# --- a subagent's edit in another worktree: back to that worktree's tab
hookj "$AG" "{\"hook_event_name\":\"PostToolUse\",\"cwd\":\"$T/repo\",\"session_id\":\"sid-$AG\",\"agent_id\":\"sub-a\",\"tool_name\":\"Edit\",\"tool_input\":{\"file_path\":\"$A/src/app.txt\"}}" >/dev/null
eventually "subagent edit in worktree a: back to its tab" "repo-wt-a|app.txt" nvst "$AG"

# --- Bash edits are followed; Bash that changes nothing is not
bash_json() { jq -nc --arg c "$1" --arg cwd "$T/repo" --arg s "sid-$AG" \
	'{hook_event_name:"PostToolUse", cwd:$cwd, session_id:$s, tool_name:"Bash", tool_input:{command:$c}}'; }
git -C "$B" checkout -q src/app.txt
hookj "$AG" "$(bash_json "cd $B && git status")" --async
sleep 0.2
sed -i '' 's/line two/line two, by sed/' "$B/src/app.txt"
hookj "$AG" "$(bash_json "cd $B && sed -i '' 's/line two/line two, by sed/' src/app.txt")" --async
eventually "Bash edit: followed into its worktree's tab" "repo-wt-b|app.txt|line two, by sed" \
	sh -c "printf '%s|%s' \"\$(nvim --server '$(sock "$AG")' --remote-expr 'fnamemodify(getcwd(), \":t\") . \"|\" . expand(\"%:t\")')\" \"\$(nvim --server '$(sock "$AG")' --remote-expr 'getline(2)')\""
hookj "$AG" "$(bash_json "cd $A && git log -1")" --async
sleep 0.8
ok "Bash that changes nothing: nvim stays" "$(nvst "$AG")" "repo-wt-b|app.txt"
git -C "$B" checkout -q src/app.txt

# --- the nvim hears about Bash that changed no file it can name
q "luaeval('(function() _G.__ct = 0; vim.api.nvim_create_autocmd(\"User\", { pattern = \"WeaverEvent\", callback = function(e) if e.data.ev == \"checktime\" then _G.__ct = _G.__ct + 1 end end }); return 1 end)()')" >/dev/null
hookj "$AG" "$(bash_json 'sed -i.bak s/a/b/ /nonexistent/x')" --async
eventually "Bash finished: nvim told to re-check files" "1" q "luaeval('_G.__ct')"

# --- a stale swap file never raises the blocking ATTENTION dialog
SW=$A/src/swapme.txt
printf 'swap me\n' >"$SW"
XDG_STATE_HOME=$AT/state nvim --clean --headless "+normal Gochanged" "+sleep 30" "$SW" &
sp=$!
sleep 1.5
kill -9 $sp 2>/dev/null
wait $sp 2>/dev/null
hook "$AG" PostToolUse "$T/repo" "$SW"
eventually "follow into a swapped file: responsive, read-only" "swapme.txt|1" q 'expand("%:t") . "|" . &readonly'

# --- a deleted file is reported once, not on every check
q "luaeval('(function() local o = vim.notify; _G.__cap = {}; vim.notify = function(m, ...) table.insert(_G.__cap, m); return o(m, ...) end; return 1 end)()')" >/dev/null
q "execute('messages clear')" >/dev/null
rm -f "$AT"/state/nvim/swap/*swapme* "$SW"
sleep 3.5
ok "deleted file reported once" \
	"$(q "luaeval('(function() local function n(list) return #vim.tbl_filter(function(m) return m:find(\"deleted on disk\") ~= nil end, list) end local c = n(_G.__cap) return c > 0 and c or n(vim.split(vim.fn.execute(\"messages\"), \"\\n\")) end)()')")" "1"
# (show another file first: wiping the only window's buffer would close the tab)
q "execute('silent! edit $A/src/app.txt | silent! bwipeout! $SW')" >/dev/null

# --- a diff review is never taken over; the edit waits; the key closes it
echo "uncommitted by the agent" >>"$A/src/app.txt"
focus_nvim
key "$(typ ':tabnext 2')0d"
key "$(typ ':AgentReview uncommitted')0d"
panel() { q "luaeval('(function() for _, w in ipairs(vim.api.nvim_list_wins()) do local b = vim.api.nvim_win_get_buf(w); if vim.bo[b].filetype == \"DiffviewFiles\" then return table.concat(vim.api.nvim_buf_get_lines(b, 0, -1, false), \" \") end end return \"\" end)()')"; }
panel_lists() { panel | grep -q 'app\.txt' && echo 1 || echo 0; }
for _ in $(seq 80); do [ "$(panel_lists)" = 1 ] && break; sleep 0.1; done
ok "AgentReview opens the review listing the uncommitted file" "$(panel_lists)" "1"
[ "$(panel_lists)" = 1 ] || echo "  panel: [$(panel)] tabs: $(tabcwds "$AG") msgs: $(q "execute('messages')" | tail -3 | tr '\n' '/')"
review_tab=$(q 'tabpagenr()')
hook "$AG" PostToolUse "$T/repo" "$B/README.md"
sleep 1
ok "diff review: your tab kept" "$(q 'tabpagenr()')" "$review_tab"
key "$(typ ':AgentReview uncommitted')0d"
eventually "the same key inside the review closes it" "1" q "luaeval('require(\"weaver.diffview\").current() == nil and 1 or 0')"
eventually "...and the waiting edit is followed" "repo-wt-b|README.md" nvst "$AG"
git -C "$A" checkout -q src/app.txt

Z go-to-tab-name follow-test >/dev/null 2>&1 && Z close-tab >/dev/null 2>&1
done_suite
