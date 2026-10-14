#!/bin/bash
# Threads: `weaver new` opens a tab with the agent's nvim on the left and the
# agent on the right, in a new worktree or this checkout; `weaver close`
# closes it, and removes the worktree only when nothing would be lost.
. "$(dirname "$0")/../lib.sh"
fresh

# Fake agents: record their arguments, then stay at a prompt like an agent.
for a in claude codex; do
	cat >"$AT/bin/$a" <<EOF
#!/bin/bash
printf '%s\n' "\$@" >"$AT/args.$a.\${ZELLIJ_PANE_ID:-none}"
echo "FAKE $a \$*"
[ -t 0 ] && exec bash --noprofile --norc -i
exit 0
EOF
	chmod +x "$AT/bin/$a"
done

tab_named() { Z list-tabs -j | jq -r --arg n "$1" '[.[] | select(.name == $n) | .tab_id] | .[0] // empty'; }
# The panes of thread tab $1: the agent's into AG, its nvim's into NVP.
thread_panes() {
	local j
	j=$(Z list-panes -a -j)
	AG=$(printf '%s' "$j" | jq -r --argjson t "$1" '[.[] | select((.is_plugin | not) and .tab_id == $t
		and ((.terminal_command // "") | test(" _agent ")))] | .[0].id // empty')
	NVP=$(printf '%s' "$j" | jq -r --argjson t "$1" '[.[] | select((.is_plugin | not) and .tab_id == $t
		and ((.terminal_command // "") | test(" nvim --thread ")))] | .[0].id // empty')
}
# Wait (up to 5s) for shell condition $1.
waitfor() {
	local i
	for i in $(seq 50); do
		eval "$1" && return 0
		sleep 0.1
	done
	return 1
}
th() { jq -r ".$2" "$SD/${SESSION}_$1.thread" 2>/dev/null; }
columns() { Z list-panes -a -j | jq -r ".[] | select((.is_plugin | not) and .id == $1) | .pane_columns"; }
TABS=""

# --- a thread in a new worktree, from the arguments
out=$(cd "$T/repo" && weaver new --no-focus 'Fix the flaky test!' 2>&1)
ok "new: says where the thread is" "$(printf '%s' "$out" | grep -c 'thread fix-the-flaky-test in')" "1"
tab=$(tab_named fix-the-flaky-test)
TABS="$TABS $tab"
ok "new: a tab named after the branch" "${tab:+yes}" "yes"
thread_panes "$tab"
ok "new: nvim and the agent in it" "${NVP:+nvim}|${AG:+agent}|$(tabpanes "$tab" | jq length)" "nvim|agent|2"
WT=$T/repo.fix-the-flaky-test
ok "new: Worktrunk made the worktree, on a new branch" \
	"$(git -C "$WT" symbolic-ref --short HEAD 2>/dev/null)" "fix-the-flaky-test"
waitfor "[ -f '$AT/args.claude.$AG' ]"
ok "new: the agent started with the task as its prompt" "$(cat "$AT/args.claude.$AG" 2>/dev/null)" "Fix the flaky test!"
ok "new: the thread record" \
	"$(th "$AG" name)|$(th "$AG" task)|$(th "$AG" branch)|$(th "$AG" worktree)|$(th "$AG" here)|$(th "$AG" worktree_created)|$(th "$AG" agent)|$(th "$AG" repo)|$(th "$AG" pane)" \
	"fix-the-flaky-test|Fix the flaky test!|fix-the-flaky-test|$WT|false|true|claude|$T/repo|$AG"
a=$(columns "$AG") n=$(columns "$NVP")
ok "new: the agent on the right, a fifth of the width" "$([ "$a" -ge 36 ] && [ "$a" -le 44 ] && [ "$n" -gt "$a" ] && echo yes)" "yes"
ok "new: the tab keeps the session's status bar" \
	"$(Z list-panes -a -j | jq -r "[.[] | select(.is_plugin and .tab_id == $tab and .title == \"zellij:compact-bar\")] | length")" "1"
waitfor "[ -S '$(sock "$AG")' ]"
unstick "$AG"
ok "new: the nvim beside it is the agent's weaver nvim" "$(nvim_pane "$AG")|$(nvq "$AG" 'fnamemodify(getcwd(), ":t")')" \
	"$NVP|repo.fix-the-flaky-test"
ok "new: its rendezvous is cleaned up" "$(ls "$SD/new" 2>/dev/null | wc -l | tr -d ' ')" "0"
hook "$AG" SessionStart "$WT"
settle
ok "new: the agent's first hook finds its nvim" "$(st "$AG" nvim_pane)" "$NVP"
weaver edit "$AG" >/dev/null 2>&1
ok "new: Alt v on the agent goes to that nvim, never a second one" "$(client)|$(tabpanes "$tab" | jq length)" "terminal_$NVP|2"

# --- here: the current checkout, a long task
long='Quick question: why does the readme say the app has three lines when the source file clearly has more?'
out=$(cd "$T/repo-wt-b/src" && weaver new --here --no-focus "$long" 2>&1)
name=quick-question-why-does-the-readme-say-the-app
ok "here: named from the task, at most 48 characters, cut at a word" "$(printf '%s' "$out" | grep -c "thread $name in")" "1"
tab=$(tab_named "$name")
TABS="$TABS $tab"
thread_panes "$tab"
waitfor "[ -f '$SD/${SESSION}_$AG.thread' ]"
ok "here: in the checkout it was started from, no worktree made" \
	"$(th "$AG" worktree)|$(th "$AG" here)|$(th "$AG" worktree_created)|$(th "$AG" branch)" "$T/repo-wt-b|true|false|feat-b"
ok "here: no new branch" "$(git -C "$T/repo" branch --list "$name*" | wc -l | tr -d ' ')" "0"

# --- an existing branch continues in its own worktree; codex
out=$(cd "$T/repo" && weaver new --no-focus --branch feat-a --agent codex "carry on with a" 2>&1)
tab=$(tab_named feat-a)
TABS="$TABS $tab"
thread_panes "$tab"
waitfor "[ -f '$AT/args.codex.$AG' ]"
ok "existing branch: its worktree, not a new one; codex got the task" \
	"$(th "$AG" worktree)|$(th "$AG" worktree_created)|$(cat "$AT/args.codex.$AG" 2>/dev/null)" "$T/repo-wt-a|false|carry on with a"

# --- without Worktrunk: git, at Worktrunk's default path
out=$(cd "$T/repo" && WEAVER_WORKTRUNK=0 weaver new --no-focus "plain git" 2>&1)
tab=$(tab_named plain-git)
TABS="$TABS $tab"
thread_panes "$tab"
waitfor "[ -f '$SD/${SESSION}_$AG.thread' ]"
ok "git: a worktree beside the repository" \
	"$(git -C "$T/repo.plain-git" symbolic-ref --short HEAD 2>/dev/null)|$(th "$AG" worktree_created)" "plain-git|true"
GIT_AG=$AG

# --- refusals
out=$(cd "$AT" && weaver new --no-focus "outside" 2>&1)
ok "new: outside a repository with no repository used yet... the last one is used" \
	"$(printf '%s' "$out" | grep -c 'thread outside in')" "1"
tab=$(tab_named outside)
TABS="$TABS $tab"
out=$(cd "$T/repo" && weaver new --no-focus --agent nope "x" 2>&1)
ok "new: an unknown agent is refused" "$(printf '%s' "$out" | grep -c 'unknown agent')" "1"
out=$(cd "$T/repo" && weaver new --no-focus --branch 'bad..name' "x" 2>&1)
ok "new: an invalid branch is refused" "$(printf '%s' "$out" | grep -c 'not a valid branch name')" "1"
out=$(cd "$T/repo" && weaver new </dev/null 2>&1)
ok "new: no task and no terminal says how" "$(printf '%s' "$out" | grep -c 'what should the agent do')" "1"

# --- rerun of an agent's pane resumes it, never sends the task again
rm -f "$AT/args.claude.77"
ZELLIJ_PANE_ID=77 weaver _agent gone-token claude </dev/null >/dev/null 2>&1
ok "rerun: claude --continue" "$(tr '\n' ' ' <"$AT/args.claude.77")" "--continue "
printf '{"pane":77,"session_id":"sid-77","state":"ended"}\n' >"$SD/${SESSION}_77.json"
ZELLIJ_PANE_ID=77 weaver _agent gone-token claude </dev/null >/dev/null 2>&1
ok "rerun: its own session when known" "$(tr '\n' ' ' <"$AT/args.claude.77")" "--resume sid-77 "
rm -f "$SD/${SESSION}_77.json"

# --- the form: Alt N, type, Enter
focus 0
key 1b4e
sleep 1.5
printf 'form thread' | xxd -p | while read -r h; do key "$h"; done
sleep 0.8
fl=$(Z list-panes -a -j | jq -r '[.[] | select((.is_plugin | not) and .is_floating and ((.terminal_command // "") | test("weaver new")))] | .[0].id // empty')
scr=$(Z dump-screen --pane-id "terminal_$fl" 2>/dev/null)
ok "form: the task, the branch it makes and where" \
	"$(printf '%s' "$scr" | grep -c 'task  form thread')|$(printf '%s' "$scr" | grep -c 'branch *form-thread')|$(printf '%s' "$scr" | grep -c 'new worktree')" "1|1|1"
key 09
sleep 0.8
scr=$(Z dump-screen --pane-id "terminal_$fl" 2>/dev/null)
ok "form: tab switches to this checkout" "$(printf '%s' "$scr" | grep -c "this checkout")" "1"
key 0d
waitfor "[ -n \"\$(tab_named form-thread)\" ]"
tab=$(tab_named form-thread)
TABS="$TABS $tab"
thread_panes "$tab"
waitfor "[ -f '$SD/${SESSION}_$AG.thread' ]"
ok "form: enter starts it, here, in the focused pane's checkout" "$(th "$AG" here)|$(th "$AG" worktree)" "true|$T/repo"
ok "form: you land on the agent" "$(client)" "terminal_$AG"

focus 0
before=$(Z list-tabs -j | jq length)
key 1b4e
sleep 1.5
key 1b
sleep 1
ok "form: esc cancels, nothing made" "$(Z list-tabs -j | jq length)|$(Z list-panes -a -j | jq '[.[] | select(.is_floating)] | length')" \
	"$before|0"

for t in $TABS; do Z close-tab-by-id "$t" >/dev/null 2>&1; done
sleep 0.5
done_suite
