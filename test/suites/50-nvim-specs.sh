#!/bin/bash
# The headless specs of weaver's Neovim side (test/nvim/), run with the
# end-to-end suites so one command runs everything. No zellij needed.
. "$(dirname "$0")/../lib.sh"
out=$("$(dirname "$0")/../nvim/run.sh" 2>&1)
rc=$?
printf '%s\n' "$out" | grep -v '^PASS ' | sed 's/^/  /'
p=$(printf '%s\n' "$out" | grep -c '^PASS ')
f=$(printf '%s\n' "$out" | grep -c '^FAIL ')
pass=$((pass + p))
fail=$((fail + f))
[ $rc = 0 ] || [ $f -gt 0 ] || fail=$((fail + 1))
done_suite
