#!/bin/bash
# Soak test for the zjstatus counts widget (weaver status --zjstatus under
# zjstatus's command widget): five tabs, hook traffic changing the counts all
# the time, for WEAVER_SOAK_MINUTES (default 8). Checks every few seconds that
# zellij still answers and zjstatus has not crashed, and at the end that the
# zellij log has no plugin panic (zjstatus issue #247). Run it by hand:
#
#   WEAVER_TEST_ZJSTATUS=1 test/run.sh test/soak/zjstatus.sh
#
# zjstatus asks zellij for permissions in a session that has not granted them:
# the test answers y in each tab. zellij remembers that in its permissions
# cache; the test puts that file back as it was when it is done.
. "$(dirname "$0")/../lib.sh"
fresh

PERM="$HOME/Library/Caches/org.Zellij-Contributors.Zellij/permissions.kdl"
[ -d "${PERM%/*}" ] || PERM="${XDG_CACHE_HOME:-$HOME/.cache}/zellij/permissions.kdl"
if [ -f "$PERM" ]; then cp -p "$PERM" "$AT/permissions.kdl.before"; fi
restore_permissions() {
	if [ -f "$AT/permissions.kdl.before" ]; then cp -p "$AT/permissions.kdl.before" "$PERM"; else rm -f "$PERM"; fi
}
trap restore_permissions EXIT

MIN=${WEAVER_SOAK_MINUTES:-8}
LOG=${TMPDIR:-/tmp}/zellij-$(id -u)/zellij-log/zellij.log
[ -f "$LOG" ] || LOG=$(ls -t /var/folders/*/*/T/zellij-"$(id -u)"/zellij-log/zellij.log 2>/dev/null | head -n 1)
panics() {
	local n
	n=$(grep -a -c -E 'panicked|unreachable. instruction|out of bounds memory|ERROR IN PLUGIN' "$LOG" 2>/dev/null)
	echo "${n:-0}"
}
# The last 3000 characters zellij drew, as text: escapes become spaces (zellij
# draws runs of spaces as cursor moves).
plain() {
	python3 -I -c '
import re, sys
s = open(sys.argv[1], "rb").read()[-60000:].decode("utf-8", "replace")
s = re.sub(r"\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1bP[^\x1b]*\x1b\\", "", s)
s = re.sub(r"\x1b\[[0-9;?]*[a-zA-Z]", " ", s)
print(re.sub(r" +", " ", s)[-3000:])' "$AT/screen.log"
}

for t in three four five; do Z new-tab --name "$t" >/dev/null; done
sleep 2
# Grant zjstatus its permissions where it asks.
for tab in $(Z list-tabs -a -j | jq -r '.[].tab_id'); do
	Z go-to-tab-by-id "$tab"
	sleep 0.5
	pl=$(Z list-panes -a -j | jq -r ".[] | select(.is_plugin and .tab_id == $tab and (.title | test(\"zjstatus\"))) | .id" | head -n 1)
	[ -n "$pl" ] || continue
	Z focus-pane-id "plugin_$pl"
	sleep 0.3
	key 79 # y
	sleep 0.5
done
Z go-to-tab-by-id 0
sleep 3
ok "zjstatus renders the tabs (permissions granted)" "$(plain | grep -a -c 'one\*')" "1"

before=$(panics)
end=$(($(date +%s) + MIN * 60))
i=0 slow=0 crash=0 shown=0 checks=0
while [ "$(date +%s)" -lt $end ]; do
	i=$((i + 1))
	p=$((i % 3))
	case $((i % 4)) in
	0) hook $p UserPromptSubmit "$T/repo" "" "soak $i" >/dev/null ;;
	1) hook $p PermissionRequest "$T/repo" >/dev/null ;;
	2) hook $p PostToolUse "$T/repo" "$T/repo/src/app.txt" ;;
	3) hook $p Stop "$T/repo" >/dev/null ;;
	esac
	[ $((i % 4)) = 0 ] && Z go-to-tab-by-id $(((i / 4) % 5)) >/dev/null 2>&1
	if [ $((i % 10)) = 0 ]; then
		checks=$((checks + 1))
		t=$(ms perl -e 'alarm 5; open(STDOUT, ">", "/dev/null"); exec @ARGV' zellij action list-panes -a -j)
		[ "$t" -gt 3000 ] && slow=$((slow + 1))
		screen=$(plain)
		case $screen in *"ERROR IN PLUGIN"*) crash=$((crash + 1)) ;; esac
		case $screen in *"! "[0-9]* | *"✓ "[0-9]* | *"● "[0-9]*) shown=$((shown + 1)) ;; esac
	fi
	sleep 0.5
done
echo "  $i events over $MIN min; $checks checks: $slow slow zellij answers, $crash crash screens, counts shown $shown times"
ok "zellij kept answering (no answer over 3s)" "$slow" "0"
ok "zjstatus never showed ERROR IN PLUGIN" "$crash" "0"
ok "the counts were on screen" "$([ "$shown" -gt $((checks / 2)) ] && echo y)" y
ok "no plugin panic in the zellij log" "$(($(panics) - before))" "0"
settle
ok "the widget shows what weaver status says" \
	"$(weaver status --zjstatus | sed 's/#\[[^]]*\]//g' | tr -s ' ' | sed 's/ $//')" \
	"$(weaver status | tr -s ' ')"
done_suite
