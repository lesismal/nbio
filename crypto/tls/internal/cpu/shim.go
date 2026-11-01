// Copyright 2026 The nbio Authors. BSD-style license (same as Go).
//
// Shim: stands in for a Go-internal package that code outside GOROOT cannot
// import. It keeps the package name and the exported API that crypto/tls uses,
// so the import rewrite in tools/tlssync/sync.sh is the only change needed in
// Go's files. See crypto/tls/README.md.

// Package cpu replaces internal/cpu with golang.org/x/sys/cpu. Only the
// feature bits crypto/tls reads (AES-GCM hardware support) are exposed.
package cpu

import "golang.org/x/sys/cpu"

var (
	X86   = cpu.X86
	ARM64 = cpu.ARM64
	S390X = cpu.S390X
)
