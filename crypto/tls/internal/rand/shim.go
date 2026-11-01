// Copyright 2026 The nbio Authors. BSD-style license (same as Go).
//
// Shim: stands in for a Go-internal package that code outside GOROOT cannot
// import. It keeps the package name and the exported API that crypto/tls uses,
// so the import rewrite in tools/tlssync/sync.sh is the only change needed in
// Go's files. See crypto/tls/README.md.

// Package rand replaces crypto/internal/rand for tests only. Std code outside
// GOROOT cannot redirect crypto/rand, so SetTestingReader is a no-op; it is
// only used by a benchmark, which then runs with real randomness.
package rand

import "io"

func SetTestingReader(io.Reader) {}
