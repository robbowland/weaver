#!/bin/bash
# Subagents and other ways an agent works: subagent edits and their live list,
# the main agent looking around its own checkout, Bash and MCP edits, cwd
# changes, background work, and its task list. What nvim shows of it all is
# tested in the 5x suites; here the engine's state and what it tells nvim.
. "$(dirname "$0")/../lib.sh"
fresh

# ev EVENT CWD [EXTRA-JSON]: an event in agent A's session, through the hook
# Claude Code would use.
ev() { hooka 0 "$(printf '{"hook_event_name":"%s","cwd":"%s","session_id":"sid-sub"%s}' "$1" "$2" "${3:-}")"; }
edit_by() { printf ',"agent_id":"%s","tool_name":"Edit","tool_input":{"file_path":"%s"},"tool_response":{}' "$1" "$2"; }
bash_by() { printf ',"agent_id":"%s","tool_name":"Bash","tool_input":{"command":%s},"tool_response":{}' "$1" "$(jq -Rn --arg c "$2" '$c')"; }

fake_nvim 0
ev SessionStart "$T/repo" ',"source":"startup"'
ev UserPromptSubmit "$T/repo" ',"prompt":"implement both features"'

# --- subagents: a live list, and their edits move the root
ev SubagentStart "$T/repo" ',"agent_id":"sub-a","agent_type":"general-purpose"'
ev SubagentStart "$T/repo" ',"agent_id":"sub-b","agent_type":"Explore"'
ok "subagents starting are listed" "$(st 0 'subagents | map(.id + ":" + .type) | join(",")')" "sub-a:general-purpose,sub-b:Explore"
ev PostToolUse "$T/repo" "$(edit_by sub-a "$T/repo-wt-a/src/app.txt")"
ok "a subagent's edit in a worktree: the root follows" "$(st 0 root)|$(st 0 file)" "$T/repo-wt-a|$T/repo-wt-a/src/app.txt"
ev PostToolUse "$T/repo" "$(edit_by sub-b "$T/repo-wt-b/README.md")"
ok "another subagent in another worktree" "$(st 0 root)" "$T/repo-wt-b"
ev SubagentStop "$T/repo" ',"agent_id":"sub-b","agent_type":"Explore","agent_transcript_path":"/dev/null"'
ok "a subagent stopping leaves the list" "$(st 0 'subagents | map(.id) | join(",")')" "sub-a"
settle
ok "nvim is told of each edit, with the subagent" "$(nvim_events 0 agent_id | tr ',' '\n' | grep '^edit' | tr '\n' ' ')" "edit:sub-a edit:sub-b "
ok "...and each edit's file" "$(nvim_events 0 file | tr ',' '\n' | grep '^edit' | sed 's|.*/||' | tr '\n' ' ')" "app.txt README.md "
ok "...and its root" "$(nvim_events 0 root | tr ',' '\n' | grep '^edit' | sed 's|.*/||' | tr '\n' ' ')" "repo-wt-a repo-wt-b "

# --- the main agent looking around its own checkout keeps the worktree
ev UserPromptSubmit "$T/repo/src" ',"prompt":"<agent-message from=\"sub-b\"> [Subagent hand-back] done"'
ok "main agent cd'ing inside its checkout: root stays on the worktree" "$(st 0 root)" "$T/repo-wt-b"
ok "a subagent's report is not taken as your prompt" "$(st 0 prompt)|$(st 0 turn)" "implement both features|1"
ev PostToolUse "$T/repo-wt-b" "$(edit_by sub-b "$T/repo-wt-a/README.md")"
ok "a subagent's cwd is not the agent's" "$(st 0 cwd)" "$T/repo/src"

# --- CwdChanged moves it for real
ev CwdChanged "$T/repo-wt-a" ",\"old_cwd\":\"$T/repo\",\"new_cwd\":\"$T/repo-wt-a/src\""
ok "CwdChanged into a worktree: the root moves there" "$(st 0 root)|$(st 0 cwd)" "$T/repo-wt-a|$T/repo-wt-a/src"

# --- edits made through Bash are followed too
ev PostToolUse "$T/repo" "$(bash_by sub-b "cd $T/repo-wt-b && git status")"
sleep 0.1
sed -i '' 's/line two/line two, by sed/' "$T/repo-wt-b/src/app.txt"
ev PostToolUse "$T/repo" "$(bash_by sub-b "cd $T/repo-wt-b && sed -i '' 's/line two/line two, by sed/' src/app.txt")"
ok "Bash edit: root and file follow it" "$(st 0 root)|$(st 0 file)" "$T/repo-wt-b|$T/repo-wt-b/src/app.txt"
ok "...and the file joins the agent's files" "$(st 0 'files | map(select(endswith("repo-wt-b/src/app.txt"))) | length')" "1"
ev PostToolUse "$T/repo" "$(bash_by sub-a "cd $T/repo-wt-a && git log -1")"
ok "Bash that changes nothing: root stays" "$(st 0 root)" "$T/repo-wt-b"
settle
ok "nvim told to re-check files after a Bash command that changed nothing" "$(nvim_events 0 | tr ',' '\n' | tail -n 1)" "checktime"
ok "the log has the Bash commands" "$(jq -r 'select(.ev == "bash") | .cmd' "$(st 0 log)" | grep -c 'git')" "2"

