// Copyright 2026 The nbio Authors. BSD-style license (same as Go).
//
// Shim for tests: stands in for a Go-internal test helper package that code
// outside GOROOT cannot import. Same package name and API as Go's.

// Package cryptotest is a reduced copy of Go's crypto/internal/cryptotest.
package cryptotest

import (
	"crypto/fips140"
	"strconv"
	"strings"
	"testing"
)

func MustMinimumFIPS140ModuleVersion(tb testing.TB, min string) {
	tb.Helper()
	if fips140.Version() == "latest" {
		return
	}
	if minor(tb, fips140.Version()) < minor(tb, min) {
		tb.Skipf("test requires FIPS 140-3 module %s or later", min)
	}
}

func minor(tb testing.TB, version string) int {
	v, ok := strings.CutPrefix(version, "v1.")
	if !ok {
		tb.Fatalf("unexpected FIPS 140 version format: %q", version)
	}
	v, _, _ = strings.Cut(v, ".")
	i, err := strconv.Atoi(v)
	if err != nil {
		tb.Fatalf("unexpected FIPS 140 version format %q: %v", version, err)
	}
	return i
}

// RerunWithFIPS140Enforced skips: GODEBUG=fips140=only is not supported by
// this copy (the public crypto/cipher GCM refuses TLS counter nonces in that
// mode, and only the Go module itself can be FIPS 140-3 validated anyway).
func RerunWithFIPS140Enforced(t *testing.T) {
	t.Helper()
	t.Skip("skipping: fips140=only mode is not supported outside GOROOT")
}

// FetchModule skips: the BoGo shim needs BoringSSL's test runner from the
// network. See crypto/tls/README.md for running BoGo by hand.
func FetchModule(t *testing.T, module, version string) string {
	t.Skip("skipping: fetching modules is not supported here")
	return ""
}
