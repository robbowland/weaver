#!/bin/bash
# Each agent's own changes (docs/design.md, Agent chains): the main thread's
# and each subagent's chain in every worktree it changed, its start just
# before the agent's first change there, its tip the agent's files as it
# left them; from what Edit, Write and Bash (bashEditDiff) say they did, and
# without it. Agent B (pane 1) works in its own scratch repo.
. "$(dirname "$0")/../lib.sh"
fresh

R=$AT/agentrepo
W=$AT/agentrepo-wt
rm -rf "$R" "$W"
git init -q -b main "$R"
git -C "$R" config user.email test@example.com
git -C "$R" config user.name test
printf 'one\ntwo\n' >"$R/a.txt"
printf 'bee\n' >"$R/b.txt"
printf 'secret.env\n' >"$R/.gitignore"
git -C "$R" add . && git -C "$R" commit -qm init
git -C "$R" worktree add -q -b side "$W"
SID=sid-agents
ref() { printf 'refs/worktree/weaver/agents/%s/%s' "$SID" "$1"; }

# ev EVENT [EXTRA-JSON]: an event of the session, through the hook Claude
# Code would use.
ev() { hooka 1 "$(printf '{"hook_event_name":"%s","cwd":"%s","session_id":"%s"%s}' "$1" "$R" "$SID" "${2:-}")" >/dev/null; }
# post AGENT TOOL INPUT RESPONSE: a finished call ("" AGENT: the main thread).
post() {
	local by=""
	[ -n "$1" ] && by=$(printf ',"agent_id":"%s","agent_type":"general-purpose"' "$1")
	ev PostToolUse "$by,\"tool_name\":\"$2\",\"tool_input\":$3,\"tool_response\":$4"
	settle
}
input_file() { jq -nc --arg f "$1" '{file_path:$f}'; }
input_cmd() { jq -nc --arg c "$1" '{command:$c}'; }
# What an agent's chain changed in a worktree: "<status> <path>" each.
changed() { git -C "$1" diff --name-status "$(ref "$2")^" "$(ref "$2")" 2>/dev/null | tr '\t' ' ' | tr '\n' ',' | sed 's/,$//'; }
added() { git -C "$1" diff "$(ref "$2")^" "$(ref "$2")" -- "$3" 2>/dev/null | grep '^[-+][^-+]' | tr '\n' ',' | sed 's/,$//'; }
commits() { git -C "$1" rev-list --count "$(ref "$2")" 2>/dev/null || echo 0; }

ev SessionStart ',"source":"startup"'
ev UserPromptSubmit ',"prompt":"build it"'
cp "$R/.git/index" "$AT/agents.index.before"
cp "$R/.git/worktrees/agentrepo-wt/index" "$AT/agents.wtindex.before"

# Your own change, before any agent touches the worktree.
printf 'bee\nby you\n' >"$R/b.txt"

# The main thread edits a.txt; Edit says what the file held.
printf 'one\ntwo\nthree\n' >"$R/a.txt"
post "" Edit "$(input_file "$R/a.txt")" "$(jq -nc '{originalFile:"one\ntwo\n",structuredPatch:[]}')"
ok "the main thread's edit starts its chain: start, then tip" "$(commits "$R" main)" "2"
ok "...whose diff is its edit alone, not your change before it" "$(changed "$R" main)|$(added "$R" main a.txt)" "M a.txt|+three"
ok "...and whose start is the worktree just before it, yours included" \
	"$(git -C "$R" show "$(ref main)^:b.txt" | tr '\n' ',')|$(git -C "$R" show "$(ref main)^:a.txt" | tr '\n' ',')" "bee,by you,|one,two,"

# A subagent's Write creates a file in the other worktree.
printf 'token\n' >"$W/new.txt"
post sub-a Write "$(input_file "$W/new.txt")" '{"type":"create","originalFile":null}'
ok "a subagent's first write starts its chain in its own worktree" "$(changed "$W" sub-a)" "A new.txt"
ok "...and none in the main checkout" "$(commits "$R" sub-a)" "0"

# Its Bash changes a.txt there; bashEditDiff gives the hunks.
printf 'one\ntwo\nx\n' >"$W/a.txt"
post sub-a Bash "$(input_cmd "printf 'x\\n' >> a.txt")" "$(jq -nc --arg f "$W/a.txt" \
	'{stdout:"",stderr:"",bashEditDiff:{files:[{filePath:$f,hunks:[{oldStart:1,oldLines:2,newStart:1,newLines:3,lines:[" one"," two","+x"]}]}],changedFiles:[$f],moreFiles:0}}')"
ok "its Bash edit moves the tip; the start stays" "$(changed "$W" sub-a)|$(commits "$W" sub-a)" "M a.txt,A new.txt|2"

# Another subagent's first change, in the main checkout, is a file Bash
# creates: its start must not hold it.
printf 'sea\n' >"$R/c.txt"
post sub-b Bash "$(input_cmd "echo sea > c.txt")" "$(jq -nc --arg f "$R/c.txt" \
	'{bashEditDiff:{files:[{filePath:$f,hunks:[{oldStart:0,oldLines:0,newStart:1,newLines:1,lines:["+sea"]}]}],changedFiles:[$f],moreFiles:0}}')"
ok "a subagent whose first change is a file Bash created: added" "$(changed "$R" sub-b)" "A c.txt"
ok "the main thread's chain does not take another agent's files" "$(changed "$R" main)" "M a.txt"

