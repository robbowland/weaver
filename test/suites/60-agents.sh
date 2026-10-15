#!/bin/bash
# Agents other than Claude Code: `weaver event` (normalized JSON) and the Codex
# adapter (`weaver adapt codex`, Codex's own hook input).
. "$(dirname "$0")/../lib.sh"
fresh

# --- weaver event: a made-up agent in pane 2
live_pid
P=$LIVE_PID
ev() { printf '%s' "$1" | as_pane 2 weaver event; }
base='"agent":"pi","session":"pi-1","cwd":"'"$T/repo"'","pid":'"$P"
ev "{$base,\"event\":\"session_start\",\"source\":\"startup\"}"
ok "event: session start makes an idle agent of the given kind" "$(st 2 state)|$(st 2 agent)|$(st 2 pid)" "idle|pi|$P"
ev "{$base,\"event\":\"prompt\",\"prompt\":\"tidy the readme\"}"
ok "event: a prompt is a turn" "$(st 2 state)|$(st 2 turn)|$(st 2 prompt)" "working|1|tidy the readme"
ev "{$base,\"event\":\"needs_you\",\"waiting\":\"run rm -rf build?\"}"
ok "event: needs you, with what it waits on" "$(st 2 state)|$(st 2 waiting)" "needs-you|run rm -rf build?"
ev "{$base,\"event\":\"tool_done\",\"tool\":\"write\",\"file\":\"$T/repo-wt-b/README.md\"}"
ok "event: a tool that edited a file: back to working, root and file follow" \
	"$(st 2 state)|$(st 2 root)|$(st 2 file)" "working|$T/repo-wt-b|$T/repo-wt-b/README.md"
ev "{$base,\"event\":\"tool_done\",\"tool\":\"bash\",\"command\":\"ls\"}"
sleep 0.1
printf 'by bash\n' >>"$T/repo-wt-b/src/app.txt"
ev "{$base,\"event\":\"tool_done\",\"tool\":\"bash\",\"command\":\"echo by bash >> src/app.txt\",\"paths\":[\"$T/repo-wt-b\"]}"
ok "event: a tool without a file: its changes are found like a Bash edit's" "$(st 2 file)" "$T/repo-wt-b/src/app.txt"
ev "{$base,\"event\":\"stop\",\"message\":\"Tidied.\"}"
ok "event: stop is done, with the last reply" "$(st 2 state)|$(st 2 last_message)" "done|Tidied."
ok "event: the dashboard lists it" "$(weaver ls | grep -c 'tidy the readme')" "1"
ev "{$base,\"event\":\"prompt\",\"prompt\":\"more\"}"
ev "{$base,\"event\":\"interrupt\"}"
ok "event: interrupt" "$(st 2 state)" "interrupted"
ev "{$base,\"event\":\"error\",\"error\":\"rate limited\"}"
ok "event: error needs you" "$(st 2 state)|$(st 2 waiting)" "needs-you|error: rate limited"
ev '{"event":"stop","session":"pi-1"}'
ok "event: no agent named: ignored" "$(st 2 state)" "needs-you"
ev "{$base,\"event\":\"session_end\"}"
ok "event: session end" "$(st 2 state)" "ended"
settle
ok "event: the activity log has it all" \
	"$(jq -r .ev "$(st 2 log)" | sort -u | tr '\n' ' ')" "bash edit end error interrupt needs-you prompt session snapshot state stop "
kill "$P"

# --- the Codex adapter: Codex's hook input, as Codex sends it
fresh
live_pid
CX=$LIVE_PID
cx() { printf '%s' "$1" | WEAVER_AGENT_PID=$CX as_pane 1 weaver adapt codex; }
cb='"session_id":"cx-1","transcript_path":null,"cwd":"'"$T/repo-wt-b"'","model":"gpt-5","permission_mode":"default"'
cx "{$cb,\"hook_event_name\":\"SessionStart\",\"source\":\"startup\"}"
ok "codex: session start" "$(st 1 state)|$(st 1 agent)" "idle|codex"
cx "{$cb,\"turn_id\":\"t1\",\"hook_event_name\":\"UserPromptSubmit\",\"prompt\":\"fix the tests\"}"
ok "codex: prompt" "$(st 1 state)|$(st 1 turn)|$(st 1 prompt)" "working|1|fix the tests"
cx "{$cb,\"turn_id\":\"t1\",\"hook_event_name\":\"PermissionRequest\",\"tool_name\":\"shell\",\"tool_input\":{\"command\":[\"git\",\"push\"]}}"
ok "codex: a permission request needs you" "$(st 1 state)|$(st 1 waiting)" "needs-you|shell: git push"
patch='*** Begin Patch\n*** Update File: '"$T/repo-wt-b/src/app.txt"'\n@@\n-line one\n+line 1\n*** End Patch'
cx "{$cb,\"turn_id\":\"t1\",\"hook_event_name\":\"PostToolUse\",\"tool_name\":\"apply_patch\",\"tool_use_id\":\"u1\",\"tool_input\":{\"input\":\"$patch\"},\"tool_response\":{}}"
ok "codex: apply_patch: back to working, its file followed" "$(st 1 state)|$(st 1 file)" "working|$T/repo-wt-b/src/app.txt"
cx "{$cb,\"turn_id\":\"t1\",\"hook_event_name\":\"SubagentStart\",\"agent_id\":\"w1\",\"agent_type\":\"worker\"}"
ok "codex: subagents" "$(st 1 'subagents | map(.type) | join(",")')" "worker"
cx "{$cb,\"turn_id\":\"t1\",\"hook_event_name\":\"Interrupt\"}"
ok "codex: Interrupt" "$(st 1 state)" "interrupted"
cx "{$cb,\"turn_id\":\"t2\",\"hook_event_name\":\"UserPromptSubmit\",\"prompt\":\"again\"}"
cx "{$cb,\"turn_id\":\"t2\",\"hook_event_name\":\"Stop\",\"stop_hook_active\":false,\"last_assistant_message\":\"All green.\"}"
ok "codex: Stop" "$(st 1 state)|$(st 1 last_message)|$(st 1 turn)" "done|All green.|2"
cx "{$cb,\"hook_event_name\":\"SessionEnd\",\"reason\":\"other\"}"
ok "codex: SessionEnd" "$(st 1 state)" "ended"
kill "$CX"
done_suite
