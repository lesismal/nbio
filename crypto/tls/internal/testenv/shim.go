// Copyright 2026 The nbio Authors. BSD-style license (same as Go).
//
// Shim for tests: stands in for a Go-internal test helper package that code
// outside GOROOT cannot import. Same package name and API as Go's.

// Package testenv is a reduced copy of Go's internal/testenv.
package testenv

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func Builder() string { return os.Getenv("GO_BUILDER_NAME") }

func CPUIsSlow() bool {
	switch runtime.GOARCH {
	case "arm", "mips", "mipsle", "mips64", "mips64le", "wasm":
		return true
	}
	return false
}

func MustHaveExec(t testing.TB) {
	switch runtime.GOOS {
	case "js", "wasip1", "ios":
		t.Helper()
		t.Skipf("skipping test: cannot exec subprocess on %s", runtime.GOOS)
	}
}

func MustHaveExecPath(t testing.TB, path string) {
	MustHaveExec(t)
	if _, err := exec.LookPath(path); err != nil {
		t.Helper()
		t.Skipf("skipping test: %s: %s", path, err)
	}
}

func MustHaveExternalNetwork(t testing.TB) {
	if testing.Short() || os.Getenv("ARCO_TLS_EXTERNAL_NETWORK") == "" {
		t.Helper()
		t.Skipf("skipping test: no external network (set ARCO_TLS_EXTERNAL_NETWORK=1)")
	}
}

// MustHaveSource always skips: these tests inspect GOROOT sources.
func MustHaveSource(t testing.TB) {
	t.Helper()
	t.Skip("skipping test: needs the GOROOT source tree of the package under test")
}

func Executable(t testing.TB) string {
	MustHaveExec(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}

func GoToolPath(t testing.TB) string {
	MustHaveExec(t)
	p, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("skipping test: go tool not found: %v", err)
	}
	return p
}

func CleanCmdEnv(cmd *exec.Cmd) *exec.Cmd {
	if cmd.Env != nil {
		panic("environment already set")
	}
	for _, env := range cmd.Environ() {
		if strings.HasPrefix(env, "GODEBUG=") || strings.HasPrefix(env, "GOTRACEBACK=") {
			continue
		}
		cmd.Env = append(cmd.Env, env)
	}
	return cmd
}

func Command(t testing.TB, name string, args ...string) *exec.Cmd {
	t.Helper()
	MustHaveExec(t)
	return exec.CommandContext(context.Background(), name, args...)
}

func SetGODEBUG(t testing.TB, v string) {
	t.Helper()
	t.Setenv("GODEBUG", os.Getenv("GODEBUG")+","+v)
}
