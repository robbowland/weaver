#!/bin/bash
# The demo's agent: a scripted stand-in for Claude Code. It prints a turn as
# Claude Code would, edits the worktree, and reports each step through
# weaver's hooks, so the board, the card and the Neovim beside it follow it
# as they would a real one. Read-only (--permission-mode plan), it reviews;
# else it adds the --loud flag, asking before it runs it.
set -u
sid="" plan=0 prompt=""
while [ $# -gt 0 ]; do
	case $1 in
	--session-id) sid=$2; shift ;;
	-n) shift ;;
	--permission-mode) [ "$2" = plan ] && plan=1; shift ;;
	*) prompt=$1 ;;
	esac
	shift
done
dir=$(pwd -P)
B=$'\e[1m' G=$'\e[90m' GR=$'\e[32m' N=$'\e[0m'

# hook EVENT [JSON FIELDS] [--async]: weaver hears of it, as from Claude Code.
hook() {
	local f=${2:-}
	[ -n "$f" ] || f='{}'
	jq -nc --arg e "$1" --arg s "$sid" --arg c "$dir" "{hook_event_name: \$e, session_id: \$s, cwd: \$c, transcript_path: \"/dev/null\"} + $f" |
		WEAVER_AGENT=claude WEAVER_AGENT_PID=$$ wvr hook ${3:-} >/dev/null 2>&1
}
say() { printf '%s\n' "$@"; sleep 0.25; }
tool() { # tool NAME ARG RESULT
	say "" "${GR}●${N} ${B}$1${N}($2)"
	sleep 0.6
	[ -n "${3:-}" ] && say "  ${G}⎿  $3${N}"
}
wait_on() { while :; do read -r _ || sleep 1; done; }

hook SessionStart '{source: "startup"}'
first=${prompt%%$'\n'*}
say "${G}>${N} ${first:0:40}"
hook UserPromptSubmit "$(jq -nc --arg p "$prompt" '{prompt: $p, prompt_id: "p1"}')"
sleep 1

if [ "$plan" = 1 ]; then
	tool Bash "git diff origin/main" "+4 −1 in greet.py"
	hook PreToolUse '{tool_name: "Bash", tool_input: {command: "git diff origin/main"}}'
	hook PostToolUse '{tool_name: "Bash", tool_input: {command: "git diff origin/main"}, tool_response: {}}' --async
	tool Read greet.py "Read 14 lines"
	hook PreToolUse '{tool_name: "Read", tool_input: {file_path: "greet.py"}}'
	hook PostToolUse '{tool_name: "Read", tool_input: {file_path: "greet.py"}, tool_response: {}}' --async
	sleep 6
	msg="--quiet works, but --quiet --loud still prints: make the two exclusive, and test it."
	say "" "${GR}●${N} --quiet works, but" "  --quiet --loud still prints." "" "  Make the two exclusive, and add" "  a test for it."
	hook Stop "$(jq -nc --arg m "$msg" '{last_assistant_message: $m}')"
	say "" "${G}>${N} "
	wait_on
fi

for t in "1 Parse --loud" "2 Uppercase the greeting" "3 Run it"; do
	hook TaskCreated "$(jq -nc --arg i "${t%% *}" --arg s "${t#* }" '{task_id: $i, task_subject: $s}')" --async
done
tool Read greet.py "Read 10 lines"
hook PreToolUse '{tool_name: "Read", tool_input: {file_path: "greet.py"}}'
hook PostToolUse '{tool_name: "Read", tool_input: {file_path: "greet.py"}, tool_response: {}}' --async
sleep 0.8
cat >greet.py <<'PY'
import argparse


def main():
    p = argparse.ArgumentParser()
    p.add_argument("name")
    p.add_argument("--loud", action="store_true", help="shout it")
    args = p.parse_args()
    msg = f"Hello, {args.name}!"
    print(msg.upper() if args.loud else msg)


if __name__ == "__main__":
    main()
PY
tool Update greet.py "Added 3 lines"
hook PostToolUse "$(jq -nc --arg f "$dir/greet.py" '{tool_name: "Edit", tool_input: {file_path: $f}, tool_response: {}}')" --async
hook TaskCompleted '{task_id: "1", task_subject: "Parse --loud"}' --async
hook TaskCompleted '{task_id: "2", task_subject: "Uppercase the greeting"}' --async
sleep 0.8
cmd="python greet.py --loud Ada"
tool Bash "$cmd"
say "" "  ${B}Run this command?${N}" "  ${GR}❯${N} 1. Yes" "    2. No"
hook PermissionRequest "$(jq -nc --arg c "$cmd" '{tool_name: "Bash", tool_input: {command: $c}}')"
until read -rsn1 k && [ "$k" = 1 -o "$k" = y -o -z "$k" ]; do :; done
hook PostToolUse "$(jq -nc --arg c "$cmd" '{tool_name: "Bash", tool_input: {command: $c}, tool_response: {}}')" --async
# The question goes once answered.
printf '\e[4A\e[J'
say "  ${G}⎿  HELLO, ADA!${N}"
hook TaskCompleted '{task_id: "3", task_subject: "Run it"}' --async
sleep 0.8
msg="Added --loud: it uppercases the greeting, and python greet.py --loud Ada prints HELLO, ADA!"
say "" "${GR}●${N} Added --loud: it uppercases the" "  greeting. python greet.py --loud" "  Ada prints HELLO, ADA!"
hook Stop "$(jq -nc --arg m "$msg" '{last_assistant_message: $m}')"
say "" "${G}>${N} "
wait_on
