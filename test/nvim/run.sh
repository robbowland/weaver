#!/bin/bash
# Headless specs for weaver's Neovim side, each in its own nvim, with a
# hermetic config (test/nvim/init.lua) and scratch state.
#
#   test/nvim/run.sh [spec...]     default: every test/nvim/*_spec.lua
set -u

HERE=$(cd "$(dirname "$0")" && pwd -P)
SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/weaver-nvim-test.XXXXXX") || exit 1
export XDG_STATE_HOME="$SCRATCH/state" XDG_CONFIG_HOME="$SCRATCH/config" XDG_CACHE_HOME="$SCRATCH/cache"
mkdir -p "$XDG_STATE_HOME" "$XDG_CONFIG_HOME" "$XDG_CACHE_HOME"
unset VIMINIT WEAVER_PANE WEAVER_STATE WEAVER_BIN WEAVER_BASE

[ $# -gt 0 ] || set -- "$HERE"/*_spec.lua
failed=0
for spec in "$@"; do
	echo "=== nvim/${spec##*/}"
	perl -e 'alarm 180; exec @ARGV' nvim --headless -i NONE -n -u "$HERE/init.lua" \
		-c "luafile $spec" </dev/null 2>"$SCRATCH/${spec##*/}.err"
	rc=$?
	if [ $rc != 0 ]; then
		failed=$((failed + 1))
		[ -s "$SCRATCH/${spec##*/}.err" ] && sed 's/^/  stderr: /' "$SCRATCH/${spec##*/}.err" | head -20
	fi
done
[ $failed = 0 ] || {
	echo "scratch dir: $SCRATCH"
	echo "$failed nvim spec file(s) failed"
	exit 1
}
rm -rf "$SCRATCH"
echo "all nvim specs passed"
