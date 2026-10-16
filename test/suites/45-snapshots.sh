#!/bin/bash
# Per-turn snapshots (docs/design.md): the chain of start and end commits,
# outside changes, Esc and interrupts, a turn that resumes, roots first touched
# mid-turn, the private index, big files, the reviewed mark; and the activity
# log. Agent B (pane 1) works in its own scratch repo so nothing else touches it.
. "$(dirname "$0")/../lib.sh"
fresh

R=$AT/snaprepo
rm -rf "$R" "$R-wt"
git init -q -b main "$R"
git -C "$R" config user.email test@example.com
git -C "$R" config user.name test
printf 'one\ntwo\n' >"$R/a.txt"
git -C "$R" add . && git -C "$R" commit -qm init
git -C "$R" worktree add -q -b side "$R-wt"
REF=refs/worktree/weaver/sessions/sid-snap

# j EVENT [EXTRA-JSON]: an event of the current turn (prompt id p-N).
CUR=0
j() { printf '{"hook_event_name":"%s","cwd":"%s","session_id":"sid-snap","prompt_id":"p-%s","transcript_path":"/tmp/t.jsonl"%s}' "$1" "$R" "$CUR" "${2:-}"; }
prompt() {
	CUR=$1
	hooka 1 "$(j UserPromptSubmit ",\"prompt\":\"$2\"")" >/dev/null
	settle
}
edit() { printf '%s\n' "$2" >>"$1"; hooka 1 "$(j PostToolUse ",\"tool_name\":\"Edit\",\"tool_input\":{\"file_path\":\"$1\"},\"tool_response\":{}")"; }
stop() { hooka 1 "$(j Stop ',"last_assistant_message":"done"')" >/dev/null; settle; }
# The subjects of a chain, without their prompts.
subj() { sed 's/^\(weaver: turn [0-9]* [a-z]*\).*/\1/'; }
chain() { git -C "${1:-$R}" log --format='%s' "$REF" 2>/dev/null | subj | tr '\n' ',' | sed 's/,$//'; }
trailer() { git -C "${3:-$R}" log -1 --format="%(trailers:key=$2,valueonly)" "$1" | tr -d '\n'; }

hooka 1 "$(j SessionStart ',"source":"startup"')" >/dev/null
cp "$R/.git/index" "$AT/index.before"
prompt 1 "add three"
ok "a prompt snapshots the worktree as the turn's start (a root commit)" \
	"$(chain)|$(git -C "$R" rev-list --count "$REF")" "weaver: turn 1 start|1"
edit "$R/a.txt" three
stop
ok "Stop snapshots its end" "$(chain)" "weaver: turn 1 end,weaver: turn 1 start"
ok "turn 1's diff is exactly the agent's edit" "$(git -C "$R" diff "$REF^" "$REF" | grep '^[-+][^-+]')" "+three"
ok "the commit carries the turn, prompt and ids" \
	"$(trailer "$REF" Weaver-Turn)|$(trailer "$REF" Weaver-Point)|$(trailer "$REF" Prompt-Id)|$(trailer "$REF" Weaver-Session)|$(git -C "$R" log -1 --format=%b "$REF" | head -n 1)" \
	"1|end|p-1|sid-snap|add three"
ok "...by weaver, not you" "$(git -C "$R" log -1 --format='%an <%ae>' "$REF")" "weaver <weaver@localhost>"
ok "your staging area is untouched" "$(cmp -s "$R/.git/index" "$AT/index.before" && echo same)|$(git -C "$R" diff --cached --name-only)" "same|"

# Changes made between turns are not the next turn's.
printf 'by you\n' >>"$R/a.txt"
prompt 2 "add four"
edit "$R/a.txt" four
stop
ok "your edit between turns is its own start commit" \
	"$(chain)" "weaver: turn 2 end,weaver: turn 2 start,weaver: turn 1 end,weaver: turn 1 start"
ok "...so turn 2's diff is only the agent's" "$(git -C "$R" diff "$REF^" "$REF" | grep '^[-+][^-+]')" "+four"
ok "blame against the chain names the turn of each line" \
	"$(git -C "$R" blame -s --line-porcelain "$REF" -- a.txt | grep '^summary' | sed 's/summary weaver: //; s/: .*//' | tr '\n' ',')" \
	"turn 1 start,turn 1 start,turn 1 end,turn 2 start,turn 2 end,"

# An unchanged turn adds nothing; a turn that resumes after background work
# ends once.
prompt 3 "just look"
stop
ok "a turn that changed nothing: no commit" "$(git -C "$R" rev-list --count "$REF")" "4"
prompt 4 "start, wait for the background, finish"
edit "$R/a.txt" five
stop
edit "$R/a.txt" six
stop
ok "a turn that resumes: one end, covering both" \
	"$(git -C "$R" log --format=%s "$REF" | grep -c 'turn 4 end')|$(git -C "$R" diff "$REF^" "$REF" | grep -c '^+[fs]')" "1|2"

