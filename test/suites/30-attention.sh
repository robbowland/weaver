#!/bin/bash
# Attention: the state machine (needs you, done, interrupted, compaction, and
# every notification type), the bell, Claude Code's own session status, the
# dashboard's triage details, Alt 1, the status bar, unread, and Worktrunk
# markers.
. "$(dirname "$0")/../lib.sh"
fresh
feed >/dev/null

j() { printf '{"hook_event_name":"%s","cwd":"%s","session_id":"sid-1"%s}' "$1" "$T/repo-wt-b" "${2:-}"; }
note() { j Notification ",\"notification_type\":\"$1\",\"message\":\"${2:-a notice}\""; }
bash_input=',"tool_name":"Bash","tool_input":{"command":"git push --force"}'
read_input=',"tool_name":"Read","tool_input":{"file_path":"/etc/hosts"}'
ask_input=',"tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Which branch?"}]}'
BELL='{"terminalSequence":"\u0007"}'

# --- states and the bell (agent B, pane 1)
hookj 1 "$(j UserPromptSubmit ',"prompt":"push it"')" >/dev/null
out=$(hookj 1 "$(j PermissionRequest "$bash_input")")
ok "permission dialog: needs you at once" "$(st 1 state)|$(st 1 waiting)" "needs-you|Bash: git push --force"
ok "...and rings the bell" "$out" "$BELL"
since=$(st 1 since)
sleep 1.1
out=$(hookj 1 "$(note permission_prompt 'Claude needs your permission to use Bash')")
ok "later permission notification: no second bell" "$out" ""
ok "...keeps the specific waiting text" "$(st 1 waiting)" "Bash: git push --force"
ok "...and the time it started waiting" "$(st 1 since)" "$since"
hookj 1 "$(j PostToolUse "$read_input")" --async
ok "another tool finishing: still needs you" "$(st 1 state)" "needs-you"
hookj 1 "$(j PostToolUse "$bash_input")" --async
ok "approved tool finishing: back to working" "$(st 1 state)|$(st 1 waiting)" "working|"
out=$(hookj 1 "$(j PreToolUse "$ask_input")")
ok "a question for you: needs you, with bell" "$(st 1 state)|$(st 1 waiting)|$out" "needs-you|question: Which branch?|$BELL"
hookj 1 "$(j PostToolUse "$ask_input")" --async
ok "question answered: working" "$(st 1 state)" "working"
out=$(hookj 1 "$(j Stop ',"last_assistant_message":"Pushed.\nAll done."')")
ok "Stop: done, last reply kept on one line" "$(st 1 state)|$(st 1 last_message)" "done|Pushed. All done."
ok "...and rings the bell: the tab is marked until you look" "$out" "$BELL"
out=$(WEAVER_BELL=needs-you hookj 1 "$(j UserPromptSubmit ',"prompt":"again"')"; WEAVER_BELL=needs-you hookj 1 "$(j Stop)")
ok "WEAVER_BELL=needs-you: no bell when done" "$out" ""
hookj 1 "$(note idle_prompt 'Claude is waiting for your input')"
ok "idle reminder after Stop: still done" "$(st 1 state)" "done"
out=$(hookj 1 "$(note agent_needs_input 'Teammate setup needs your input')")
ok "agent_needs_input: needs you" "$(st 1 state)|$out" "needs-you|$BELL"
hookj 1 "$(j StopFailure ',"error":"rate_limit"')"
ok "StopFailure: needs you, says why" "$(st 1 state)|$(st 1 waiting)" "needs-you|error: rate_limit"

# --- every notification type (defect 4)
hookj 1 "$(j UserPromptSubmit ',"prompt":"use the mcp"')" >/dev/null
out=$(hookj 1 "$(note elicitation_dialog 'An MCP server needs your input')")
ok "elicitation_dialog: needs you, with bell" "$(st 1 state)|$out" "needs-you|$BELL"
out=$(hookj 1 "$(note elicitation_response 'Elicitation response for server x: accept')")
ok "elicitation_response (you answered): working, no bell" "$(st 1 state)|$out" "working|"
out=$(hookj 1 "$(note elicitation_complete 'MCP server x confirmed elicitation 1 complete')")
ok "elicitation_complete: no bell, no change" "$(st 1 state)|$out" "working|"
out=$(hookj 1 "$(note worker_permission_prompt 'worker needs permission for Bash')")
ok "worker_permission_prompt: needs you" "$(st 1 state)|$out" "needs-you|$BELL"
hookj 1 "$(j PostToolBatch ',"tool_calls":[]')" --async
hookj 1 "$(j Stop)" >/dev/null
hookj 1 "$(note agent_completed 'a background agent finished')"
ok "agent_completed is about another agent: state unchanged" "$(st 1 state)" "done"
out=$(hookj 1 "$(note quota_auto_resume_stale 'Usage limit reset · press enter to continue')")
ok "quota_auto_resume_stale: needs you" "$(st 1 state)|$out" "needs-you|$BELL"
hookj 1 "$(note quota_auto_resume_fired 'resuming')" >/dev/null
ok "quota_auto_resume_fired: working" "$(st 1 state)" "working"
hookj 1 "$(note auth_success 'Claude Code login successful')"
ok "other notices leave the state alone" "$(st 1 state)" "working"

