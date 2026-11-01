// Copyright 2026 The nbio Authors. BSD-style license (same as Go).
//
// Shim: stands in for a Go-internal package that code outside GOROOT cannot
// import. It keeps the package name and the exported API that crypto/tls uses,
// so the import rewrite in tools/tlssync/sync.sh is the only change needed in
// Go's files. See crypto/tls/README.md.

// Package sha512 only provides the Digest type that tls12.MasterSecret switches
// on to feed the FIPS service indicator, which is a no-op here.
package sha512

type Digest struct{}

func (*Digest) Write(p []byte) (int, error) { panic("unused") }
func (*Digest) Sum(b []byte) []byte         { panic("unused") }
func (*Digest) Reset()                      { panic("unused") }
func (*Digest) Size() int                   { panic("unused") }
func (*Digest) BlockSize() int              { panic("unused") }
