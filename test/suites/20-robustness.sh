#!/bin/bash
# Failure modes: other sessions' files, corrupt state, stale and held locks,
# racing hooks, nested sessions, agents that die without a word, stale nvim
# files, odd paths, and a partial pane list.
. "$(dirname "$0")/../lib.sh"
fresh

rows() { weaver ls | grep -c -E '^\[.\] '; }

# The harness itself: zellij falls back to some other running session when the
# named one is gone, so the test shim must refuse.
ZELLIJ_SESSION_NAME=weaver-no-such-session zellij action list-tabs >/dev/null 2>&1
ok "test shim refuses actions on a session that isn't running" "$?" "1"

feed >/dev/null
settle

# A session whose name extends ours must not be listed or garbage-collected.
cp "$SD/${SESSION}_1.json" "$SD/${SESSION}_x_12.json"
ok "other session's file not listed" "$(rows)" "3"
ok "other session's file not deleted" "$([ -f "$SD/${SESSION}_x_12.json" ] && echo kept)" "kept"
rm -f "$SD/${SESSION}_x_12.json"

# --- one corrupt state file must not empty the dashboard (defect 6)
cp "$SD/${SESSION}_2.json" "$AT/c2.json"
printf '{"pane":2,"state":"wor' >"$SD/${SESSION}_2.json"
ok "a corrupt state file: the other agents are still listed" "$(rows)" "2"
ok "...and the next event rewrites it" \
	"$(hook 2 UserPromptSubmit "$T/repo" "" "again" && st 2 state)" "working"

# --- locks (defect 10): a dead owner's lock is removed at once; a live one is
# waited for, however long, and never stolen.
SF2="$SD/${SESSION}_2.json"
sleep 0.2 &
dead=$!
wait $dead
printf '%s\n' "$dead" >"$SF2.lock"
t=$(ms /bin/bash -c "printf '%s' '{\"hook_event_name\":\"Stop\",\"cwd\":\"$T/repo\",\"session_id\":\"sid-2\"}' | ZELLIJ_PANE_ID=2 weaver hook >/dev/null")
ok "a lock left by a dead hook is taken over at once" "$([ "$t" -lt 1000 ] && echo y)|$(st 2 state)" "y|done"
live_pid
printf '%s\n' "$LIVE_PID" >"$SF2.lock"
hook 2 UserPromptSubmit "$T/repo" "" "held" >/dev/null &
waiter=$!
sleep 1.2
ok "a lock held by a live hook is waited for, not stolen" "$(st 2 state)|$(cat "$SF2.lock")" "done|$LIVE_PID"
rm -f "$SF2.lock"
wait $waiter
ok "...and the waiting hook goes ahead once it is released" "$(st 2 state)|$(st 2 prompt)" "working|held"
kill "$LIVE_PID"

# --- racing hooks lose nothing: 12 edits at once all land in the file list
for i in 1 2 3 4 5 6 7 8 9 10 11 12; do
	hook 2 PostToolUse "$T/repo" "$T/repo/src/race$i.txt" &
done
wait
settle
ok "12 concurrent edits: valid state, every file listed" \
	"$(jq -r '.files | map(select(test("race"))) | length' "$SF2")" "12"
ok "...and no lock or temp file left behind" "$(ls "$SD" | grep -c -E '\.lock$|\.tmp$')" "0"

# --- a session nested in the pane's agent (claude -p, an SDK script) inherits
# ZELLIJ_PANE_ID, but must not take the pane over (defect 7)
live_pid
AGENT=$LIVE_PID
nested() { printf '%s' "$2" | CLAUDE_PID=$1 ZELLIJ_PANE_ID=1 weaver hook >/dev/null; }
nested "$AGENT" "{\"hook_event_name\":\"SessionStart\",\"source\":\"startup\",\"cwd\":\"$T/repo-wt-b\",\"session_id\":\"sid-real\"}"
nested "$AGENT" "{\"hook_event_name\":\"UserPromptSubmit\",\"prompt\":\"the real work\",\"cwd\":\"$T/repo-wt-b\",\"session_id\":\"sid-real\"}"
ok "the pane's agent records its pid" "$(st 1 pid)" "$AGENT"
nested 424242 "{\"hook_event_name\":\"SessionStart\",\"source\":\"startup\",\"cwd\":\"$T/repo\",\"session_id\":\"sid-nested\"}"
nested 424242 "{\"hook_event_name\":\"Stop\",\"cwd\":\"$T/repo\",\"session_id\":\"sid-nested\"}"
ok "a nested session's events are ignored" "$(st 1 session_id)|$(st 1 state)|$(st 1 prompt)" "sid-real|working|the real work"
kill "$AGENT"
wait "$AGENT" 2>/dev/null
live_pid
NEXT=$LIVE_PID
nested "$NEXT" "{\"hook_event_name\":\"SessionStart\",\"source\":\"startup\",\"cwd\":\"$T/repo\",\"session_id\":\"sid-next\"}"
ok "once the pane's agent is gone, a new one takes the pane" "$(st 1 session_id)|$(st 1 pid)" "sid-next|$NEXT"
kill "$NEXT"

