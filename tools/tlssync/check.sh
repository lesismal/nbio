#!/usr/bin/env bash
# check.sh — everything that must pass before a crypto/tls sync is committed.
# Usage: tools/tlssync/check.sh [fuzztime]   (default fuzztime 60s per fuzzer)
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$ROOT"
FUZZTIME=${1:-60s}

step() { printf '\n== %s\n' "$*"; }

step "diff against Go $(cat crypto/tls/UPSTREAM): only import lines and the 2 NewGCMForTLS1x lines expected"
tools/tlssync/diff-upstream.sh | grep '^[-+][^-+]' | grep -v -E '^[-+]\s+([a-z]+ )?"[^"]+"$' |
	grep -v -E 'gcm\.NewGCMForTLS1[23]\(aes(\.\(\*fipsaes\.Block\))?\)$' && {
	echo "unexpected change against upstream Go (see above): review it, then record it in crypto/tls/README.md"
	exit 1
} || echo "ok"

step "build and vet"
go build ./...
go vet ./crypto/tls/... ./extension/... ./nbhttp/...

step "Go's crypto/tls test suite + non-blocking + audit regression tests, with -race"
go test -count=1 -race ./crypto/tls/...

step "nbio tests"
go test -count=1 ./...

step "fuzz the non-blocking record layer ($FUZZTIME each)"
for f in FuzzNonBlockRecordStream FuzzNonBlockGarbage; do
	go test -run '^$' -fuzz "^$f\$" -fuzztime "$FUZZTIME" -timeout 0 ./crypto/tls/
done

step "govulncheck (module + crypto/tls copy)"
tools/tlssync/vulncheck.sh

echo
echo "all checks passed"