# Esc while it writes fires nothing: the next prompt ends the previous turn.
prompt 5 "something long"
edit "$R/a.txt" seven
prompt 6 "after an Esc"
ok "a prompt after an Esc: the previous turn ends there" \
	"$(git -C "$R" log --format=%s "$REF" | sed -n 1p | subj)" "weaver: turn 5 end"
# Esc during a tool ends the turn at once.
edit "$R/a.txt" eight
hooka 1 "$(j PostToolUseFailure ',"tool_name":"Bash","tool_input":{"command":"sleep 99"},"error":"Interrupted","is_interrupt":true')"
settle
ok "Esc during a tool: the turn ends there" "$(git -C "$R" log -1 --format=%s "$REF" | subj)" "weaver: turn 6 end"

# A worktree first touched mid-turn starts its chain from HEAD.
prompt 7 "work in the other worktree"
edit "$R-wt/a.txt" nine
stop
ok "first touch of another worktree: its chain starts at HEAD" "$(chain "$R-wt")" "weaver: turn 7 end,weaver: turn 7 start"
ok "...so the turn's diff there is only the edit" "$(git -C "$R-wt" diff "$REF^" "$REF" | grep '^[-+][^-+]')" "+nine"

# Untracked files are in, ignored and big ones out.
prompt 8 "big files"
printf 'secret\n' >"$R/ignored.log"
printf '*.log\n' >"$R/.gitignore"
printf 'new\n' >"$R/new.txt"
dd if=/dev/zero of="$R/huge.bin" bs=1024 count=6000 2>/dev/null
edit "$R/a.txt" ten
stop
files=$(git -C "$R" show --name-only --format= "$REF" | sort | tr '\n' ' ')
ok "untracked files are snapshotted, ignored and big ones are not" "$files" ".gitignore a.txt new.txt "
rm -f "$R/huge.bin"
prompt 81 "a file that grows"
dd if=/dev/zero of="$R/grows.bin" bs=1024 count=10 2>/dev/null
edit "$R/a.txt" small
stop
ok "...a small untracked file is in" "$(git -C "$R" cat-file -t "$REF:grows.bin" 2>/dev/null)" "blob"
prompt 82 "it grew"
dd if=/dev/zero of="$R/grows.bin" bs=1024 count=6000 2>/dev/null
edit "$R/a.txt" grown
stop
ok "...and out once it grows past the limit, not kept stale" "$(git -C "$R" cat-file -t "$REF:grows.bin" 2>/dev/null || echo absent)" "absent"
rm -f "$R/grows.bin"

# Marking reviewed.
c1=$(cd "$R" && wvr snapshot --reviewed)
ok "snapshot --reviewed prints the commit on the reviewed ref" \
	"$(git -C "$R" rev-parse refs/worktree/weaver/reviewed)|$(trailer "$c1" Weaver-Point)" "$c1|reviewed"
c2=$(wvr snapshot --reviewed "$R")
ok "...and the same commit again while nothing changed" "$c2" "$c1"
printf 'later\n' >>"$R/a.txt"
c3=$(wvr snapshot --reviewed "$R")
ok "...a new one on top when something did" "$(git -C "$R" rev-parse "$c3^")" "$c1"
ok "outside a repository: an error" "$(wvr snapshot --reviewed / 2>&1 >/dev/null | grep -c 'not in a git repository')" "1"

# WEAVER_SNAPSHOTS=0 turns them off.
before=$(git -C "$R" rev-parse "$REF")
CUR=9
WEAVER_SNAPSHOTS=0 hooka 1 "$(j UserPromptSubmit ',"prompt":"no snapshots"')" >/dev/null
printf 'x\n' >>"$R/a.txt"
WEAVER_SNAPSHOTS=0 hooka 1 "$(j Stop)" >/dev/null
settle
ok "WEAVER_SNAPSHOTS=0: no snapshots" "$(git -C "$R" rev-parse "$REF")" "$before"

# The activity log: one JSON object per line, in order.
L=$(st 1 log)
ok "the activity log is where the state says" "$L" "$AT/state/weaver/log/sid-snap.jsonl"
ok "...every line is JSON with t, pane, turn and ev" \
	"$(jq -s 'all(.[]; has("t") and has("pane") and has("turn") and has("ev"))' "$L")" "true"
ok "...prompts, edits, stops and snapshots, with their turn" \
	"$(jq -r 'select(.turn == 2) | .ev' "$L" | sort -u | tr '\n' ' ')" "edit prompt snapshot state stop "
ok "...a snapshot line names its commit" \
	"$(jq -r 'select(.ev == "snapshot" and .turn == 1 and .point == "end") | .commit' "$L")" \
	"$(git -C "$R" log --format='%H %s' "$REF" | grep 'turn 1 end' | cut -d' ' -f1)"
done_suite
