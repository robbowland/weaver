#!/bin/bash
# weaver's tests: Go's (go vet, and go test with the contract between the Go
# half and docs/design.md), the hook engine's conformance cases
# (test/conformance: events in, state out, by any build), the suites in
# test/suites/ on a throwaway Rex
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
# The hook engine by its contract alone (no Rex needed): any build passes it.
echo "=== conformance"
python3 "$HERE/conformance/run.py" || failed=$((failed + 1))
for t in hooks.sh rex.sh; do
	echo "=== $t"
	bash "$HERE/$t" || failed=$((failed + 1))
done
if [ $failed -gt 0 ]; then
	echo "$failed of 4 failed"
	exit 1
fi
echo "all passed"