# A Bash call that says nothing (no bashEditDiff), or holds back what it saw
# (shared, no files): what git sees changed since may be another agent's or
# yours, so the chain takes none of it.
printf 'one\ntwo\nthree\nfour\n' >"$R/a.txt"
post sub-b Bash "$(input_cmd "echo four >> a.txt")" '{"stdout":""}'
printf 'sea\nshore\n' >"$R/c.txt"
post sub-b Bash "$(input_cmd "echo shore >> c.txt")" '{"bashEditDiff":{"files":[],"changedFiles":[],"moreFiles":0,"shared":true}}'
ok "a Bash call that does not say what it changed adds nothing to the chain" "$(changed "$R" sub-b)|$(added "$R" sub-b c.txt)" "A c.txt|+sea"

# The first calls of a batch run before the hook of the first: the start is
# read with the later ones' changes in it. Each later call says what its file
# held, so the start is put right.
printf 'bee\nby you\nby sub-c\n' >"$R/b.txt"
printf 'new\n' >"$R/e.txt"
post sub-c Edit "$(input_file "$R/b.txt")" "$(jq -nc '{originalFile:"bee\nby you\n"}')"
post sub-c Write "$(input_file "$R/e.txt")" '{"type":"create","originalFile":null}'
ok "a batch whose later change was on disk first: every one of its changes is the agent's" \
	"$(changed "$R" sub-c)|$(added "$R" sub-c b.txt)|$(commits "$R" sub-c)" "M b.txt,A e.txt|+by sub-c|2"

# Big files: a tracked one is taken, an untracked one left out.
printf 'dee\n' >"$W/d.txt"
post sub-d Write "$(input_file "$W/d.txt")" '{"type":"create","originalFile":null}'
head -c 3000 /dev/zero | tr '\0' 'x' >"$W/b.txt"
head -c 3000 /dev/zero | tr '\0' 'y' >"$W/huge.txt"
WEAVER_SNAPSHOT_MAX_KB=1 post sub-d Bash "$(input_cmd "make big")" "$(jq -nc --arg b "$W/b.txt" --arg h "$W/huge.txt" \
	'{bashEditDiff:{files:[],changedFiles:[$b,$h],moreFiles:0}}')"
ok "a big file it tracks is taken, a big untracked one is not" "$(changed "$W" sub-d)" "M b.txt,A d.txt"
ok "no private index is left behind" "$(find "$R/.git/weaver" "$R/.git/worktrees/agentrepo-wt/weaver" -name 'index-sid-agents-*' 2>/dev/null | wc -l | tr -d ' ')" "0"

# The main thread edits a file git ignores: none of its chain.
printf 'KEY=2\n' >"$R/secret.env"
post "" Edit "$(input_file "$R/secret.env")" "$(jq -nc '{originalFile:"KEY=1\n"}')"
ok "a file git ignores stays out of the chain" "$(changed "$R" main)" "M a.txt"

# The main thread edits a file a subagent changed, in that worktree; Edit
# says only how (structuredPatch).
printf 'one\nTWO\nx\n' >"$W/a.txt"
post "" Edit "$(input_file "$W/a.txt")" \
	'{"originalFile":null,"structuredPatch":[{"oldStart":1,"oldLines":3,"newStart":1,"newLines":3,"lines":[" one","-two","+TWO"," x"]}]}'
ok "the main thread's chain there holds only its own hunk" "$(added "$W" main a.txt)" "-two,+TWO"
ok "...and the subagent's keeps the file as it left it" "$(git -C "$W" show "$(ref sub-a):a.txt" | tr '\n' ',')" "one,two,x,"

# The Agent call that started sub-a says what it was for.
post "" Agent '{"description":"write the token","subagent_type":"general-purpose","prompt":"..."}' \
	'{"status":"completed","agentId":"sub-a","agentType":"general-purpose"}'
LOG=$SD/log/$SID.jsonl
ok "the log names each chain once, as it starts" \
	"$(jq -r 'select(.ev=="snapshot" and .point=="agent") | .agent_id + "@" + (.root | sub(".*/"; ""))' "$LOG" | tr '\n' ' ')" \
	"main@agentrepo sub-a@agentrepo-wt sub-b@agentrepo sub-c@agentrepo sub-d@agentrepo-wt main@agentrepo-wt "
ok "...and what a subagent was asked to do" "$(jq -r 'select(.ev=="agent") | .agent_id + ":" + .agent_type + ":" + .desc' "$LOG")" \
	"sub-a:general-purpose:write the token"
ok "the commits are weaver's, with the agent" \
	"$(git -C "$R" log -1 --format='%an|%(trailers:key=Weaver-Agent,valueonly)|%(trailers:key=Weaver-Point,valueonly)' "$(ref sub-b)" | tr -d '\n')" \
	"weaver|sub-b|tip"
ok "your staging areas are untouched" \
	"$(cmp -s "$R/.git/index" "$AT/agents.index.before" && echo same)|$(cmp -s "$R/.git/worktrees/agentrepo-wt/index" "$AT/agents.wtindex.before" && echo same)" \
	"same|same"

# Snapshots off: no chains.
SID=sid-agents-off
WEAVER_SNAPSHOTS=0 hooka 1 "$(printf '{"hook_event_name":"PostToolUse","cwd":"%s","session_id":"%s","tool_name":"Edit","tool_input":%s,"tool_response":{}}' \
	"$R" "$SID" "$(input_file "$R/a.txt")")" >/dev/null
settle
ok "WEAVER_SNAPSHOTS=0: no agent chain" "$(commits "$R" main)" "0"

done_suite