# --- MCP edit tools are followed (defect 11); MCP reads are not looked at
sleep 0.1
printf 'from mcp\n' >"$T/repo-wt-a/mcp-made.txt"
ev PostToolUse "$T/repo" ",\"tool_name\":\"mcp__filesystem__write_file\",\"tool_input\":{\"path\":\"$T/repo-wt-a/mcp-made.txt\",\"content\":\"from mcp\"},\"tool_response\":{}"
ok "an MCP write: root and file follow it" "$(st 0 root)|$(st 0 file)" "$T/repo-wt-a|$T/repo-wt-a/mcp-made.txt"
printf 'sneaky\n' >"$T/repo-wt-b/sneaky.txt"
ev PostToolUse "$T/repo" ',"tool_name":"mcp__filesystem__read_file","tool_input":{"path":"x"},"tool_response":{}'
ok "an MCP read is not taken for an edit" "$(st 0 file)" "$T/repo-wt-a/mcp-made.txt"
rm -f "$T/repo-wt-b/sneaky.txt" "$T/repo-wt-a/mcp-made.txt"

# --- tasks
ev TaskCreated "$T/repo" ',"task_id":"1","task_subject":"Write the parser"'
ev TaskCreated "$T/repo" ',"task_id":"2","task_subject":"Test it"'
ev TaskCompleted "$T/repo" ',"task_id":"1","task_subject":"Write the parser"'
ok "the task list" "$(st 0 'tasks | map((if .done then "x" else "o" end) + " " + .subject) | join(", ")')" "x Write the parser, o Test it"
ok "the resume card shows it" "$(weaver _preview 0 | sed 's/\x1b\[[0-9;]*m//g' | grep -c '^tasks  *✓ Write the parser  ○ Test it')" "1"

# --- waiting on background subagents is not done
ev Stop "$T/repo" ',"background_tasks":[{"id":"x1","type":"subagent","status":"running","description":"Implement feature A"},{"id":"x2","type":"shell","status":"running","description":"npm run dev"}]'
ok "Stop with a subagent in the background: still working" "$(st 0 state)|$(st 0 background)" "working|1"
ok "...and the background subagent stays listed" "$(st 0 'subagents | map(.id) | join(",")')" "sub-a"
ok "dashboard: says so" "$(weaver ls | grep -c 'implement both features  *1 in background')" "1"
ok "resume card: what runs in the background" "$(weaver _preview 0 | sed 's/\x1b\[[0-9;]*m//g' | grep -c '^background *Implement feature A')" "1"
ev Notification "$T/repo" ',"notification_type":"idle_prompt","message":"Claude is waiting for your input"'
ok "idle reminder meanwhile: still working" "$(st 0 state)" "working"
ev UserPromptSubmit "$T/repo" ',"prompt":"<agent-message from=\"x1\"> [Subagent hand-back] done"'
ev Stop "$T/repo" ',"background_tasks":[]'
ok "last report handled: done" "$(st 0 state)|$(st 0 background)" "done|0"
ok "...and no subagent is listed any more" "$(st 0 'subagents | length')" "0"
# --- a prompt typed while the agent works still reaches the bar
ev UserPromptSubmit "$T/repo" ',"prompt":"and add tests","session_title":"Both features"'
settle
ok "a prompt while working: nvim is told the new prompt, turn and title" \
	"$(nvq 0 "luaeval('(function() for i = #_G.ev, 1, -1 do local e = _G.ev[i]; if e.ev == \"state\" then return e.prompt .. \"|\" .. e.turn .. \"|\" .. e.title end end end)()')")" \
	"and add tests|$(st 0 turn)|Both features"

# --- a prompt with control characters (pasted escapes) still reaches nvim
hooka 0 "$(jq -nc --arg c "$T/repo" '{hook_event_name:"UserPromptSubmit", cwd:$c, session_id:"sid-sub", prompt:"paste\r\u001b[31mred\u001b[0m"}')"
settle
ok "a pasted prompt with escapes: nvim gets valid JSON, the escapes as spaces" \
	"$(nvq 0 "luaeval('(function() for i = #_G.ev, 1, -1 do if _G.ev[i].ev == \"state\" then return _G.ev[i].prompt end end end)()')")" \
	"paste [31mred [0m"
kill "$FAKE_NVIM_PID" 2>/dev/null
done_suite
