#!/bin/bash
# Failure modes found in review: other sessions' files, stale swap files,
# deleted files, diff reviews, symlinked paths. Runs after 10-core.sh.
. "$(dirname "$0")/../lib.sh"

rows() { weaver ls | grep -c -E '^[!✓●○] '; }

# The harness itself: zellij falls back to some other running session when the
# named one is gone, so the test shim must refuse.
ZELLIJ_SESSION_NAME=weaver-no-such-session zellij action list-tabs >/dev/null 2>&1
ok "test shim refuses actions on a session that isn't running" "$?" "1"
capture_notify() {
	nvq 0 "luaeval('(function() local o = vim.notify; _G.__cap = {}; vim.notify = function(m, ...) table.insert(_G.__cap, m); return o(m, ...) end; return 1 end)()')" >/dev/null
}

# A session whose name extends ours must not be listed or garbage-collected.
cp "$SD/${SESSION}_1.json" "$SD/${SESSION}_x_12.json"
ok "other session's file not listed" "$(rows)" "3"
ok "other session's file not deleted" "$([ -f "$SD/${SESSION}_x_12.json" ] && echo kept)" "kept"
rm -f "$SD/${SESSION}_x_12.json"

# A stale swap file must not raise the blocking ATTENTION dialog.
SW=$T/repo-wt-a/src/swapme.txt
printf 'swap me\n' >"$SW"
XDG_STATE_HOME=$AT/state nvim --clean --headless "+normal Gochanged" "+sleep 30" "$SW" &
sp=$!
sleep 1.5
kill -9 $sp 2>/dev/null
wait $sp 2>/dev/null
ok "swap file left behind" "$(ls "$AT/state/nvim/swap/" 2>/dev/null | grep -c swapme)" "1"
hook 0 PostToolUse "$T/repo" "$SW"
sleep 1
ok "follow into swapped file: nvim responsive, read-only" "$(nvq 0 'fnamemodify(bufname("%"), ":t") . "|" . &readonly')" "swapme.txt|1"

# Same at startup (agent B has no nvim yet).
Z new-pane --close-on-exit -- "$AT/bin/weaver" nvim 1 "$SW" >/dev/null
sleep 4
unstick 1
ok "startup on swapped file: responsive, read-only" "$(nvq 1 'fnamemodify(bufname("%"), ":t") . "|" . &readonly')" "swapme.txt|1"
ok "startup swap override removed afterwards" "$(nvq 1 'exists("#weaver_start")')" "0"
nvsend 1 '<Esc>:qa!<CR>'
sleep 1

# A deleted file is reported once, not on every check.
capture_notify
rm -f "$AT"/state/nvim/swap/*swapme* "$SW"
sleep 3.5
ok "deleted file reported once, not every second" \
	"$(nvq 0 "luaeval('#vim.tbl_filter(function(m) return m:find(\"deleted on disk\") ~= nil end, _G.__cap)')")" "1"

# AgentReview includes uncommitted work; following leaves a diff review alone.
nvsend 0 "<Esc>:cd $T/repo-wt-a<CR>:AgentReview<CR>"
sleep 3
ok "AgentReview lists uncommitted app.txt" \
	"$(Z dump-screen --pane-id "terminal_$(nvim_pane 0)" | grep -q 'app.txt' && echo listed)" "listed"
# tiled windows only: a notification popup is a floating window in this tab
wins() {
	nvq 0 "luaeval('vim.fn.tabpagenr() .. \"|\" .. table.concat(vim.tbl_map(function(w) return vim.fn.fnamemodify(vim.api.nvim_buf_get_name(vim.api.nvim_win_get_buf(w)), \":t\") .. (vim.wo[w].diff and \"1\" or \"0\") end, vim.tbl_filter(function(w) return vim.api.nvim_win_get_config(w).relative == \"\" end, vim.api.nvim_tabpage_list_wins(0))), \",\")')"
}
before=$(wins)
hook 0 PostToolUse "$T/repo" "$T/repo-wt-a/README.md"
sleep 1
ok "following leaves the diff tab's windows alone" "$(wins)" "$before"
ok "...and says why" "$(nvq 0 "luaeval('_G.__cap[#_G.__cap]')")" "weaver: not opening README.md: you are reviewing a diff"
nvsend 0 '<Esc>:DiffviewClose<CR>'
sleep 1

# A path through a symlink (like /tmp vs /private/tmp) is not dropped by edit.
ln -s "$T" "$AT/tlink"
hook 0 PostToolUse "$AT/tlink/repo" "$AT/tlink/repo-wt-a/src/app.txt"
sleep 0.8
nvsend 0 "<Esc>:e! $T/repo-wt-a/README.md<CR>"
sleep 0.8
for _ in 1 2 3; do
	[ "$(client)" = terminal_0 ] && break
	key 1b6c
	sleep 0.4
done
key 1b76
sleep 2.5
ok "Alt v with a symlinked path reopens the agent's file" "$(nvq 0 'fnamemodify(bufname("%"), ":t")')" "app.txt"
done_suite
