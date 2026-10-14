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
# Wait (up to 10s) for shell condition $1.
waitfor() {
	local i
	for i in $(seq 100); do
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
ok "new: no remote, so from the local main, and it says so" "$(printf '%s' "$out" | grep -c 'no remote: starts from the local main')" "1"
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

# A narrow agent is widened to its minimum when the tab opens.
out=$(cd "$T/repo" && WEAVER_AGENT_MIN_COLS=70 weaver new --no-focus --here "wide agent" 2>&1)
tab=$(tab_named wide-agent)
TABS="$TABS $tab"
thread_panes "$tab"
a=$(columns "$AG")
ok "new: the agent gets at least WEAVER_AGENT_MIN_COLS" "$([ "$a" -ge 69 ] && [ "$a" -le 72 ] && echo yes || echo "$a")" "yes"

# WEAVER_CLAUDE names the command a claude thread runs.
out=$(cd "$T/repo" && WEAVER_CLAUDE="$AT/bin/codex --from-env" weaver new --no-focus --here "other command" 2>&1)
tab=$(tab_named other-command)
TABS="$TABS $tab"
thread_panes "$tab"
waitfor "[ -f '$AT/args.codex.$AG' ]"
ok "new: WEAVER_CLAUDE is the command a claude thread runs" "$(tr '\n' ' ' <"$AT/args.codex.$AG" 2>/dev/null)" "--from-env other command "

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

# --- where a new branch starts: the remote's default branch, fetched first
git clone -q --bare "$T/repo" "$T/origin.git"
git clone -q "$T/origin.git" "$T/up"
git clone -q "$T/origin.git" "$T/elsewhere"
for r in up elsewhere; do git -C "$T/$r" config user.email t@example.com && git -C "$T/$r" config user.name t; done
git -C "$T/elsewhere" commit -q --allow-empty -m "pushed from elsewhere"
git -C "$T/elsewhere" push -q origin main
git -C "$T/elsewhere" push -q origin main:feature-x
pushed=$(git -C "$T/elsewhere" rev-parse HEAD)
out=$(cd "$T/up" && weaver new --no-focus "from the remote" 2>&1)
tab=$(tab_named from-the-remote)
TABS="$TABS $tab"
thread_panes "$tab"
waitfor "[ -f '$SD/${SESSION}_$AG.thread' ]"
ok "base: fetched first, the branch starts from origin/main" \
	"$(git -C "$T/up.from-the-remote" merge-base --is-ancestor "$pushed" HEAD && echo fresh)|$(th "$AG" base)|$(printf '%s' "$out" | grep -c 'worktree for from-the-remote from origin/main')" "fresh|origin/main|1"
ok "base: your local main is left as it was" "$(git -C "$T/up" merge-base --is-ancestor "$pushed" main || echo untouched)" "untouched"
out=$(cd "$T/up" && weaver new --no-focus --branch feature-x "carry on with x" 2>&1)
tab=$(tab_named feature-x)
TABS="$TABS $tab"
ok "base: a branch only on the remote is checked out to track it" \
	"$(git -C "$T/up.feature-x" rev-parse --abbrev-ref '@{u}' 2>/dev/null)" "origin/feature-x"

# @ is the HEAD of the checkout you are in.
git -C "$T/up" worktree add -q -b side "$T/up-side"
git -C "$T/up-side" commit -q --allow-empty -m "on the side"
side=$(git -C "$T/up-side" rev-parse HEAD)
out=$(cd "$T/up-side" && weaver new --no-focus --base @ "stack on side" 2>&1)
tab=$(tab_named stack-on-side)
TABS="$TABS $tab"
ok "base: --base @ stacks on the checkout you are in" \
	"$(git -C "$T/up.stack-on-side" merge-base --is-ancestor "$side" HEAD && echo stacked)" "stacked"

# The form fetches while you type, and its header says how it is going (a
# remote that takes 2s to answer).
git -C "$T/up" config protocol.ext.allow always
git -C "$T/up" remote set-url origin "ext::sh -c sleep% 2;% %S% $T/origin.git"
utab=$(Z new-tab --name up --cwd "$T/up" -- bash --noprofile --norc -i)
TABS="$TABS $utab"
rm -rf "$SD/fetch"
sleep 0.5
key 1b4e
sleep 1.2
fl=$(Z list-panes -a -j | jq -r '[.[] | select((.is_plugin | not) and .is_floating)] | .[0].id // empty')
ok "form: the fetch starts as the form opens" \
	"$(Z dump-screen --pane-id "terminal_$fl" 2>/dev/null | grep -cE 'base +origin/main +fetching')" "1"
sleep 2.5
ok "form: ...and its header says when it is done, untouched" \
	"$(Z dump-screen --pane-id "terminal_$fl" 2>/dev/null | grep -cE 'base +origin/main +fetched')" "1"
key 1b
sleep 0.5
git -C "$T/up" remote set-url origin "$T/origin.git"

# Offline, or too slow: the local default branch, and a line saying so.
git -C "$T/up" remote set-url origin "$T/nowhere.git"
rm -rf "$SD/fetch"
out=$(cd "$T/up" && weaver new --no-focus "offline one" 2>&1)
TABS="$TABS $(tab_named offline-one)"
ok "base: offline, the local main, and it says so" \
	"$(printf '%s' "$out" | grep -c 'could not fetch origin (offline?): starts from the local main')|$(git -C "$T/up.offline-one" rev-parse HEAD)" \
	"1|$(git -C "$T/up" rev-parse main)"
git -C "$T/up" config protocol.ext.allow always
git -C "$T/up" remote set-url origin 'ext::sh -c sleep% 6'
rm -rf "$SD/fetch"
t=$(ms sh -c "cd '$T/up' && WEAVER_FETCH_TIMEOUT=1 weaver new --no-focus 'slow one' >'$AT/slow.out' 2>&1")
TABS="$TABS $(tab_named slow-one)"
ok "base: a fetch that takes too long is given up after WEAVER_FETCH_TIMEOUT" \
	"$(grep -c 'origin took over 1s to fetch: starts from the local main' "$AT/slow.out")|$([ "$t" -lt 4000 ] && echo quick)" "1|quick"
ok "base: no remote: the local main, and it says so" \
	"$(cd "$T/repo" && weaver new --no-focus --here "x" >/dev/null 2>&1; cd "$T/repo" && weaver new --no-focus "no remote here" 2>&1 | grep -c 'no remote: starts from the local main')" "1"
TABS="$TABS $(tab_named x) $(tab_named no-remote-here)"
git -C "$T/up" remote remove origin

# --- Alt v brings a thread's split back when it reopens its nvim
out=$(cd "$T/repo" && weaver new --no-focus --here "split again" 2>&1)
tab=$(tab_named split-again)
TABS="$TABS $tab"
thread_panes "$tab"
waitfor "[ -f '$SD/${SESSION}_$AG.nvim' ]"
Z close-pane --pane-id "terminal_$NVP"
sleep 0.5
ok "split: with its nvim closed the agent has the tab" "$(columns "$AG")" "200"
t=$(ms weaver edit "$AG")
nv=$(nvim_pane "$AG")
a=$(columns "$AG") n=$(columns "$nv")
echo "  (Alt v reopening a thread's nvim with its split: ${t}ms)"
ok "split: nvim back on the left, the agent a fifth of the width again" \
	"$([ "$a" -ge 36 ] && [ "$a" -le 44 ] && [ "$n" -gt "$a" ] && echo yes)|$(Z list-panes -a -j | jq -r "[.[] | select((.is_plugin | not) and (.id == $nv or .id == $AG))] | sort_by(.pane_x) | map(.id | tostring) | join(\",\")")" \
	"yes|$nv,$AG"
ok "split: the same agent, still running" \
	"$(Z list-panes -a -j | jq -r ".[] | select((.is_plugin | not) and .id == $AG) | .exited")|$(cat "$AT/args.claude.$AG")" "false|split again"
# The agent's minimum (WEAVER_AGENT_MIN_COLS, 44 by default) wins over its
# 20% when that is narrower: here 60 of 200 columns.
Z close-pane --pane-id "terminal_$nv"
sleep 0.5
WEAVER_AGENT_MIN_COLS=60 weaver edit "$AG" >/dev/null 2>&1
nv=$(nvim_pane "$AG")
a=$(columns "$AG")
ok "split: at least WEAVER_AGENT_MIN_COLS for the agent" "$([ "$a" -ge 56 ] && [ "$a" -le 64 ] && echo yes || echo "$a")" "yes"
extra=$(Z new-pane --tab-id "$tab" --direction down --cwd "$T/repo" -- bash --noprofile --norc)
extra=${extra#terminal_}
sleep 0.3
Z close-pane --pane-id "terminal_$nv"
sleep 0.5
before=$(columns "$extra")
weaver edit "$AG" >/dev/null 2>&1
ok "split: with a pane you added, as before: nothing of yours resized" \
	"$(tabpanes "$tab" | jq length)|$(columns "$extra")" "3|$before"

# --- closing
tab=$(tab_named fix-the-flaky-test)
thread_panes "$tab"
out=$(weaver close "$AG" </dev/null 2>&1)
ok "close: without a terminal it needs --yes" "$?|$(printf '%s' "$out" | grep -c -- '--yes')" "1|1"
printf 'wip\n' >"$WT/wip.txt"
out=$(weaver close --yes --remove-worktree "$AG" 2>&1)
ok "close: the tab goes, with the agent and its nvim" "$(tab_named fix-the-flaky-test)|$(ls "$SD/${SESSION}_$AG".* 2>/dev/null | wc -l | tr -d ' ')" "|0"
ok "close: a worktree with uncommitted work stays, and it says why" \
	"$([ -d "$WT" ] && echo kept)|$(printf '%s' "$out" | grep -c '1 uncommitted file')" "kept|1"

out=$(weaver close --yes --remove-worktree "$GIT_AG" 2>&1)
ok "close: a clean, merged worktree goes when asked; its branch stays" \
	"$([ -d "$T/repo.plain-git" ] || echo removed)|$(git -C "$T/repo" branch --list plain-git | wc -l | tr -d ' ')" "removed|1"

# A pane of yours in the worktree (another tab) keeps it, and close says why
# on stdout, nothing on stderr.
out=$(cd "$T/repo" && weaver new --no-focus "someone is in it" 2>&1)
tab=$(tab_named someone-is-in-it)
thread_panes "$tab"
waitfor "[ -f '$SD/${SESSION}_$AG.thread' ]"
inside=$(Z new-tab --name inside --cwd "$T/repo.someone-is-in-it" -- bash --noprofile --norc -i)
TABS="$TABS $inside"
sleep 0.5
user=$(tabpanes "$inside" | jq -r '.[0]')
out=$(weaver close --yes --remove-worktree "$AG" 2>"$AT/close.err")
ok "close: a worktree another pane is in stays, and it says which" \
	"$([ -d "$T/repo.someone-is-in-it" ] && echo kept)|$(printf '%s' "$out" | grep -c "pane $user is in it")|$(wc -c <"$AT/close.err" | tr -d ' ')" "kept|1|0"

# A thread's commits that are neither merged nor pushed keep its worktree.
out=$(cd "$T/repo" && weaver new --no-focus "commit something" 2>&1)
tab=$(tab_named commit-something)
thread_panes "$tab"
waitfor "[ -f '$SD/${SESSION}_$AG.thread' ]"
git -C "$T/repo.commit-something" commit -q --allow-empty -m "work"
out=$(weaver close --yes --remove-worktree "$AG" 2>&1)
ok "close: unmerged, unpushed commits keep the worktree" \
	"$([ -d "$T/repo.commit-something" ] && echo kept)|$(printf '%s' "$out" | grep -c 'neither merged nor pushed')" "kept|1"

# Closing from nvim's pane closes its agent; panes you added stay.
tab=$(tab_named feat-a)
thread_panes "$tab"
extra=$(Z new-pane --tab-id "$tab" --cwd "$T/repo" -- bash --noprofile --norc)
extra=${extra#terminal_}
ZELLIJ_PANE_ID=$extra weaver close --yes >/dev/null 2>&1
ok "close: from a shell beside the thread, its agent; the tab stays for that shell" \
	"$(tab_named feat-a)|$(tabpanes "$tab")" "$tab|[$extra]"
ok "close: a thread's existing worktree is never removed" "$([ -d "$T/repo-wt-a" ] && echo kept)" "kept"

# An agent that is not a thread: just its pane.
ntab=$(Z new-tab --name loose --cwd "$T/repo" -- bash --noprofile --norc -i)
TABS="$TABS $ntab $tab"
loose=$(tabpanes "$ntab" | jq -r '.[0]')
hook "$loose" SessionStart "$T/repo"
weaver close --yes "$loose" >/dev/null 2>&1
ok "close: an agent that is not a thread" "$(tab_named loose)|$([ -f "$SD/${SESSION}_$loose.json" ] || echo forgotten)" "|forgotten"

# Asked on the terminal, from a floating pane in the thread's own tab: the
# tab takes that pane with it, so the rest finishes in the background.
out=$(cd "$T/repo" && weaver new "ask me" 2>&1)
tab=$(tab_named ask-me)
thread_panes "$tab"
waitfor "[ -f '$SD/${SESSION}_$AG.thread' ]"
Z new-pane --floating --close-on-exit -- weaver close "$NVP" >/dev/null
sleep 1
fl=$(Z list-panes -a -j | jq -r '[.[] | select((.is_plugin | not) and .is_floating)] | .[0].id // empty')
scr=$(Z dump-screen --pane-id "terminal_$fl" 2>/dev/null)
ok "close: given its nvim's pane, says what goes" \
	"$(printf '%s' "$scr" | grep -c 'close  ask-me')|$(printf '%s' "$scr" | grep -c 'goes  the agent, its nvim and the tab')|$(printf '%s' "$scr" | grep -c 'y close   n keep')" "1|1|1"
key 6e
sleep 0.8
ok "close: n keeps it" "$(tab_named ask-me)" "$tab"
Z new-pane --floating --close-on-exit -- weaver close "$AG" >/dev/null
sleep 1
fl=$(Z list-panes -a -j | jq -r '[.[] | select((.is_plugin | not) and .is_floating)] | .[0].id // empty')
key 79
sleep 0.5
ok "close: then offers the clean, merged worktree" \
	"$(Z dump-screen --pane-id "terminal_$fl" 2>/dev/null | grep -c 'remove  the worktree .*repo.ask-me · branch ask-me stays')" "1"
key 79
waitfor "[ -z \"\$(tab_named ask-me)\" ] && [ ! -d '$T/repo.ask-me' ]"
ok "close: y, y: the tab and the worktree are gone, the branch stays" \
	"$(tab_named ask-me)|$([ -d "$T/repo.ask-me" ] || echo removed)|$(git -C "$T/repo" branch --list ask-me | wc -l | tr -d ' ')" "|removed|1"

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
ok "form: the pane fits the form, borderless (fzf draws the frame)" \
	"$(Z list-panes -a -j | jq -r ".[] | select((.is_plugin | not) and .id == $fl) | \"\(.pane_columns) \(.pane_rows > 8 and .pane_rows < 16)\"")" "96 true"
ok "form: the task, the branch it makes and where" \
	"$(printf '%s' "$scr" | grep -c 'task  form thread')|$(printf '%s' "$scr" | grep -c 'branch *form-thread')|$(printf '%s' "$scr" | grep -c 'new worktree')" "1|1|1"
key 1b62 # alt-b: the branch
sleep 1
printf -- '-x' | xxd -p | while read -r h; do key "$h"; done
key 0d
sleep 0.8
key 1b5b5a # shift-tab: the agent
sleep 0.8
scr=$(Z dump-screen --pane-id "terminal_$fl" 2>/dev/null)
ok "form: alt-b edits the branch, shift-tab picks the agent" \
	"$(printf '%s' "$scr" | grep -c 'branch *form-thread-x')|$(printf '%s' "$scr" | grep -c 'agent *codex')" "1|1"
key 1b42 # alt-B: the base
sleep 1
key 40
sleep 0.5
key 0d
sleep 0.8
ok "form: alt-B picks the base" "$(Z dump-screen --pane-id "terminal_$fl" 2>/dev/null | grep -cE 'base +@ +the HEAD of main')" "1"
key 1b5b5a
key 09
sleep 0.8
scr=$(Z dump-screen --pane-id "terminal_$fl" 2>/dev/null)
ok "form: tab switches to this checkout" "$(printf '%s' "$scr" | grep -c "this checkout")|$(printf '%s' "$scr" | grep -c 'agent *claude')" "1|1"
key 0d
waitfor "[ -n \"\$(tab_named form-thread)\" ]"
tab=$(tab_named form-thread)
TABS="$TABS $tab"
thread_panes "$tab"
waitfor "[ -f '$SD/${SESSION}_$AG.thread' ]"
ok "form: enter starts it, here, in the focused pane's checkout" "$(th "$AG" here)|$(th "$AG" worktree)" "true|$T/repo"
ok "form: you land on the agent" "$(client)" "terminal_$AG"

# A long task, written in $EDITOR (nvim, from run.sh).
focus 0
key 1b4e
sleep 1.5
printf 'long one' | xxd -p | while read -r h; do key "$h"; done
key 05 # ctrl-e
sleep 2
printf 'Gosecond line' | xxd -p | while read -r h; do key "$h"; done
key 1b
printf ':wq' | xxd -p | while read -r h; do key "$h"; done
key 0d
sleep 2
fl=$(Z list-panes -a -j | jq -r '[.[] | select((.is_plugin | not) and .is_floating)] | .[0].id // empty')
ok "form: ctrl-e writes the task in \$EDITOR; the form shows its first line and the rest" \
	"$(Z dump-screen --pane-id "terminal_$fl" 2>/dev/null | grep -cE 'task  long one( |$)|[+]1 more lines')" "2"
key 0d
waitfor "[ -n \"\$(tab_named long-one)\" ]"
tab=$(tab_named long-one)
TABS="$TABS $tab"
thread_panes "$tab"
waitfor "[ -f '$AT/args.claude.$AG' ]"
ok "form: named after its first line; the agent gets all of it" "$(cat "$AT/args.claude.$AG" 2>/dev/null | tr '\n' '|')" "long one|second line|"

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
