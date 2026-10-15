#!/bin/bash
# Threads lost with their zellij: when an agent dies because its zellij went
# (a crash, or zellij quit), its thread is kept as lost, and `weaver resume`
# (or the weaver session's home, as it starts again) brings it back in a new
# tab whose agent resumes its conversation. An agent ended on purpose (/exit),
# or whose pane closed while its zellij ran on, is forgotten as before.
. "$(dirname "$0")/../lib.sh"
fresh

for a in claude codex; do
	cat >"$AT/bin/$a" <<EOF
#!/bin/bash
printf '%s\n' "\$@" >"$AT/args.$a.\${ZELLIJ_PANE_ID:-none}"
[ -t 0 ] && exec bash --noprofile --norc -i
exit 0
EOF
	chmod +x "$AT/bin/$a"
done
waitfor() {
	local i
	for i in $(seq 100); do
		eval "$1" && return 0
		sleep 0.1
	done
	return 1
}
LOST=$SD/lost
dead_pid() {
	sleep 0 &
	DEAD=$!
	wait "$DEAD"
}
# lost_thread SKEY PANE SID NAME NUM DIR [END_REASON] [TS]: the files of a
# thread whose agent (a dead pid) ran in pane PANE of zellij session SKEY.
lost_thread() {
	dead_pid
	jq -nc --argjson pane "$2" --arg sid "$3" --argjson pid "$DEAD" --arg dir "$6" --arg why "${7:-}" \
		--argjson ts "${8:-$(date +%s)}" \
		'{pane: $pane, agent: "claude", state: "ended", ts: $ts, since: $ts, session_id: $sid, pid: $pid,
		  title: "the title", turn: 3, turn_ended: 3, cwd: $dir, root: $dir, start_cwd: $dir,
		  prompt: "the last prompt", last_message: "the last reply", files: [], subagents: [], tasks: []}
		+ (if $why != "" then {end_reason: $why} else {} end)' >"$SD/${1}_$2.json"
	jq -nc --argjson pane "$2" --arg name "$4" --argjson num "$5" --arg dir "$6" \
		'{num: $num, cmd: "claude", name: $name, task: ("do " + $name), repo: "", branch: $name, worktree: $dir,
		  here: false, worktree_created: false, agent: "claude", created: 1, base: "", pane: $pane}' >"$SD/${1}_$2.thread"
}
# The agent pane of tab $1 into AG, its nvim's into NVP.
tab_panes() {
	local j
	j=$(Z list-panes -a -j)
	NVP=$(printf '%s' "$j" | jq -r --argjson t "$1" '[.[] | select((.is_plugin | not) and .tab_id == $t
		and ((.terminal_command // "") | test(" nvim --thread ")))] | .[0].id // empty')
	AG=$(printf '%s' "$j" | jq -r --argjson t "$1" '[.[] | select((.is_plugin | not) and .tab_id == $t
		and ((.terminal_command // "") | test(" _agent ")))] | .[0].id // empty')
}
tab_named() { Z list-tabs -j | jq -r --arg n "$1" '[.[] | select(.name == $n)] | .[0].tab_id // empty'; }

# --- which dead agents are kept
lost_thread gone-sess 5 sid-lost-a alpha 2 "$T/repo-wt-a"
lost_thread gone-sess 6 sid-exit beta 3 "$T/repo-wt-b" prompt_input_exit
weaver gc >/dev/null 2>&1
ok "lost: a thread whose zellij went is kept, its pane's files go" \
	"$(jq -r '"\(.session) \(.thread.name) \(.thread.num) \(.state.session_id) \(.state.turn)"' "$LOST/gone-sess_sid-lost-a.json" 2>/dev/null)|$(ls "$SD"/gone-sess_5.* 2>/dev/null | wc -l | tr -d ' ')" \
	"gone-sess alpha 2 sid-lost-a 3|0"
ok "lost: one ended on purpose (/exit) is forgotten" \
	"$([ -f "$LOST/gone-sess_sid-exit.json" ] && echo kept || echo forgotten)|$(ls "$SD"/gone-sess_6.* 2>/dev/null | wc -l | tr -d ' ')" \
	"forgotten|0"
# This zellij runs on: its pane was closed, not lost.
lost_thread "$SESSION" 70 sid-closed gamma 4 "$T/repo"
weaver gc >/dev/null 2>&1
ok "lost: a thread whose pane closed while its zellij ran on is forgotten" \
	"$([ -f "$LOST/${SESSION}_sid-closed.json" ] && echo kept || echo forgotten)|$(ls "$SD/${SESSION}_70."* 2>/dev/null | wc -l | tr -d ' ')" \
	"forgotten|0"
# Its last event before this zellij started: it ran in an earlier one.
lost_thread "$SESSION" 71 sid-earlier delta 5 "$T/repo" "" 1000
weaver gc >/dev/null 2>&1
ok "lost: a thread from before this zellij started (same session name) is kept" \
	"$(jq -r .thread.name "$LOST/${SESSION}_sid-earlier.json" 2>/dev/null)" "delta"
rm -f "$LOST/${SESSION}_sid-earlier.json"

# The hook: why a session ended.
hookj 72 "{\"hook_event_name\":\"SessionStart\",\"source\":\"startup\",\"cwd\":\"$T/repo\",\"session_id\":\"sid-72\"}"
hookj 72 "{\"hook_event_name\":\"SessionEnd\",\"reason\":\"prompt_input_exit\",\"cwd\":\"$T/repo\",\"session_id\":\"sid-72\"}"
ok "hook: SessionEnd keeps its reason" "$(st 72 state) $(st 72 end_reason)" "ended prompt_input_exit"
hookj 72 "{\"hook_event_name\":\"SessionStart\",\"source\":\"startup\",\"cwd\":\"$T/repo\",\"session_id\":\"sid-72b\"}"
ok "hook: a new session in the pane starts without one" "$(st 72 end_reason)" "null"
rm -f "$SD/${SESSION}_72."*

# A new agent given the pane id of one lost in an earlier zellij: the old
# thread is kept before the new state replaces it.
lost_thread "$SESSION" 73 sid-reused epsilon 6 "$T/repo" "" 1000
live_pid
printf '%s' "{\"hook_event_name\":\"SessionStart\",\"source\":\"startup\",\"cwd\":\"$T/repo\",\"session_id\":\"sid-new-73\"}" |
	ZELLIJ_PANE_ID=73 WEAVER_AGENT_PID=$LIVE_PID weaver hook
ok "hook: a reused pane id keeps the old thread as lost" \
	"$(jq -r .thread.name "$LOST/${SESSION}_sid-reused.json" 2>/dev/null)|$(st 73 session_id)|$([ -f "$SD/${SESSION}_73.thread" ] && echo thread || echo none)" \
	"epsilon|sid-new-73|none"
kill "$LIVE_PID" 2>/dev/null
rm -f "$SD/${SESSION}_73."* "$LOST/${SESSION}_sid-reused.json"

# --- listing, and the dashboard
o=$(weaver resume)
ok "resume: alone, lists the lost threads" \
	"$(printf '%s' "$o" | grep -c 'alpha')|$(printf '%s' "$o" | grep -c 'sid-lost')" "1|1"
o=$(COLUMNS=120 weaver ls </dev/null | cat)
ok "dashboard: lost threads under LOST, not counted as threads" \
	"$(printf '%s' "$o" | grep -c 'LOST · 1')|$(printf '%s' "$o" | grep -c 'alpha.*lost with zellij')|$(printf '%s' "$o" | grep -c 'THREADS \] [0-9]')" \
	"1|1|0"
ok "dashboard: a lost thread's card" \
	"$(weaver _preview sid-lost-a 'alpha · repo · lost' | perl -pe 's/\e\[[0-9;]*m//g' |
		grep -cE '^lost · |^asked +do alpha|^said +the last reply')" "3"
ok "resume: an unknown one says so" "$(weaver resume nope 2>&1 | grep -c 'no lost thread')" "1"

# --- weaver resume NAME: a new tab, the conversation resumed
out=$(NOFOCUS=1 weaver resume alpha 2>&1)
tab=$(tab_named alpha)
tab_panes "${tab:-0}"
waitfor "[ -f '$AT/args.claude.$AG' ]"
ok "resume: a tab named after it, its agent resumes the conversation" \
	"$([ -n "$tab" ] && echo tab)|$(tr '\n' ' ' <"$AT/args.claude.$AG" 2>/dev/null)" "tab|--resume sid-lost-a "
ok "resume: it keeps its number, its record and its state carry on" \
	"$(jq -r '"\(.num) \(.name) \(.pane)"' "$SD/${SESSION}_$AG.thread" 2>/dev/null)|$(st "$AG" session_id) $(st "$AG" turn) $(st "$AG" state) $(st "$AG" title)" \
	"2 alpha $AG|sid-lost-a 3 idle the title"
ok "resume: the lost record is gone" "$(ls "$LOST" 2>/dev/null | wc -l | tr -d ' ')" "0"
waitfor "[ -f '$SD/${SESSION}_$AG.nvim' ]"
ok "resume: its nvim is bound to it" "$(nvim_pane "$AG")" "$NVP"
hookj "$AG" "{\"hook_event_name\":\"SessionStart\",\"source\":\"resume\",\"cwd\":\"$T/repo-wt-a\",\"session_id\":\"sid-lost-a\"}"
ok "resume: the agent's first hook carries its turns on" "$(st "$AG" turn) $(st "$AG" title)" "3 the title"
Z close-tab-by-id "$tab" >/dev/null 2>&1

# --- forget, and --all
lost_thread gone-sess 7 sid-zeta zeta 7 "$T/repo-wt-a"
lost_thread gone-sess 8 sid-eta eta 8 "$T/repo-wt-b"
lost_thread gone-sess 9 sid-theta theta 9 "$T/repo-wt-b"
weaver gc >/dev/null 2>&1
weaver resume --forget --yes theta >/dev/null 2>&1
ok "resume --forget: drops it for good" "$([ -f "$LOST/gone-sess_sid-theta.json" ] && echo kept || echo gone)" "gone"
out=$(NOFOCUS=1 weaver resume --all 2>&1)
waitfor "[ -n \"\$(tab_named zeta)\" ] && [ -n \"\$(tab_named eta)\" ]"
ok "resume --all: every one back, each in its tab" \
	"$([ -n "$(tab_named zeta)" ] && echo zeta)|$([ -n "$(tab_named eta)" ] && echo eta)|$(ls "$LOST" 2>/dev/null | wc -l | tr -d ' ')" \
	"zeta|eta|0"
for n in zeta eta; do Z close-tab-by-id "$(tab_named "$n")" >/dev/null 2>&1; done

# A thread whose directory has gone stays lost, and says why.
lost_thread gone-sess 10 sid-iota iota 10 "$T/no-such-dir"
weaver gc >/dev/null 2>&1
ok "resume: a thread whose directory is gone stays lost" \
	"$(weaver resume iota 2>&1 | grep -c 'is gone')|$([ -f "$LOST/gone-sess_sid-iota.json" ] && echo kept)" "1|kept"
rm -f "$LOST/gone-sess_sid-iota.json"

# --- the weaver session: its home brings back its own lost threads
lost_thread "$SESSION" 74 sid-kappa kappa 1 "$T/repo-wt-a" "" 1000
lost_thread other-sess 75 sid-lambda lambda 2 "$T/repo-wt-b"
echo weaver >"$SD/${SESSION}.layout"
home=$(Z new-tab --name home -- "$AT/bin/weaver" home)
waitfor "[ -n \"\$(tab_named kappa)\" ]"
ok "session: home brings back the threads this session lost, not other sessions'" \
	"$([ -n "$(tab_named kappa)" ] && echo kappa)|$([ -n "$(tab_named lambda)" ] && echo lambda)|$(jq -r .thread.name "$LOST/other-sess_sid-lambda.json" 2>/dev/null)" \
	"kappa||lambda"
tab_panes "$(tab_named kappa)"
waitfor "[ -f '$AT/args.claude.$AG' ]"
ok "session: its agent resumes its conversation" "$(tr '\n' ' ' <"$AT/args.claude.$AG" 2>/dev/null)" "--resume sid-kappa "
Z close-tab-by-id "$(tab_named kappa)" >/dev/null 2>&1
Z close-tab-by-id "$home" >/dev/null 2>&1
rm -f "$SD/${SESSION}.layout"
sleep 0.5
done_suite