# --- a dialog you answered leaves needs-you, whatever happened to its tool
# (defect 3): the batch resolves (approved, or rejected with feedback)...
hookj 1 "$(j PermissionRequest "$bash_input")" >/dev/null
hookj 1 "$(j PostToolBatch ',"tool_calls":[{"tool_name":"Bash","tool_input":{"command":"git push --force"},"tool_use_id":"t1","tool_response":"The user doesn'"'"'t want to proceed with this tool use. The tool use was rejected (eg. if it was a file edit, the new_string was NOT written to the file). To tell you how to proceed, the user said: use --force-with-lease"}]')" --async
ok "rejected with feedback: the agent carries on" "$(st 1 state)" "working"
# ...or rejected outright: the agent stops and waits for you
hookj 1 "$(j PermissionRequest "$bash_input")" >/dev/null
hookj 1 "$(j PostToolBatch ',"tool_calls":[{"tool_name":"Bash","tool_input":{"command":"git push --force"},"tool_use_id":"t2","tool_response":"The user doesn'"'"'t want to proceed with this tool use. The tool use was rejected (eg. if it was a file edit, the new_string was NOT written to the file). STOP what you are doing and wait for the user to tell you how to proceed."}]')" --async
ok "rejected outright: interrupted" "$(st 1 state)" "interrupted"

# --- interrupts (defect 9): Esc during a tool, and Esc while it was writing
hookj 1 "$(j UserPromptSubmit ',"prompt":"long job"')" >/dev/null
hookj 1 "$(j PostToolUseFailure ',"tool_name":"Bash","tool_input":{"command":"false"},"error":"exit 1"')" --async
ok "a tool that merely fails: working" "$(st 1 state)" "working"
hookj 1 "$(j PostToolUseFailure ',"tool_name":"Bash","tool_input":{"command":"sleep 100"},"error":"Interrupted","is_interrupt":true')" --async
ok "Esc during a tool: interrupted" "$(st 1 state)" "interrupted"
hookj 1 "$(j UserPromptSubmit ',"prompt":"long job"')" >/dev/null
hookj 1 "$(note idle_prompt 'Claude is waiting for your input')"
ok "the idle reminder while 'working': it was stopped with Esc" "$(st 1 state)" "interrupted"

# --- compaction carries on; a new start, clear or resume is idle (defect 2)
hookj 1 "$(j UserPromptSubmit ',"prompt":"big refactor"')" >/dev/null
hookj 1 "$(j SessionStart ',"source":"compact"')" >/dev/null
ok "compaction mid-turn: still working" "$(st 1 state)|$(st 1 prompt)" "working|big refactor"
hookj 1 "$(j PostToolUse "$read_input")" --async
ok "...and the next tool call changes nothing" "$(st 1 state)" "working"
hookj 1 "$(j SessionStart ',"source":"resume"')" >/dev/null
ok "resume: idle" "$(st 1 state)" "idle"
turn=$(st 1 turn)
hookj 1 '{"hook_event_name":"SessionStart","source":"clear","cwd":"'"$T/repo-wt-b"'","session_id":"sid-1b"}' >/dev/null
ok "clear (a new session): idle, turns start over" "$(st 1 state)|$(st 1 turn)|$([ "$turn" -gt 0 ] && echo had)" "idle|0|had"
hookj 1 '{"hook_event_name":"UserPromptSubmit","prompt":"<task-notification>done</task-notification>","cwd":"'"$T/repo-wt-b"'","session_id":"sid-1b"}' >/dev/null
ok "a task notification is not your prompt: no turn" "$(st 1 turn)|$(st 1 state)" "0|working"
hookj 1 '{"hook_event_name":"UserPromptSubmit","prompt":"hi","source":"user","session_title":"Fix the readme","cwd":"'"$T/repo-wt-b"'","session_id":"sid-1b"}' >/dev/null
ok "your prompt: turn 1, and the session title" "$(st 1 turn)|$(st 1 title)" "1|Fix the readme"

# --- Claude Code's own session status refines what hooks can't see
live_pid
P1=$LIVE_PID
pj() { printf '{"hook_event_name":"%s","cwd":"%s","session_id":"sid-live"%s}' "$1" "$T/repo-wt-b" "${2:-}"; }
lhook() { printf '%s' "$1" | CLAUDE_PID=$P1 ZELLIJ_PANE_ID=1 weaver hook ${2:-} >/dev/null; }
lhook "$(pj SessionStart ',"source":"startup"')"
lhook "$(pj UserPromptSubmit ',"prompt":"run the slow tests"')"
lhook "$(pj PermissionRequest "$bash_input")"
sleep 1.1
claude_session "$P1" sid-live busy
ok "a long tool you approved: working, though no hook said so" "$(weaver ls | grep -c '^● .*run the slow tests')" "1"
lhook "$(pj PostToolUse "$bash_input")" --async
ok "...and working once it finishes" "$(st 1 state)" "working"
sleep 1.1
claude_session "$P1" sid-live waiting "$(($(date +%s) * 1000))" "dialog open"
ok "a dialog no hook reported: needs you" "$(weaver ls | grep -c '^! .*dialog open')" "1"
jq -c '.ts -= 30' "$SD/${SESSION}_1.json" >"$AT/c.json" && mv "$AT/c.json" "$SD/${SESSION}_1.json"
claude_session "$P1" sid-live idle $((($(date +%s) - 10) * 1000))
ok "quiet, but Claude Code is idle: stopped with Esc" "$(weaver ls | grep -c '^○ .*interrupted')" "1"
kill "$P1"

