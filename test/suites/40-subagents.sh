#!/bin/bash
# Subagents: their edits and Bash edits, the main agent looking around its own
# checkout, background work, and a tab per worktree in nvim. Runs after the
# other suites (agent A's nvim is open).
. "$(dirname "$0")/../lib.sh"

# ev EVENT CWD [EXTRA-JSON] [--async]: an event in agent A's new session.
ev() { hookj 0 "$(printf '{"hook_event_name":"%s","cwd":"%s","session_id":"sid-sub"%s}' "$1" "$2" "${3:-}")" ${4:+"$4"}; }
edit_by() { printf ',"agent_id":"%s","tool_name":"Edit","tool_input":{"file_path":"%s"}' "$1" "$2"; }
bash_by() { printf ',"agent_id":"%s","tool_name":"Bash","tool_input":{"command":%s}' "$1" "$(jq -Rn --arg c "$2" '$c')"; }

nvsend 0 "<Esc>:silent! tabonly!<CR>:tcd $T/repo<CR>:e! $T/repo/README.md<CR>"
sleep 0.5
ev SessionStart "$T/repo"
ev UserPromptSubmit "$T/repo" ',"prompt":"implement both features"'

# --- two subagents in two worktrees: a tab each
ev PostToolUse "$T/repo" "$(edit_by sub-a "$T/repo-wt-a/src/app.txt")"
sleep 0.8
ok "subagent edit in a worktree: nvim opens it in a new tab" "$(tabcwds 0)|$(nvst 0)" "repo,repo-wt-a|repo-wt-a|app.txt"
ev PostToolUse "$T/repo" "$(edit_by sub-b "$T/repo-wt-b/README.md")"
sleep 0.8
ok "another subagent in another worktree: another tab" "$(tabcwds 0)|$(nvst 0)" "repo,repo-wt-a,repo-wt-b|repo-wt-b|README.md"
ev PostToolUse "$T/repo" "$(edit_by sub-a "$T/repo-wt-a/src/app.txt")"
sleep 0.8
ok "the first one again: back to its tab" "$(tabcwds 0)|$(nvst 0)" "repo,repo-wt-a,repo-wt-b|repo-wt-a|app.txt"

# --- the main agent looking around its own checkout keeps the worktree
ev UserPromptSubmit "$T/repo/src" ',"prompt":"<agent-message from=\"sub-b\"> [Subagent hand-back] done"'
sleep 0.5
ok "main agent cd'ing inside its checkout: root stays on the worktree" "$(st 0 root)|$(nvst 0)" "$T/repo-wt-a|repo-wt-a|app.txt"
ok "a subagent's report is not taken as your prompt" "$(st 0 prompt)" "implement both features"
ev PostToolUse "$T/repo-wt-b" "$(edit_by sub-b "$T/repo-wt-a/README.md")"
ok "a subagent's cwd is not the agent's" "$(st 0 cwd)" "$T/repo/src"

# --- edits made through Bash are followed too
ev PostToolUse "$T/repo" "$(bash_by sub-b "cd $T/repo-wt-b && git status")" --async
sleep 0.1
sed -i '' 's/line two/line two, by sed/' "$T/repo-wt-b/src/app.txt"
ev PostToolUse "$T/repo" "$(bash_by sub-b "cd $T/repo-wt-b && sed -i '' 's/line two/line two, by sed/' src/app.txt")" --async
sleep 0.8
ok "Bash edit: root, file and nvim follow it" "$(st 0 root)|$(nvst 0)|$(nvq 0 'getline(2)')" \
	"$T/repo-wt-b|repo-wt-b|app.txt|line two, by sed"
ok "...in that worktree's tab" "$(tabcwds 0)" "repo,repo-wt-a,repo-wt-b"
ev PostToolUse "$T/repo" "$(bash_by sub-a "cd $T/repo-wt-a && git log -1")" --async
sleep 0.5
ok "Bash that changes nothing: nvim stays" "$(st 0 root)|$(nvst 0)" "$T/repo-wt-b|repo-wt-b|app.txt"

# --- waiting on background subagents is not done
ev Stop "$T/repo" ',"background_tasks":[{"id":"x1","type":"subagent","status":"running","description":"Implement feature A"},{"id":"x2","type":"shell","status":"running","description":"npm run dev"}]'
ok "Stop with a subagent in the background: still working" "$(st 0 state)|$(st 0 background)" "working|1"
jq '.ts -= 120' "$SD/${SESSION}_0.json" >"$AT/c.json" && mv "$AT/c.json" "$SD/${SESSION}_0.json"
ok "dashboard: says so, not 'interrupted?'" \
	"$(agentnav ls | grep -c '1 in background · implement both features')|$(agentnav ls | grep -c 'interrupted? implement both')" "1|0"
ev Notification "$T/repo" ',"notification_type":"idle_prompt","message":"Claude is waiting for your input"'
ok "idle reminder meanwhile: still working" "$(st 0 state)" "working"
ev UserPromptSubmit "$T/repo" ',"prompt":"<agent-message from=\"x1\"> [Subagent hand-back] done"'
ev Stop "$T/repo" ',"background_tasks":[]'
ok "last report handled: done" "$(st 0 state)|$(st 0 background)" "done|0"

# --- a diff review is never taken over
nvsend 0 '<Esc>:AgentReview<CR>'
sleep 2.5
review_tab=$(nvq 0 'tabpagenr()')
ev PostToolUse "$T/repo" "$(edit_by sub-a "$T/repo-wt-a/src/app.txt")"
sleep 0.8
ok "edit in another worktree while you review a diff: tab kept" "$(nvq 0 'tabpagenr()')" "$review_tab"
nvsend 0 '<Esc>:DiffviewClose<CR>'
sleep 1
done_suite
