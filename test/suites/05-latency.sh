#!/bin/bash
# Hook latency: what each event costs the agent. A fake agent (pane 90) is bound
# to a headless nvim and to a real pane, so following and renaming cost what
# they cost in real use. Independent of the other suites.
. "$(dirname "$0")/../lib.sh"

P=90
N=${WEAVER_BENCH_N:-15}
mkdir -p "/tmp/weaver-$(id -u)" "$SD" && chmod 700 "/tmp/weaver-$(id -u)"
NVP=$(new_pane bash --noprofile --norc)
rm -f "$(sock $P)"
nvim --clean --headless --listen "$(sock $P)" \
	-c 'lua function WeaverEvent() return 0 end' \
	</dev/null >/dev/null 2>&1 &
nvpid=$!
printf '%s\n%s\n' "$NVP" "$nvpid" >"$SD/${SK}_$P.nvim"
for _ in 1 2 3 4 5 6 7 8 9 10; do [ -S "$(sock $P)" ] && break; sleep 0.2; done

J=$AT/lat
mkdir -p "$J"
base='"session_id":"sid-lat","transcript_path":"/dev/null","permission_mode":"default"'
printf '{"hook_event_name":"SessionStart","source":"startup","cwd":"%s",%s}' "$T/repo" "$base" >"$J/start"
printf '{"hook_event_name":"UserPromptSubmit","prompt":"measure me","cwd":"%s",%s}' "$T/repo" "$base" >"$J/prompt"
printf '{"hook_event_name":"Stop","cwd":"%s","last_assistant_message":"ok",%s}' "$T/repo" "$base" >"$J/stop"
printf '{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"ls"},"cwd":"%s",%s}' "$T/repo" "$base" >"$J/perm"
printf '{"hook_event_name":"Notification","notification_type":"idle_prompt","message":"waiting","cwd":"%s",%s}' "$T/repo" "$base" >"$J/note"
for w in a b; do
	printf '{"hook_event_name":"PostToolUse","tool_name":"Edit","tool_input":{"file_path":"%s"},"tool_response":{},"cwd":"%s",%s}' \
		"$T/repo-wt-$w/src/app.txt" "$T/repo" "$base" >"$J/edit-$w"
done
printf '{"hook_event_name":"PostToolUse","tool_name":"Read","tool_input":{"file_path":"%s"},"tool_response":{},"cwd":"%s",%s}' \
	"$T/repo/README.md" "$T/repo" "$base" >"$J/read"

# run FILE [ARGS]: one hook call, in ms: until the hook exits, which is what
# the agent waits for (its background work goes on without it).
run() {
	perl -MTime::HiRes=time -e '
		open(STDIN, "<", shift) or die; open(my $out, ">&", \*STDOUT); open(STDOUT, ">", "/dev/null");
		my $t = time; system(@ARGV); printf $out "%d", (time - $t) * 1000' "$1" wvr hook ${2:+"$2"}
}
# median NAME CMD...: run CMD N times, print and keep the median. Calls are
# spaced as agent events are, so one call's background work does not slow
# the next.
median() {
	local name=$1 i t
	shift
	: >"$J/times"
	for ((i = 0; i < N; i++)); do
		t=$("$@")
		echo "$t" >>"$J/times"
		sleep 0.25
	done
	MED=$(sort -n "$J/times" | sed -n "$(((N + 1) / 2))p")
	# The lower quartile, for the budgets: the path's own cost, which a
	# loaded machine (another test run, an agent) inflates less.
	LOW=$(sort -n "$J/times" | sed -n "$(((N + 3) / 4))p")
	printf '  %-36s median %3s ms   max %3s ms\n' "$name" "$MED" "$(sort -n "$J/times" | tail -n 1)"
}
cycle() {
	run "$J/prompt" >/dev/null
	run "$J/$1"
}
# An edit, in worktree a and b by turns, so the root changes every time.
edits() {
	local n=0
	{ read -r n <"$J/n"; } 2>/dev/null
	echo $((n + 1)) >"$J/n"
	if [ $((n % 2)) = 0 ]; then run "$J/edit-a" "$@"; else run "$J/edit-b" "$@"; fi
}

run "$J/start" >/dev/null
run "$J/prompt" >/dev/null
echo "  (hook latency, $N runs each)"
median "UserPromptSubmit" run "$J/prompt"
P_PROMPT=$MED L_PROMPT=$LOW
median "Stop" cycle stop
median "PermissionRequest" cycle perm
median "Notification idle_prompt" run "$J/note"
run "$J/prompt" >/dev/null
median "Edit via the sync hook (as before)" edits
median "Edit via the async hook (as now)" edits --async
P_EDIT=$MED L_EDIT=$LOW
median "async PostToolUse Read, working" run "$J/read" --async
P_ASYNC=$MED L_ASYNC=$LOW
# The floor: wvr starting and exiting, doing nothing (most of every call
# above: a Go binary's start, which a loaded machine stretches).
base() {
	perl -MTime::HiRes=time -e 'open(STDOUT, ">", "/dev/null"); my $t = time; system(@ARGV); printf STDERR "%d", (time - $t) * 1000' wvr version 2>&1
}
median "wvr starting, doing nothing (floor)" base
L_BASE=$LOW
sleep 1

# Generous budgets, so a loaded machine passes; what they catch is a
# synchronous nvim or Rex call (10-25 ms each, 2 s when stuck) creeping
# back in, or the async hook parsing events it has no use for.
ok "latency: a prompt costs the agent under 25 ms" "$([ "$L_PROMPT" -lt 25 ] && echo y)" y
ok "latency: an edit in the async hook waits on neither nvim nor Rex (under 40 ms)" \
	"$([ "$L_EDIT" -lt 40 ] && echo y)" y
ok "latency: the async hook with nothing to do costs next to nothing over wvr starting (under 3 ms more)" \
	"$([ "$L_ASYNC" -lt $((L_BASE + 3)) ] && echo y)" y ||
	echo "  (async nothing ${L_ASYNC} ms, floor ${L_BASE} ms, lower quartiles)"

kill "$nvpid" 2>/dev/null
close_pane "$NVP" >/dev/null 2>&1
rm -f "$SD/${SK}_$P".* "$(sock $P)"
done_suite