# --- an agent killed without SessionEnd is forgotten (defect 5)
live_pid
GHOST=$LIVE_PID
printf '{"hook_event_name":"UserPromptSubmit","prompt":"doomed","cwd":"%s","session_id":"sid-ghost"}' "$T/repo" |
	CLAUDE_PID=$GHOST ZELLIJ_PANE_ID=2 weaver hook >/dev/null
ok "a live agent is listed" "$(weaver ls | grep -c 'doomed')" "1"
kill "$GHOST"
wait "$GHOST" 2>/dev/null
ok "killed without SessionEnd: no longer listed" "$(weaver ls | grep -c 'doomed')" "0"
ok "...and its state is gone" "$([ -f "$SD/${SESSION}_2.json" ] && echo kept || echo gone)" "gone"

# --- an agent cleaned up while the dashboard reads the others hides nothing
jq -c '.pane = 3' "$SD/${SESSION}_0.json" >"$SD/${SESSION}_3.json"
FOUND_RACE=$(WEAVER_SOURCED=1 bash -c '. "$1"; session_files json; rm -f "$2"; CLAUDE_LIVE="[]"; list_panes;
	jq -rRn --argjson panes "$PANES" --argjson claude "[]" --argjson now 0 "$JQ_ROWS" "${FOUND[@]}" 2>/dev/null | grep -c "^0"' _ \
	"$AT/bin/weaver" "$SD/${SESSION}_3.json")
ok "a state file removed while the rows are read: the others are still read" "$FOUND_RACE" "1"

# --- a pid reused by an unrelated process is not the agent
live_pid
printf '{"hook_event_name":"UserPromptSubmit","prompt":"reused","cwd":"%s","session_id":"sid-reuse","pid":0}' "$T/repo" |
	CLAUDE_PID=$LIVE_PID ZELLIJ_PANE_ID=2 weaver hook >/dev/null
jq '.proc_start = "Thu Jan  1 00:00:00 1970"' "$SD/${SESSION}_2.json" >"$AT/r.json" && mv "$AT/r.json" "$SD/${SESSION}_2.json"
ok "a pid now running something else: forgotten" "$(weaver ls | grep -c 'reused')" "0"
kill "$LIVE_PID"

# --- a stale nvim file (that nvim has exited) never renames a pane
hook 2 UserPromptSubmit "$T/repo" "" "renaming"
title=$(Z list-panes -a -j | jq -r '.[] | select((.is_plugin|not) and .id==1) | .title')
printf '1\n424242\n' >"$SD/${SESSION}_2.nvim"
hook 2 PostToolUse "$T/repo" "$T/repo-wt-a/src/app.txt"
settle
ok "a stale nvim file: the pane it names keeps its title" \
	"$(Z list-panes -a -j | jq -r '.[] | select((.is_plugin|not) and .id==1) | .title')" "$title"
rm -f "$SD/${SESSION}_2.nvim"

# --- state from a zellij session that no longer exists (a reused name has
# new pane ids) is cleaned up
jq -c '.ts -= 7200' "$SD/${SESSION}_0.json" >"$SD/weaver-gone-session_0.json"
cp "$SD/${SESSION}_0.json" "$SD/weaver-busy-session_0.json"
weaver gc >/dev/null
ok "state of a session that is gone: cleaned up" "$([ -f "$SD/weaver-gone-session_0.json" ] && echo kept || echo gone)" "gone"
ok "...but not within the hour (zellij's list can miss a busy session)" \
	"$([ -f "$SD/weaver-busy-session_0.json" ] && echo kept)" "kept"
rm -f "$SD/weaver-busy-session_0.json"
ok "...and ours kept" "$([ -f "$SD/${SESSION}_0.json" ] && echo kept)" "kept"

# --- a pane that is gone is cleaned up, but only on a pane list that has our
# own pane in it: never on a partial answer
jq '.pane = 77' "$SD/${SESSION}_0.json" >"$SD/${SESSION}_77.json"
ZELLIJ_PANE_ID=999999 weaver ls >/dev/null
ok "a pane list without our own pane: nothing cleaned up" "$([ -f "$SD/${SESSION}_77.json" ] && echo kept)" "kept"
ZELLIJ_PANE_ID=0 weaver ls >/dev/null
ok "a full pane list: the closed pane's agent is cleaned up" "$([ -f "$SD/${SESSION}_77.json" ] && echo kept || echo gone)" "gone"

# --- paths with spaces and unicode
W="$T/wt é space"
git -C "$T/repo" worktree add -q -b odd-path "$W" 2>/dev/null
printf 'x\n' >"$W/naïve file.txt"
hook 2 UserPromptSubmit "$T/repo" "" "unicode: é ü 漢字"
hook 2 PostToolUse "$T/repo" "$W/naïve file.txt"
ok "a root and file with spaces and unicode" "$(st 2 root)|$(st 2 file)" "$W|$W/naïve file.txt"
ok "...the prompt survives" "$(st 2 prompt)" "unicode: é ü 漢字"
ok "...and the activity log stays valid JSON" "$(jq -s 'length > 0' "$(st 2 log)")" "true"
done_suite
