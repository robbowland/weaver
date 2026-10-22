#!/bin/bash
# Agents other than Claude Code: `wvr event` (normalized JSON) and the Codex
# adapter (`wvr adapt codex`, Codex's own hook input).
. "$(dirname "$0")/../lib.sh"
fresh

# --- wvr event: a made-up agent in pane 2
live_pid
P=$LIVE_PID
ev() { printf '%s' "$1" | as_pane 2 wvr event; }
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
ok "event: the dashboard lists it" "$(wvr ls | grep -c 'tidy the readme')" "1"
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
# cx JSON: a hook as Codex runs one in its own process: from inside its pane
# (pane 1's shell).
cx() {
	printf '%s' "$1" >"$AT/cx.json"
	rm -f "$AT/cx.done"
	tmux -S "$TSOCK" send-keys -t %1 "WEAVER_AGENT_PID=$CX wvr adapt codex <'$AT/cx.json'; touch '$AT/cx.done'" Enter
	for _ in $(seq 60); do [ -f "$AT/cx.done" ] && break; sleep 0.05; done
}
cb='"session_id":"cx-1","transcript_path":null,"cwd":"'"$T/repo-wt-b"'","model":"gpt-5","permission_mode":"default"'
printf '{"num":3,"name":"cx","agent":"codex","pane":1}\n' >"$SD/${SK}_1.thread"
cx "{$cb,\"hook_event_name\":\"SessionStart\",\"source\":\"startup\"}"
ok "codex: session start; its thread learns its conversation" "$(st 1 state)|$(st 1 agent)|$(jq -r .session_id "$SD/${SK}_1.thread")" "idle|codex|cx-1"
cx "{$cb,\"turn_id\":\"t1\",\"hook_event_name\":\"UserPromptSubmit\",\"prompt\":\"fix the tests\"}"
ok "codex: prompt" "$(st 1 state)|$(st 1 turn)|$(st 1 prompt)" "working|1|fix the tests"
cx "{$cb,\"turn_id\":\"t1\",\"hook_event_name\":\"PermissionRequest\",\"tool_name\":\"shell\",\"tool_input\":{\"command\":[\"git\",\"push\"]}}"
ok "codex: a permission request needs you" "$(st 1 state)|$(st 1 waiting)" "needs-you|shell: git push"
cx "{$cb,\"turn_id\":\"t1\",\"hook_event_name\":\"PostToolUse\",\"tool_name\":\"shell\",\"tool_input\":{\"command\":[\"ls\"]},\"tool_response\":{}}"
ok "...another call ending while it waits leaves it waiting" "$(st 1 state)|$(st 1 waiting)" "needs-you|shell: git push"
cx "{$cb,\"turn_id\":\"t1\",\"hook_event_name\":\"PostToolUse\",\"tool_name\":\"shell\",\"tool_input\":{\"command\":[\"git\",\"push\"]},\"tool_response\":{}}"
ok "...its own call ending: working again" "$(st 1 state)" "working"
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
ok "codex: SessionEnd, and why" "$(st 1 state)|$(st 1 end_reason)" "ended|other"
kill "$CX"

# --- Codex runs every session's hooks in its app server, with the
# environment of whichever session started it: a hook from outside the pane
# its TMUX_PANE names finds its pane by its session, bound from then on.
fresh
printf 'package main\n\nimport "time"\n\nfunc main() { time.Sleep(time.Hour) }\n' >"$AT/codex.go"
go build -o "$AT/codex" "$AT/codex.go"
CXP=$(tmux -S "$TSOCK" split-window -d -P -F '#{pane_id}' -t %0 -c "$T/repo-wt-b" -- "$AT/codex" | tr -d '%')
sleep 0.3
cd2() { printf '%s' "{\"session_id\":\"cx-2\",\"cwd\":\"$1\",$2}" | as_pane 1 wvr adapt codex; }
cd2 "$T/repo-wt-b" '"hook_event_name":"SessionStart","source":"startup"'
ok "codex in its app server: the event goes to its session's pane, its agent that pane's codex, not the pane its environment names" \
	"$(st "$CXP" state)|$(st "$CXP" agent)|$(st "$CXP" pid)|$([ -f "$SD/${SK}_1.json" ] && echo pane-1 || echo none)" \
	"idle|codex|$(tmux -S "$TSOCK" display -p -t "%$CXP" '#{pane_pid}')|none"
cd2 "$T/repo" '"turn_id":"t1","hook_event_name":"UserPromptSubmit","prompt":"look around"'
ok "...bound to its session: an event from elsewhere goes there too" "$(st "$CXP" state)|$(st "$CXP" turn)" "working|1"
cd2 "$T/repo" '"hook_event_name":"SessionEnd","reason":"other"'
ok "...and the binding goes with the session" "$(st "$CXP" state)|$([ -f "$SD/codex/cx-2" ] && echo kept || echo gone)" "ended|gone"
# The app server's environment is a server that has gone ($TMUX's pid and
# session): the state says the pane's server and session, not the daemon's.
printf '%s' "{\"session_id\":\"cx-3\",\"cwd\":\"$T/repo-wt-b\",\"hook_event_name\":\"SessionStart\",\"source\":\"startup\"}" |
	TMUX="$TSOCK,1,7" as_pane 1 wvr adapt codex
ok "...its tmux server and session are its pane's, not the app server's environment's" \
	"$(st "$CXP" server)|$(st "$CXP" tmux_session)" \
	"$(tmux -S "$TSOCK" display -p -t "%$CXP" '#{pid}|#{session_id}')"
# An event it cannot place (no Codex pane in its directory) is no pane's:
# never the one its environment names.
printf '%s' "{\"session_id\":\"cx-4\",\"cwd\":\"$AT\",\"hook_event_name\":\"SessionStart\",\"source\":\"startup\"}" | as_pane 1 wvr adapt codex
ok "...an event it cannot place goes nowhere" "$([ -f "$SD/${SK}_1.json" ] && echo pane-1 || echo none)|$(ls "$SD/codex" 2>/dev/null | grep -c cx-4)" "none|0"
# A binding another server made (or one from before bindings said their
# server) names a pane number that means nothing here: the session goes by
# its directory, not to the pane of that number.
CXQ=$(tmux -S "$TSOCK" split-window -d -P -F '#{pane_id}' -t %0 -c "$T/repo" -- "$AT/codex" | tr -d '%')
sleep 0.3
mkdir -p "$SD/codex" && printf '%s\n' "$CXQ" >"$SD/codex/cx-5"
printf '%s' "{\"session_id\":\"cx-5\",\"cwd\":\"$T/repo-wt-b\",\"turn_id\":\"t9\",\"hook_event_name\":\"UserPromptSubmit\",\"prompt\":\"mine\"}" | as_pane 1 wvr adapt codex
ok "...a binding from another server is not taken" "$(st "$CXP" prompt)|$(st "$CXQ" prompt 2>/dev/null)|$(cut -d ' ' -f 1 "$SD/codex/cx-5")" "mine||$CXP"
tmux -S "$TSOCK" kill-pane -t "%$CXP"
tmux -S "$TSOCK" kill-pane -t "%$CXQ"
done_suite
