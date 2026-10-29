#!/bin/bash
# weaver's tests: Go's (go vet, and go test with the contracts between the Go
# half, the Neovim plugin and docs/design.md), the Neovim side's (headless),
# the suites in test/suites/ on a throwaway tmux server (test/hooks.sh),
# weaver on tmux end to end (test/tmux.sh), and on Rex (test/rex.sh, skipped
# without rex), each with its own server and state. test/soak/ holds the
# long runs, by hand.
#
#   test/run.sh
set -u
HERE=$(cd "$(dirname "$0")" && pwd -P)
failed=0
echo "=== go"
(cd "$HERE/.." && go vet ./... && go test ./...) || failed=$((failed + 1))
for t in nvim/run.sh hooks.sh tmux.sh rex.sh; do
	echo "=== $t"
	bash "$HERE/$t" || failed=$((failed + 1))
done
if [ $failed -gt 0 ]; then
	echo "$failed of 5 failed"
	exit 1
fi
echo "all passed"
