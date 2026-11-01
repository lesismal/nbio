// Copyright 2026 The nbio Authors. BSD-style license (same as Go).
//
// Shim: stands in for a Go-internal package that code outside GOROOT cannot
// import. It keeps the package name and the exported API that crypto/tls uses,
// so the import rewrite in tools/tlssync/sync.sh is the only change needed in
// Go's files. See crypto/tls/README.md.

// Package boring replaces crypto/internal/boring. BoringCrypto is never
// enabled in this copy.
package boring

import (
	"crypto/cipher"
	"crypto/rand"
)

const Enabled = false

var RandReader = rand.Reader

func Unreachable()                                  {}
func UnreachableExceptTests()                       {}
func NewGCMTLS(cipher.Block) (cipher.AEAD, error)   { panic("boringcrypto: not available") }
func NewGCMTLS13(cipher.Block) (cipher.AEAD, error) { panic("boringcrypto: not available") }
