#!/bin/bash
# The contracts between weaver's halves, which no single suite sees whole:
# the states an agent can be in and their cells, as weaver (state_cell, the
# G_* glyphs), Neovim (STATES, GLYPH in nvim/lua/weaver) and docs/design.md
# have them. A second, copied list drifting from the first fails here.
#   test/contract.sh
set -u
ROOT=$(cd "$(dirname "$0")/.." && pwd -P)
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

# weaver's glyphs and state_cell, from its own source.
eval "$(grep -m 1 "^G_NEED='" "$ROOT/weaver")"
eval "$(sed -n '/^state_cell() {$/,/^}$/p' "$ROOT/weaver")"
# Neovim's: the states it accepts, and each one's cell character.
lua_states=$(sed -n 's/^local STATES = { \(.*\) }$/\1/p' "$ROOT/nvim/lua/weaver/init.lua" |
	tr ',' '\n' | sed -E "s/^ *\\[?'?([a-z-]+)'?\\]? *= *true *$/\\1/" | sort | tr '\n' ' ')
lua_glyph() {
	sed -n "/^local GLYPH = {/,/^}/p" "$ROOT/nvim/lua/weaver/status.lua" |
		sed -nE "s/^ *\\[?'?$1'?\\]? *= *\\{ *'(.)'.*/\\1/p"
}
# The design doc's state file table.
doc_states=$(grep -m 1 '^| `state` | string |' "$ROOT/docs/design.md" | grep -oE '`[a-z-]+`' | tr -d '`' |
	grep -vx state | sort | tr '\n' ' ')

ok "states: Neovim's are the design doc's" "$lua_states" "$doc_states"
for s in $lua_states; do
	state_cell "$s"
	ok "state $s: weaver's cell is Neovim's" "${GCELL:1:1}" "$(lua_glyph "$s")"
done
state_cell starting
ok "a starting thread shows as working" "$GCELL" "$G_WORK"

echo "-- $pass passed, $fail failed"
exit $fail
