#!/bin/bash
# weaver's tests: Go's (go vet, and go test with the contract between the Go
# half and docs/design.md), the suites in test/suites/ on a throwaway Rex
# server (test/hooks.sh), and
# weaver on Rex end to end (test/rex.sh), each with its own server and state
# (the last two skipped without rex).
#
#   test/run.sh
set -u
HERE=$(cd "$(dirname "$0")" && pwd -P)
failed=0
echo "=== go"
(cd "$HERE/.." && go vet ./... && go test ./...) || failed=$((failed + 1))
for t in hooks.sh rex.sh; do
	echo "=== $t"
	bash "$HERE/$t" || failed=$((failed + 1))
done
if [ $failed -gt 0 ]; then
	echo "$failed of 3 failed"
	exit 1
fi
echo "all passed"
