#!/bin/bash
# The core: where each agent really works, Alt v / Alt V placing and reusing
# its nvim, a stuck nvim never blocking a hook, the dashboard. What nvim does
# with the agent's edits is tested in the 5x suites.
. "$(dirname "$0")/../lib.sh"
fresh

edit_json() { printf '{"hook_event_name":"PostToolUse","cwd":"%s","session_id":"sid-0","tool_name":"Edit","tool_input":{"file_path":"%s"}}' "$1" "$2"; }

echo "edited by agent a" >>"$T/repo-wt-a/src/app.txt"
echo new >"$T/repo-wt-a/src/new.txt"
feed
ok "A root is worktree, not start dir" "$(st 0 root)" "$T/repo-wt-a"
ok "A start_cwd recorded" "$(st 0 start_cwd)" "$T/repo"
ok "C root via not-yet-existing dir" "$(st 2 root)" "$T/repo-wt-b"
ok "edits are listed as the agent's files" "$(st 0 'files | map(split("/") | last) | join(",")')" "app.txt"

focus 0
key 1b76 # Alt v
sleep 3
NV=$(nvim_pane 0)
unstick 0
ok "Alt v: nvim pane recorded and focused" "$(client)" "terminal_$NV"
ok "Alt v: nvim left of agent" \
	"$(Z list-panes -a -j | jq -r "[.[] | select((.is_plugin|not) and (.id==$NV or .id==0))] | sort_by(.pane_x) | map(.id|tostring) | join(\",\")")" "$NV,0"
ok "Alt v: nvim in worktree on last edited file" "$(nvst 0)" "repo-wt-a|app.txt"
ok "Alt v: nvim's pid recorded beside its pane" "$(sed -n 2p "$SD/${SESSION}_0.nvim" | grep -c '^[0-9][0-9]*$')" "1"
ok "...and nvim knows its agent" "$(nvq 0 '$WEAVER_PANE . "|" . fnamemodify($WEAVER_STATE, ":t")')" "0|${SESSION}_0.json"

hook 0 UserPromptSubmit "$T/repo" "" "back at the start dir"
ok "cwd snapped back to start dir: root kept" "$(st 0 root)" "$T/repo-wt-a"
hook 0 UserPromptSubmit "$T/repo-wt-b" "" "now feature b"
ok "agent moves to wt-b: root follows" "$(st 0 root)" "$T/repo-wt-b"
settle
ok "nvim pane renamed" "$(Z list-panes -a -j | jq -r ".[] | select((.is_plugin|not) and .id==$NV) | .title")" "nvim: repo-wt-b"

focus 0
ok "back to agent A" "$(client)" "terminal_0"
before=$(tabpanes 0)
key 1b76
sleep 2.5
ok "Alt v again reuses nvim" "$(client)|$(tabpanes 0)" "terminal_$NV|$before"

focus 0
nvsend 0 ':lua vim.uv.sleep(15000)<CR>'
sleep 0.5
edit_json "$T/repo-wt-b" "$T/repo-wt-b/README.md" >"$AT/in-edit.json"
timed_hook() { ms /bin/bash -c "ZELLIJ_PANE_ID=0 weaver hook ${1:-} <'$AT/in-edit.json' >/dev/null"; }
t1=$(timed_hook)
t2=$(timed_hook --async)
t3=$(timed_hook --async)
echo "  stuck nvim: hook times ${t1}ms, ${t2}ms, ${t3}ms"
ok "stuck nvim: no hook waits on it (<1s)" "$([ "$t1" -lt 1000 ] && [ "$t2" -lt 1000 ] && [ "$t3" -lt 1000 ] && echo y)" y
before=$(tabpanes 0)
key 1b76
sleep 3.5
ok "Alt v on stuck nvim focuses it, no duplicate" "$(client)|$(tabpanes 0)" "terminal_$NV|$before"
sleep 10
focus 0

key 1b56 # Alt V
sleep 2.5
ok "Alt V focuses nvim fullscreen" \
	"$(Z list-panes -a -j | jq -r ".[] | select((.is_plugin|not) and .id==$NV) | .is_fullscreen")|$(client)" "true|terminal_$NV"
key 1b56
sleep 2

hook 0 Stop "$T/repo"
hook 2 PostToolUse "$T/repo" "$T/repo-wt-b/newdir/sub/new.txt" # C still busy
key 1b79 # Alt y
sleep 2.5
FL=$(Z list-panes -a -j | jq -r '.[] | select((.is_plugin|not) and .is_floating) | .id')
ok "dashboard order (needs-you, done, working)" \
	"$(Z dump-screen --pane-id "terminal_$FL" | grep -E '^ +[1-9] [!✓●○◌] ' | awk '{print $2}' | tr -d '\n')" "!✓●"
ok "dashboard listens for live reloads" "$(ls /tmp/weaver-$(id -u)/ | grep -c "^${SESSION}\.fzf\..*\.sock$")" "1"
hook 1 PermissionRequest "$T/repo-wt-b"
hookj 1 "{\"hook_event_name\":\"Stop\",\"cwd\":\"$T/repo-wt-b\",\"session_id\":\"sid-1\"}" >/dev/null
settle
sleep 0.5
ok "an agent finishing reloads the open dashboard" \
	"$(Z dump-screen --pane-id "terminal_$FL" | grep -E '^ +[1-9] [!✓●○◌] ' | awk '{print $2}' | tr -d '\n')" "✓✓●"
key 03 # ctrl-c: close it
sleep 1
ok "...and its socket goes with it" "$(ls /tmp/weaver-$(id -u)/ | grep -c "^${SESSION}\.fzf\..*\.sock$")" "0"
key 1b79
sleep 2.5
key 1b5b42 # down
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
key 0d # A is first: done and unseen, like B, and its branch sorts first
sleep 1.5
ok "Enter on agent A from tab two jumps across tabs" "$(client)" "terminal_0"
ok "...and marks it seen" "$(st 0 seen)" "true"
done_suite
