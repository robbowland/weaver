#!/bin/bash
# Needs-you detection, the bell, the dashboard's triage details, Alt 1,
# reloading, and Worktrunk markers. Runs after 10-core.sh and 20-robustness.sh.
. "$(dirname "$0")/../lib.sh"

j() { printf '{"hook_event_name":"%s","cwd":"%s","session_id":"sid-1"%s}' "$1" "$T/repo-wt-b" "${2:-}"; }
bash_input=',"tool_name":"Bash","tool_input":{"command":"git push --force"}'
read_input=',"tool_name":"Read","tool_input":{"file_path":"/etc/hosts"}'
ask_input=',"tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Which branch?"}]}'

# --- states and the bell (agent B, pane 1)
hookj 1 "$(j UserPromptSubmit ',"prompt":"push it"')" >/dev/null
out=$(hookj 1 "$(j PermissionRequest "$bash_input")")
ok "permission dialog: needs you at once" "$(st 1 state)|$(st 1 waiting)" "needs-you|Bash: git push --force"
ok "...and rings the bell" "$out" '{"terminalSequence":"\u0007"}'
since=$(st 1 since)
sleep 1.1
out=$(hookj 1 "$(j Notification ',"notification_type":"permission_prompt","message":"Claude needs your permission to use Bash"')")
ok "later permission notification: no second bell" "$out" ""
ok "...keeps the specific waiting text" "$(st 1 waiting)" "Bash: git push --force"
ok "...and the time it started waiting" "$(st 1 since)" "$since"
hookj 1 "$(j PostToolUse "$read_input")" --async
ok "another tool finishing: still needs you" "$(st 1 state)" "needs-you"
hookj 1 "$(j PostToolUse "$bash_input")" --async
ok "approved tool finishing: back to working" "$(st 1 state)|$(st 1 waiting)" "working|"
out=$(hookj 1 "$(j PreToolUse "$ask_input")")
ok "a question for you: needs you, with bell" "$(st 1 state)|$(st 1 waiting)|$out" \
	'needs-you|question: Which branch?|{"terminalSequence":"\u0007"}'
hookj 1 "$(j PostToolUse "$ask_input")" --async
ok "question answered: working" "$(st 1 state)" "working"
hookj 1 "$(j Stop ',"last_assistant_message":"Pushed.\nAll done."')"
ok "Stop: done, last reply kept on one line" "$(st 1 state)|$(st 1 last_message)" "done|Pushed. All done."
hookj 1 "$(j Notification ',"notification_type":"idle_prompt","message":"Claude is waiting for your input"')"
ok "idle reminder after Stop: still done" "$(st 1 state)" "done"
out=$(hookj 1 "$(j Notification ',"notification_type":"agent_needs_input","message":"Teammate setup needs your input"')")
ok "agent_needs_input: needs you" "$(st 1 state)|$out" 'needs-you|{"terminalSequence":"\u0007"}'
hookj 1 "$(j StopFailure ',"error":"rate_limit"')"
ok "StopFailure: needs you, says why" "$(st 1 state)|$(st 1 waiting)" "needs-you|error: rate_limit"
out=$(hookj 1 "$(j Stop)")
ok "Stop never rings" "$out" ""

# --- reload nvim after Bash (agent A has an nvim from 10-core.sh)
nvq 0 "luaeval('(function() local o = AgentChecktime; _G.__ct = 0; AgentChecktime = function() _G.__ct = _G.__ct + 1; return o() end; return 1 end)()')" >/dev/null
printf '{"hook_event_name":"PostToolUse","cwd":"%s","session_id":"sid-0","tool_name":"Bash","tool_input":{"command":"sed -i.bak s/a/b/ x"}}' "$T/repo" |
	ZELLIJ_PANE_ID=0 weaver hook --async
ok "Bash finished: agent's nvim told to re-check files" "$(nvq 0 "luaeval('_G.__ct')")" "1"
nvsend 0 "<Esc>:e! $T/repo-wt-a/src/app.txt<CR>"
sleep 0.5
printf 'watched rewrite\n' >"$T/repo-wt-a/src/app.txt"
sleep 0.5
ok "watcher reloads an outside change within 0.5s" "$(nvq 0 'getline(1)')" "watched rewrite"

# --- dashboard: waiting text, seen/unseen, interrupted
out=$(hookj 1 "$(j PermissionRequest "$bash_input")")
hook 2 UserPromptSubmit "$T/repo" "" "keep going"
jq '.ts -= 120' "$SD/${SESSION}_2.json" >"$AT/c.json" && mv "$AT/c.json" "$SD/${SESSION}_2.json"
table=$(weaver ls)
ok "dashboard: needs-you row shows what it waits on" "$(printf '%s\n' "$table" | grep -c '^! .*Bash: git push --force')" "1"
ok "dashboard: quiet working agent without Claude's footer is flagged" "$(printf '%s\n' "$table" | grep -c 'interrupted? keep going')" "1"
hook 0 Stop "$T/repo"
ok "Stop resets seen" "$(st 0 seen)" "false"

# --- Alt 1: longest-waiting needs-you first, then unseen done
key 1b6c
sleep 0.3
key 1b31 # Alt 1
sleep 2
ok "Alt 1 jumps to the agent that needs you" "$(client)" "terminal_1"
hookj 1 "$(j PostToolUse "$bash_input")" --async
key 1b31
sleep 2
ok "Alt 1 then jumps to the unseen finished agent" "$(client)|$(st 0 seen)" "terminal_0|true"
key 1b31
sleep 2
ok "Alt 1 with nothing waiting stays put" "$(client)" "terminal_0"

# --- zellij marks a tab whose pane rings the bell (what terminalSequence does)
Z write-chars --pane-id terminal_2 "printf '\\a'
" >/dev/null
sleep 1
ok "bell in an unfocused tab marks it" "$(Z list-tabs -a -j | jq -r '.[] | select(.tab_id == 1) | .has_bell_notification')" "true"

# --- Worktrunk marker on the worktree really being edited (A edits worktree a)
hook 0 UserPromptSubmit "$T/repo" "" "more work"
hook 0 PostToolUse "$T/repo" "$T/repo-wt-a/src/app.txt"
sleep 1
ok "working agent marks its real branch" "$(wt -C "$T/repo-wt-a" config state marker get 2>/dev/null)" "🤖"
ok "...not the start directory's branch" "$(wt -C "$T/repo" config state marker get 2>/dev/null)" ""
hook 0 Stop "$T/repo"
sleep 1
ok "done agent: marker says it is waiting for you" "$(wt -C "$T/repo-wt-a" config state marker get 2>/dev/null)" "💬"
hook 0 SessionEnd "$T/repo"
sleep 1
ok "session end clears the marker" "$(wt -C "$T/repo-wt-a" config state marker get 2>/dev/null)" ""
done_suite
