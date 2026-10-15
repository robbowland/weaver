#!/bin/bash
# Soak test for nvims weaver starts going away all at once (a crash, quit,
# or the server killed): six nvims with weaver.lua, ShaDa on and a fresh
# state directory, in one throwaway tmux server, killed together, for
# WEAVER_SOAK_ROUNDS rounds (default 8). Without weaver's guard about one
# in ten hung for good in wait_return: their ShaDa writes race (E886, E136)
# and an error with no UI left waits for a key. Run it by hand:
#
#   test/soak/nvim-exit.sh
set -u
HERE=$(cd "$(dirname "$0")" && pwd -P)
LUA=$HERE/../../nvim/weaver.lua
AT=$(mktemp -d /tmp/weaver-exit.XXXXXX)
T="tmux -L weaver-exit-$$ -f /dev/null"
cleanup() {
	$T kill-server 2>/dev/null
	pkill -9 -f "listen $AT/" 2>/dev/null
	rm -rf "$AT"
}
trap cleanup EXIT
rounds=${WEAVER_SOAK_ROUNDS:-8} hung=0
for ((r = 1; r <= rounds; r++)); do
	rm -rf "$AT/state"
	for i in 0 1 2 3 4 5; do
		cmd="nvim -u NONE --listen $AT/s.$r.$i -c 'luafile $LUA'"
		if [ $i = 0 ]; then
			$T new-session -d -x 200 -y 60 -e XDG_STATE_HOME="$AT/state" "$cmd"
		else
			$T new-window -e XDG_STATE_HOME="$AT/state" "$cmd"
		fi
	done
	sleep 2
	$T kill-server
	sleep 3
	n=$(pgrep -f "listen $AT/s.$r." | wc -l | tr -d ' ')
	hung=$((hung + n))
	pkill -9 -f "listen $AT/s.$r." 2>/dev/null
done
echo "-- $hung of $((rounds * 6)) nvims hung"
[ "$hung" = 0 ]
