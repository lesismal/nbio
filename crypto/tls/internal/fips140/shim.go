// Copyright 2026 The nbio Authors. BSD-style license (same as Go).
//
// Shim: stands in for a Go-internal package that code outside GOROOT cannot
// import. It keeps the package name and the exported API that crypto/tls uses,
// so the import rewrite in tools/tlssync/sync.sh is the only change needed in
// Go's files. See crypto/tls/README.md.

// Package fips140 replaces crypto/internal/fips140. Only the service
// indicator hooks are used by the copied code; they are no-ops here.
package fips140

import "crypto/fips140"

// Enabled reports whether FIPS 140-3 mode is on (GODEBUG=fips140=on/only).
var Enabled = fips140.Enabled()

func RecordApproved()    {}
func RecordNonApproved() {}
