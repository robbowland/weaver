# Helpers for the suites in test/suites/; sourced. Run the suites through
# test/run.sh, which sets AT and starts the zellij session.
: "${AT:?run the suites through test/run.sh}"

T=$AT/t
SD=$AT/state/agentnav
SESSION=$(cat "$AT/session")
export ZELLIJ_SESSION_NAME=$SESSION XDG_STATE_HOME=$AT/state PATH="$AT/bin:$PATH"
pass=0 fail=0

ok() {
	if [ "$2" = "$3" ]; then
		echo "PASS $1"
		pass=$((pass + 1))
	else
		echo "FAIL $1: got [$2] want [$3]"
		fail=$((fail + 1))
	fi
}
done_suite() {
	echo "-- $pass passed, $fail failed"
	exit $fail
}

Z() { zellij action "$@"; }
# key HEX: type bytes into the zellij client (1b65 = Alt e, 0d = Enter, ...)
key() { printf '%s\n' "$1" >"$AT/keys.fifo"; }
client() { Z list-clients | tail -n 1 | awk '{print $2}'; }
tabpanes() { Z list-panes -a -j | jq -c "[.[] | select((.is_plugin|not) and .tab_id==$1 and (.is_floating|not)) | .id]"; }
ms() { perl -MTime::HiRes=time -e 'my $t=time; system(@ARGV); printf "%d", (time-$t)*1000' -- "$@"; }

# hook PANE EVENT CWD [FILE] [PROMPT] [SID]: fake one Claude Code hook event.
hook() {
	jq -nc --arg e "$2" --arg c "$3" --arg f "${4:-}" --arg p "${5:-}" --arg s "${6:-sid-$1}" \
		'{hook_event_name:$e, cwd:$c, session_id:$s}
		+ (if $f != "" then {tool_name:"Edit", tool_input:{file_path:$f}} else {} end)
		+ (if $p != "" then {prompt:$p} else {} end)' |
		ZELLIJ_PANE_ID=$1 agentnav hook
}
# hookj PANE JSON [ARGS]: fake a hook event from raw JSON.
hookj() {
	local pane=$1 json=$2
	shift 2
	printf '%s' "$json" | ZELLIJ_PANE_ID=$pane agentnav hook "$@"
}
st() { jq -r ".$2" "$SD/${SESSION}_$1.json"; }

sock() { printf '/tmp/agentnav-%s/%s_%s.sock' "$(id -u)" "$SESSION" "$1"; }
# nvq AGENT EXPR: evaluate EXPR in the agent's nvim (3s timeout).
nvq() {
	perl -e 'alarm 3; exec @ARGV' nvim --server "$(sock "$1")" --remote-expr "$2" </dev/null || printf 'TIMEOUT'
	echo
}
nvsend() { nvim --server "$(sock "$1")" --remote-send "$2"; }
nvst() { nvq "$1" 'fnamemodify(getcwd(), ":t") . "|" . fnamemodify(bufname("%"), ":t")'; }
# The working directory of each tab in the agent's nvim, in order.
tabcwds() { nvq "$1" 'join(map(range(1, tabpagenr("$")), {_, n -> fnamemodify(getcwd(-1, n), ":t")}), ",")'; }
# A new nvim may sit at a startup hit-enter prompt from your own config;
# dismiss it like you would.
unstick() {
	if [ "$(nvq "$1" 1)" = TIMEOUT ]; then
		echo "  (startup hit-enter prompt in nvim for agent $1: pressing Enter)"
		key 0d
		sleep 1
	fi
}
nvim_pane() { cat "$SD/${SESSION}_$1.nvim"; }

# The standard scene: A started in the main checkout but edits worktree a;
# B waits for permission in worktree b; C (tab two) writes a new file in b.
feed() {
	hook 0 SessionStart "$T/repo"
	hook 0 UserPromptSubmit "$T/repo" "" "Refactor the app in worktree A"
	hook 0 PostToolUse "$T/repo" "$T/repo-wt-a/src/app.txt"
	hook 0 Stop "$T/repo"
	hook 1 SessionStart "$T/repo-wt-b"
	hook 1 UserPromptSubmit "$T/repo-wt-b" "" "fix the readme"
	hookj 1 "{\"hook_event_name\":\"Notification\",\"cwd\":\"$T/repo-wt-b\",\"session_id\":\"sid-1\",\"notification_type\":\"permission_prompt\",\"message\":\"Claude needs your permission to use Bash\"}" >/dev/null
	hook 2 SessionStart "$T/repo"
	hook 2 UserPromptSubmit "$T/repo" "" "add a new module"
	hook 2 PostToolUse "$T/repo" "$T/repo-wt-b/newdir/sub/new.txt"
}
