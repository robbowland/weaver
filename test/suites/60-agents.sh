#!/bin/bash
# Agents other than Claude Code: `wvr event` (normalized JSON), and the Codex
# adapter (`wvr adapt codex`), which weaver cannot place on Rex yet.
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
kill "$P"

# --- the Codex adapter: weaver cannot place Codex's hooks in a Rex block
# (its app server runs every session's, with any session's environment), so
# it drops them, even one from inside a pane
fresh
printf '%s' "{\"session_id\":\"cx-1\",\"cwd\":\"$T/repo-wt-b\",\"hook_event_name\":\"SessionStart\",\"source\":\"startup\"}" | as_pane 1 wvr adapt codex
ok "codex: its hooks are dropped" "$([ -f "$SD/${SK}_1.json" ] && echo pane-1 || echo none)" "none"
done_suite
