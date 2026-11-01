#!/usr/bin/env bash
# diff-upstream.sh — show every difference between crypto/tls/ and Go's
# src/crypto/tls at the tag in crypto/tls/UPSTREAM (our own files and the
# shims under internal/ excluded). Expected: import lines and the two
# NewGCMForTLS1x call sites in cipher_suites.go, nothing else.
# Usage: tools/tlssync/diff-upstream.sh [<go-git-dir>] [--stat]
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
GODIR=${1:-$ROOT/.src/go}
TAG=$(cat "$ROOT/crypto/tls/UPSTREAM")
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
git -C "$GODIR" archive "$TAG" src/crypto/tls | tar -x -C "$TMP"
rm -rf "$TMP/src/crypto/tls/fipsonly" "$TMP/src/crypto/tls/defaults_boring.go" "$TMP/src/crypto/tls/internal"
diff -ru -x internal -x UPSTREAM -x nonblock.go -x nonblock_test.go -x 'arco_*' \
	"$TMP/src/crypto/tls" "$ROOT/crypto/tls" | sed "s#$TMP/src/crypto/tls#go/$TAG/src/crypto/tls#; s#$ROOT/##" || true
