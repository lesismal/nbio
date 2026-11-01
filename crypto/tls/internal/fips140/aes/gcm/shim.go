// Copyright 2026 The nbio Authors. BSD-style license (same as Go).
//
// Shim: stands in for a Go-internal package that code outside GOROOT cannot
// import. It keeps the package name and the exported API that crypto/tls uses,
// so the import rewrite in tools/tlssync/sync.sh is the only change needed in
// Go's files. See crypto/tls/README.md.

// Package gcm replaces crypto/internal/fips140/aes/gcm. It builds AES-GCM with
// the public crypto/cipher and adds the TLS nonce checks of Go's
// GCMWithCounterNonce / GCMWithXORCounterNonce. Go applies those checks only
// in FIPS mode; here they are always on: a repeated or decreasing record
// sequence number panics instead of reusing a nonce.
package gcm

import (
	"crypto/cipher"
	"encoding/binary"
	"math"
)

const nonceSize = 12

// NewGCMForTLS12 returns an AEAD for TLS 1.2 (explicit nonce = 4-byte salt +
// 8-byte counter starting at zero).
func NewGCMForTLS12(b cipher.Block) (cipher.AEAD, error) {
	g, err := cipher.NewGCM(b)
	if err != nil {
		return nil, err
	}
	return &counterNonce{g: g, startReady: true}, nil
}

// NewGCMForTLS13 returns an AEAD for TLS 1.3 (nonce = IV XOR counter).
func NewGCMForTLS13(b cipher.Block) (cipher.AEAD, error) {
	g, err := cipher.NewGCM(b)
	if err != nil {
		return nil, err
	}
	return &xorCounterNonce{g: g}, nil
}

type counterNonce struct {
	g           cipher.AEAD
	prefixReady bool
	prefix      uint32
	startReady  bool
	start       uint64
	next        uint64
}

func (g *counterNonce) NonceSize() int { return nonceSize }
func (g *counterNonce) Overhead() int  { return g.g.Overhead() }

func (g *counterNonce) Seal(dst, nonce, plaintext, data []byte) []byte {
	if len(nonce) != nonceSize {
		panic("crypto/cipher: incorrect nonce length given to GCM")
	}
	if !g.prefixReady {
		g.prefixReady = true
		g.prefix = binary.BigEndian.Uint32(nonce[:4])
	}
	if g.prefix != binary.BigEndian.Uint32(nonce[:4]) {
		panic("crypto/cipher: GCM nonce prefix changed")
	}
	counter := binary.BigEndian.Uint64(nonce[len(nonce)-8:])
	if !g.startReady {
		g.startReady = true
		g.start = counter
	}
	counter -= g.start
	if counter == math.MaxUint64 {
		panic("crypto/cipher: counter exhausted")
	}
	if counter < g.next {
		panic("crypto/cipher: counter decreased or remained the same")
	}
	g.next = counter + 1
	return g.g.Seal(dst, nonce, plaintext, data)
}

func (g *counterNonce) Open(dst, nonce, ciphertext, data []byte) ([]byte, error) {
	return g.g.Open(dst, nonce, ciphertext, data)
}

type xorCounterNonce struct {
	g      cipher.AEAD
	ready  bool
	prefix uint32
	mask   uint64
	next   uint64
}

func (g *xorCounterNonce) NonceSize() int { return nonceSize }
func (g *xorCounterNonce) Overhead() int  { return g.g.Overhead() }

func (g *xorCounterNonce) Seal(dst, nonce, plaintext, data []byte) []byte {
	if len(nonce) != nonceSize {
		panic("crypto/cipher: incorrect nonce length given to GCM")
	}
	counter := binary.BigEndian.Uint64(nonce[len(nonce)-8:])
	if !g.ready {
		g.ready = true
		g.mask = counter
		g.prefix = binary.BigEndian.Uint32(nonce[:4])
	}
	if g.prefix != binary.BigEndian.Uint32(nonce[:4]) {
		panic("crypto/cipher: GCM nonce prefix changed")
	}
	counter ^= g.mask
	if counter == math.MaxUint64 {
		panic("crypto/cipher: counter exhausted")
	}
	if counter < g.next {
		panic("crypto/cipher: counter decreased or remained the same")
	}
	g.next = counter + 1
	return g.g.Seal(dst, nonce, plaintext, data)
}

func (g *xorCounterNonce) Open(dst, nonce, ciphertext, data []byte) ([]byte, error) {
	return g.g.Open(dst, nonce, ciphertext, data)
}
