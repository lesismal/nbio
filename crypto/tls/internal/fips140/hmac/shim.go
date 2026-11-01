// Copyright 2026 The nbio Authors. BSD-style license (same as Go).
//
// Shim: stands in for a Go-internal package that code outside GOROOT cannot
// import. It keeps the package name and the exported API that crypto/tls uses,
// so the import rewrite in tools/tlssync/sync.sh is the only change needed in
// Go's files. See crypto/tls/README.md.

// Package hmac replaces crypto/internal/fips140/hmac with the public crypto/hmac.
package hmac

import (
	"crypto/hmac"
	"hash"
)

func New[H hash.Hash](h func() H, key []byte) hash.Hash {
	return hmac.New(func() hash.Hash { return h() }, key)
}
