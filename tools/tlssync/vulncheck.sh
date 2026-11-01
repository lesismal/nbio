#!/usr/bin/env bash
# vulncheck.sh — security check for this repo, including the crypto/tls copy.
#
# govulncheck only knows Go's standard-library crypto/tls; it cannot see the
# copy in crypto/tls/. So this script runs it twice:
#   1. on this module, as usual (the toolchain and golang.org/x/... deps);
#   2. on a tiny program that uses the standard crypto/tls server API, built
#      with the Go version recorded in crypto/tls/UPSTREAM. Every crypto/tls
#      finding there is a Go security fix that our copy does NOT have yet:
#      run tools/tlssync/sync.sh with a newer tag.
# Exit status is non-zero if either run finds anything.
set -uo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
GOVULNCHECK=${GOVULNCHECK:-$(go env GOPATH)/bin/govulncheck}
# UPSTREAM_OVERRIDE is only for testing this script.
UP=${UPSTREAM_OVERRIDE:-$(cat "$ROOT/crypto/tls/UPSTREAM")}
status=0

echo "== 1. govulncheck ./... (toolchain $(go env GOVERSION))"
(cd "$ROOT" && "$GOVULNCHECK" ./...) || status=1

echo
echo "== 2. crypto/tls copy, imported from $UP"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
cat >"$TMP/go.mod" <<EOM
module tlscopycheck

go ${UP#go}
EOM
# Exercise the server paths our nbio servers use (handshake, read, write,
# close, tickets, client certificates off) so findings are symbol-level.
cat >"$TMP/main.go" <<'EOM'
package main

import (
	"crypto/tls"
	"net"
)

func main() {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	c, _ := ln.Accept()
	cfg := &tls.Config{GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return nil, nil }}
	s := tls.Server(c, cfg)
	_ = s.Handshake()
	b := make([]byte, 1)
	_, _ = s.Read(b)
	_, _ = s.Write(b)
	_ = s.ConnectionState()
	_ = s.Close()
	cl := tls.Client(c, cfg)
	_ = cl.Handshake()
}
EOM
out=$(cd "$TMP" && GOTOOLCHAIN="$UP" GOFLAGS=-mod=mod "$GOVULNCHECK" -show verbose ./... 2>&1)
echo "$out" | sed -n '/=== Symbol Results ===/,$p'
if echo "$out" | grep -q 'crypto/tls'; then
	echo
	echo "crypto/tls has security fixes after $UP: sync to the latest Go release (crypto/tls/README.md)."
	status=1
else
	echo "no known crypto/tls vulnerability in $UP."
fi
exit $status
