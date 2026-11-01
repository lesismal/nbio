// Copyright 2026 The nbio Authors. BSD-style license (same as Go).
//
// Shim: stands in for a Go-internal package that code outside GOROOT cannot
// import. It keeps the package name and the exported API that crypto/tls uses,
// so the import rewrite in tools/tlssync/sync.sh is the only change needed in
// Go's files. See crypto/tls/README.md.

// Package hkdf replaces crypto/internal/fips140/hkdf with the public crypto/hkdf.
package hkdf

import (
	"crypto/hkdf"
	"hash"
)

func Extract[H hash.Hash](h func() H, secret, salt []byte) []byte {
	prk, err := hkdf.Extract(h, secret, salt)
	if err != nil {
		panic("hkdf: " + err.Error())
	}
	return prk
}

func Expand[H hash.Hash](h func() H, pseudorandomKey []byte, info string, keyLen int) []byte {
	out, err := hkdf.Expand(h, pseudorandomKey, info, keyLen)
	if err != nil {
		panic("hkdf: " + err.Error())
	}
	return out
}
