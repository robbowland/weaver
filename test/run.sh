#!/bin/bash
# weaver's tests: the contracts between its halves, its Neovim side (headless),
# its hook engine (test/hooks.sh: the suites in test/suites/ that test it, on
# tmux) and weaver on tmux end to end, each with its own throwaway server and
# state. zellij is benched: all of test/suites/ on zellij is test/zellij.sh.
#
#   test/run.sh
set -u
HERE=$(cd "$(dirname "$0")" && pwd -P)
failed=0
for t in contract.sh nvim/run.sh hooks.sh tmux.sh; do
	echo "=== $t"
	bash "$HERE/$t" || failed=$((failed + 1))
done
if [ $failed -gt 0 ]; then
	echo "$failed of 4 failed"
	exit 1
fi
echo "all passed"
