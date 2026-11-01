// Copyright 2026 The nbio Authors. BSD-style license (same as Go).
//
// Shim: stands in for a Go-internal package that code outside GOROOT cannot
// import. It keeps the package name and the exported API that crypto/tls uses,
// so the import rewrite in tools/tlssync/sync.sh is the only change needed in
// Go's files. See crypto/tls/README.md.

// Package godebug replaces internal/godebug. A Setting reads its value from
// the GODEBUG environment variable (comma-separated name=value pairs), the
// same variable the runtime reads. Defaults from go.mod "godebug" lines are
// not applied: an unset setting means the current Go default.
package godebug

import (
	"os"
	"strings"
	"sync"
)

type Setting struct {
	name string
}

// New returns a setting. A leading "#" (Go's marker for settings that are
// not counted in runtime/metrics) is dropped.
func New(name string) *Setting {
	return &Setting{name: strings.TrimPrefix(name, "#")}
}

func (s *Setting) Name() string { return s.name }

func (s *Setting) String() string { return s.name + "=" + s.Value() }

// IncNonDefault is a no-op (Go uses it for runtime/metrics counters).
func (s *Setting) IncNonDefault() {}

var (
	mu     sync.Mutex
	raw    string
	parsed map[string]string
)

// Value returns the setting's value from GODEBUG, or "" if unset.
func (s *Setting) Value() string {
	env := os.Getenv("GODEBUG")
	mu.Lock()
	defer mu.Unlock()
	if parsed == nil || env != raw {
		raw = env
		parsed = map[string]string{}
		for _, kv := range strings.Split(env, ",") {
			if k, v, ok := strings.Cut(kv, "="); ok {
				parsed[k] = v // last one wins, as in the runtime
			}
		}
	}
	return parsed[s.name]
}
