#!/bin/bash
# The spec end to end: root tracking, Alt v / Alt V, following, the dashboard.
. "$(dirname "$0")/../lib.sh"

edit_json() { printf '{"hook_event_name":"PostToolUse","cwd":"%s","session_id":"sid-0","tool_name":"Edit","tool_input":{"file_path":"%s"}}' "$1" "$2"; }

echo "edited by agent a" >>"$T/repo-wt-a/src/app.txt"
echo new >"$T/repo-wt-a/src/new.txt"
feed
ok "A root is worktree, not start dir" "$(st 0 root)" "$T/repo-wt-a"
ok "A start_cwd recorded" "$(st 0 start_cwd)" "$T/repo"
ok "C root via not-yet-existing dir" "$(st 2 root)" "$T/repo-wt-b"

key 1b76 # Alt v
sleep 3
NV=$(nvim_pane 0)
unstick 0
ok "Alt v: nvim pane recorded and focused" "$(client)" "terminal_$NV"
ok "Alt v: nvim left of agent" \
	"$(Z list-panes -a -j | jq -r "[.[] | select((.is_plugin|not) and (.id==$NV or .id==0))] | sort_by(.pane_x) | map(.id|tostring) | join(\",\")")" "$NV,0"
ok "Alt v: nvim in worktree on last edited file" "$(nvst 0)" "repo-wt-a|app.txt"

hook 0 PostToolUse "$T/repo" "$T/repo-wt-a/README.md"
sleep 0.7
ok "agent edit opens in nvim" "$(nvst 0)" "repo-wt-a|README.md"
printf 'REWRITTEN\n' >"$T/repo-wt-a/README.md"
sleep 1.7
ok "external rewrite reloads" "$(nvq 0 'getline(1)')" "REWRITTEN"

hook 0 UserPromptSubmit "$T/repo" "" "back at the start dir"
sleep 0.5
ok "cwd snapped back to start dir: root kept" "$(st 0 root)" "$T/repo-wt-a"
ok "...and nvim stays put" "$(nvst 0)" "repo-wt-a|README.md"
hook 0 UserPromptSubmit "$T/repo-wt-b" "" "now feature b"
sleep 0.7
ok "agent moves to wt-b: nvim cds" "$(nvst 0 | cut -d'|' -f1)" "repo-wt-b"
ok "nvim pane renamed" "$(Z list-panes -a -j | jq -r ".[] | select((.is_plugin|not) and .id==$NV) | .title")" "nvim: repo-wt-b"

nvq 0 'nvim_buf_set_lines(0, 0, 1, v:false, ["MY UNSAVED EDIT"])' >/dev/null
hook 0 PostToolUse "$T/repo-wt-b" "$T/repo-wt-b/README.md"
sleep 0.7
ok "unsaved buffer not replaced" \
	"$(nvq 0 'fnamemodify(bufname("%"), ":p:h:t") . "|" . getline(1) . "|" . &modified')" "repo-wt-a|MY UNSAVED EDIT|1"
nvsend 0 '<Esc>:e!<CR>'
sleep 0.5

key 1b6c # Alt l
sleep 0.5
ok "Alt l back to agent A" "$(client)" "terminal_0"
before=$(tabpanes 0)
key 1b76
sleep 2.5
ok "Alt v again reuses nvim" "$(client)|$(tabpanes 0)" "terminal_$NV|$before"

key 1b6c
sleep 0.5
nvsend 0 ':lua vim.uv.sleep(15000)<CR>'
sleep 0.5
edit_json "$T/repo-wt-b" "$T/repo-wt-b/README.md" >"$AT/in-edit.json"
timed_hook() { ms /bin/bash -c "ZELLIJ_PANE_ID=0 agentnav hook <'$AT/in-edit.json'"; }
t1=$(timed_hook)
t2=$(timed_hook)
t3=$(timed_hook)
echo "  stuck nvim: hook times ${t1}ms, ${t2}ms, ${t3}ms"
ok "stuck nvim: first hook bounded (<3s)" "$([ "$t1" -lt 3000 ] && echo y)" y
ok "stuck nvim: later hooks skip the call (<1s)" "$([ "$t2" -lt 1000 ] && [ "$t3" -lt 1000 ] && echo y)" y
before=$(tabpanes 0)
key 1b76
sleep 3.5
ok "Alt v on stuck nvim focuses it, no duplicate" "$(client)|$(tabpanes 0)" "terminal_$NV|$before"
sleep 10
key 1b6c
sleep 0.5

key 1b56 # Alt V
sleep 2.5
ok "Alt V focuses nvim fullscreen" \
	"$(Z list-panes -a -j | jq -r ".[] | select((.is_plugin|not) and .id==$NV) | .is_fullscreen")|$(client)" "true|terminal_$NV"
key 1b56
sleep 2

hook 0 Stop "$T/repo"
key 1b79 # Alt y
sleep 2.5
FL=$(Z list-panes -a -j | jq -r '.[] | select((.is_plugin|not) and .is_floating) | .id')
ok "dashboard order (needs-you, done, working)" \
	"$(Z dump-screen --pane-id "terminal_$FL" | grep -E '^ *>? +[!✓●○] ' | awk '{print ($1==">") ? $2 : $1}' | tr -d '\n')" "!✓●"
key 1b5b42
sleep 0.3
key 1b5b42
sleep 0.3
key 05 # ctrl-e
sleep 3.5
unstick 2
NVC=$(nvim_pane 2)
ok "ctrl-e on agent C (other tab): new nvim focused there" \
	"$(client)|$(Z list-panes -a -j | jq -r ".[] | select((.is_plugin|not) and .id==$NVC) | .tab_id")" "terminal_$NVC|1"
ok "...in C's worktree" "$(nvst 2 | cut -d'|' -f1)" "repo-wt-b"
key 1b79
sleep 2.5
key 1b5b42
sleep 0.3
key 0d
sleep 1.5
ok "Enter on agent A from tab two jumps across tabs" "$(client)" "terminal_0"
done_suite
