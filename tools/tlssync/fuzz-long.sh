#!/usr/bin/env bash
# fuzz-long.sh <duration> — run both non-blocking fuzzers in parallel for a long time
# (e.g. 2h). Output: fuzz-long-<target>.txt; crashers land in
# crypto/tls/testdata/fuzz/<target>/ and must be committed with the fix.
set -uo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$ROOT"
T=${1:-2h}
for f in FuzzNonBlockRecordStream FuzzNonBlockGarbage; do
	go test -run '^$' -fuzz "^$f\$" -fuzztime "$T" -timeout 0 -parallel 3 ./crypto/tls/ >"fuzz-long-$f.txt" 2>&1 &
done
wait
tail -n 4 fuzz-long-*.txt
