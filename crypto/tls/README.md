# nbio/crypto/tls

This package is Go's `crypto/tls` (the tag in `UPSTREAM`), copied unmodified except for import lines, plus a
non-blocking adapter (`nonblock.go`) with the API nbio used from `github.com/lesismal/llib/std/crypto/tls`
(`NewConn`, `AppendAndRead`, `Append`, `ResetOrFreeBuffer`, `ResetRawInput`, `ResetConn`, `IsNonBlock`, `Conn`,
`Session`/`SetSession`). The Go files carry Go's BSD license (`LICENSE`).

## Layout

| Path | What | Origin |
|---|---|---|
| `*.go` except below | Go's `crypto/tls` | the Go tag in `UPSTREAM`, unmodified except import lines |
| `nonblock.go` | the non-blocking adapter | new |
| `nonblock_test.go`, `arco_*_test.go`, `testdata/fuzz/` | adapter tests, regression tests, fuzzers, canaries for the Go internals the adapter relies on | new |
| `internal/fips140/tls12`, `tls13`, `internal/fips140tls`, `internal/byteorder` | key schedule, PRF, helpers | Go, verbatim (import lines rewritten) |
| `internal/*/shim.go` | stand-ins for Go-internal packages that code outside GOROOT cannot import | new |
| `../../tools/tlssync/` | `sync.sh`, `check.sh`, `diff-upstream.sh`, `vulncheck.sh`, `latest.sh`, `fuzz-long.sh` | new (porting tools) |

## How the adapter works

1. **Would-block reads.** The transport of a non-blocking `Conn` is wrapped. The poller hands input to
   `AppendAndRead`; the wrapper serves it to Go's record layer and, when it runs out, returns a `net.Error`
   with `Temporary() == true`. Go's `readRecordOrCCS` does not store temporary errors, so a partial record
   stays in Go's own `rawInput` and is completed by the next input. No change to Go's code
   (`TestAssumptionTemporaryErrorsNotSticky` pins this).
2. **Handshake on a coroutine.** A handshake cannot be resumed that way (Go stores handshake errors), so it runs
   on its own goroutine used as a coroutine: the poller passes it the new bytes and waits until it needs more
   input or finishes. Only one of the two runs at a time. The wait is bounded by `NonBlockingHandshakeTimeout`
   (10 s for the whole handshake) and ends when the connection closes. One goroutine exists per connection
   *during* its handshake (≈1 RTT), none afterwards. A panic in a config callback fails that connection only.
3. **Go's locks untouched.** After the handshake, reads run on the poller and writes on any goroutine, through
   Go's own `c.in` / `c.out` locked paths (KeyUpdate replies, alerts and close_notify included).
4. **Idle buffers released.** `ResetOrFreeBuffer` (called by nbio after each read event) drops `rawInput`,
   `hand` and the `input` reference when they hold no unread bytes, so an idle connection keeps no input buffer.

Shims (`internal/*/shim.go`): `fips140/aes/gcm` builds AES-GCM with `crypto/cipher` and always applies Go's TLS
nonce checks (Go applies them only in FIPS mode, so this is stricter); `hkdf`, `hmac` wrap the public packages;
`fips140`, `sha256`, `sha512` only feed FIPS accounting; `godebug` reads `GODEBUG`; `rand`, `cpu`, `boring`,
`testenv`, `cryptotest` are small stand-ins.

## Limits

- Build with a toolchain of the same Go minor version as `UPSTREAM` (`go.mod` pins it).
- FIPS 140-3 mode is not supported (`GODEBUG=fips140=only` makes AES-GCM connections fail).
- go.mod `godebug` lines and `//go:debug` directives are not applied to this copy; the `GODEBUG` environment
  variable is.
- The poller waits for config callbacks (`GetCertificate`, `GetConfigForClient`, ...), as it did with llib:
  keep them fast (cache-only).
- Calling `Handshake`, `Read` or `Write` on a non-blocking conn before the first `AppendAndRead` fails the
  handshake instead of blocking. Non-blocking clients are not supported.

## Porting a Go release

Each Go point release that touches `crypto/tls`:

1. `tools/tlssync/sync.sh go1.N.M` (needs a clone of `https://go.googlesource.com/go` in `.src/go`; it re-copies
   the files and keeps the adapter, tests and shims).
2. Set the `go` line in `go.mod` to the same version, `go mod tidy`.
3. Read Go's diff for `src/crypto/tls` and `src/crypto/internal/fips140/tls12`, `tls13`. Re-read `nonblock.go`
   if it touches the read path (`readRecordOrCCS`, `readFromUntil`, `Read`, `handlePostHandshakeMessage`),
   error stickiness, the locks (`c.in`, `c.out`, `handshakeMutex`) or `rawInput` / `hand` / `input`. Also check,
   by reading: `handshakeContext` still holds `c.in` for the whole handshake, `HandshakeContext` starts no extra
   goroutine, and the server sets `isHandshakeComplete` only at the end of its handshake. A new Go-internal
   import needs a shim and a line in `sync.sh`.
4. `tools/tlssync/check.sh` must print "all checks passed": diff against Go (only import lines may differ), build
   and vet, Go's test suite plus the adapter's tests with `-race`, nbio's tests, fuzzing, and govulncheck on both
   the module and the copy (`vulncheck.sh`; plain govulncheck cannot see a copied package).

`latest.sh` and `vulncheck.sh` fail when a newer Go release or a newer `crypto/tls` fix exists; run them on a
schedule. Expected effort: 1–2 hours per point release, 1–3 days per minor release.