# --- the dashboard: what each agent waits on (looking at C: a done agent you
# are looking at counts as seen, so look elsewhere)
fresh
focus 2
feed >/dev/null
out=$(hookj 1 "$(j PermissionRequest "$bash_input")")
table=$(weaver ls)
ok "dashboard: needs-you row shows what it waits on" "$(printf '%s\n' "$table" | grep -c '^! .*Bash: git push --force')" "1"
hook 0 Stop "$T/repo" >/dev/null
ok "Stop resets seen" "$(st 0 seen)" "false"

# --- Alt 1: longest-waiting needs-you first, then unseen done, round and round
focus 2
key 1b31 # Alt 1
sleep 2
ok "Alt 1 jumps to the agent that needs you" "$(client)" "terminal_1"
key 1b31
sleep 2
ok "Alt 1 again goes on to the unseen finished agent" "$(client)|$(st 0 seen)" "terminal_0|true"
key 1b31
sleep 2
ok "...and round again to the one still waiting" "$(client)" "terminal_1"
hookj 1 "$(j PostToolUse "$bash_input")" --async
key 1b31
sleep 2
ok "Alt 1 with nothing waiting stays put" "$(client)" "terminal_1"
hook 2 PermissionRequest "$T/repo" >/dev/null
sleep 1
hook 0 PermissionRequest "$T/repo" >/dev/null
focus 1
key 1b31
sleep 2
ok "two agents waiting: Alt 1 takes the longest waiting first" "$(client)" "terminal_2"
key 1b31
sleep 2
ok "...then the other" "$(client)" "terminal_0"
key 1b31
sleep 2
ok "...and cycles back" "$(client)" "terminal_2"

# --- the status bar: counts, pushed to zjstatus, and unread
hook 0 Stop "$T/repo" >/dev/null
hook 1 UserPromptSubmit "$T/repo-wt-b" "" "busy" >/dev/null
ok "status: needs you, done and unseen, working" "$(weaver status)" "! 1  ✓ 1  ● 1"
settle
ok "status for zjstatus, kept by the hooks: Micrographics signal colours" "$(weaver status --zjstatus)" \
	"#[fg=#ff3b2f,bold]! 1 #[fg=#39d97a,bold]✓ 1 #[fg=#999999]● 1 "
focus 0
weaver status --push
ok "a done agent you are looking at counts as seen" "$(st 0 seen)|$(weaver status)" "true|! 1  ● 1"
ok "...in zjstatus too" "$(weaver status --zjstatus)" "#[fg=#ff3b2f,bold]! 1 #[fg=#999999]● 1 "
printf 'garbage\n' >"$SD/$SESSION.status"
ok "zjstatus widget: a bad counts file prints nothing, status 0" "$(weaver status --zjstatus; echo "rc=$?")" "
rc=0"
ok "...and outside zellij too" "$(ZELLIJ_SESSION_NAME= weaver status --zjstatus; echo "rc=$?")" "
rc=0"

# --- zellij marks a tab whose pane rings the bell (what terminalSequence does)
Z write-chars --pane-id terminal_2 "printf '\\a'
" >/dev/null
sleep 1
ok "bell in an unfocused tab marks it" "$(Z list-tabs -a -j | jq -r '.[] | select(.tab_id == 1) | .has_bell_notification')" "true"

# --- Worktrunk marker on the worktree really being edited (A edits worktree a)
if command -v wt >/dev/null 2>&1; then
	hook 0 UserPromptSubmit "$T/repo" "" "more work" >/dev/null
	hook 0 PostToolUse "$T/repo" "$T/repo-wt-a/src/app.txt"
	settle
	ok "working agent marks its real branch" "$(wt -C "$T/repo-wt-a" config state marker get 2>/dev/null)" "🤖"
	ok "...not the start directory's branch" "$(wt -C "$T/repo" config state marker get 2>/dev/null)" ""
	hook 0 Stop "$T/repo" >/dev/null
	settle
	ok "done agent: marker says it is waiting for you" "$(wt -C "$T/repo-wt-a" config state marker get 2>/dev/null)" "💬"
	hook 0 SessionEnd "$T/repo" >/dev/null
	settle
	ok "session end clears the marker" "$(wt -C "$T/repo-wt-a" config state marker get 2>/dev/null)" ""
else
	echo "SKIP Worktrunk markers: wt is not installed"
fi
done_suite
