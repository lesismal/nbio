// Copyright 2026 The nbio Authors. BSD-style license (same as Go).

// Adversarial review 3 of nonblock.go and its shims (an independent review).
// Each finding test failed before its fix and is kept as a regression test;
// the TestReview3Sound* tests are evidence for what the review found sound.
//
//	go test -race -count=1 -run TestReview3 ./crypto/tls/

package tls

import (
	"bytes"
	stdtls "crypto/tls"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// R3-1: NonBlockingHandshakeTimeout used to bound each wait for input, not
// the handshake: a client dripping one byte per (timeout - ε) kept the
// handshake goroutine and its state alive indefinitely. It now bounds the
// whole handshake.
func TestReview3HandshakeTimeoutIsTotal(t *testing.T) {
	const timeout = 100 * time.Millisecond
	hello := clientHelloBytes(t)
	a, b := net.Pipe()
	go io.Copy(io.Discard, b)
	srv := NewConn(a, nbServerConfig(VersionTLS13), false, true)
	setHandshakeTimeout(t, timeout, srv, b)
	out := make([]byte, 64)

	start := time.Now()
	for i := 0; time.Since(start) < 10*timeout; i++ {
		if i >= len(hello)-1 {
			t.Skip("ClientHello too short for this test")
		}
		if _, _, err := srv.AppendAndRead(hello[i:i+1], out); err != nil {
			if !errors.Is(err, errHandshakeTimeout) {
				t.Fatalf("byte %d after %v: %v", i, time.Since(start), err)
			}
			if d := time.Since(start); d > 3*timeout {
				t.Fatalf("timed out after %v, want about %v", d, timeout)
			}
			waitHandshakesInFlight(t, 0)
			return
		}
		time.Sleep(timeout / 2)
	}
	if !handshakeExited(srv) {
		t.Fatalf("handshake still in progress after %v (10x NonBlockingHandshakeTimeout=%v): "+
			"the timeout is per gap, a drip-feeding client is never cut off",
			time.Since(start).Round(time.Millisecond), timeout)
	}
}

// setHandshakeTimeout sets NonBlockingHandshakeTimeout for one test and
// restores it only after the handshake goroutine (which reads it) is gone.
func setHandshakeTimeout(t *testing.T, d time.Duration, srv *Conn, peer net.Conn) {
	old := NonBlockingHandshakeTimeout
	NonBlockingHandshakeTimeout = d
	t.Cleanup(func() {
		srv.Close()
		peer.Close()
		deadline := time.Now().Add(5 * time.Second)
		for !handshakeExited(srv) && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		NonBlockingHandshakeTimeout = old
	})
}

// R3-2: stepHandshake used to wait for the handshake goroutine with no
// timeout and without watching Close. A config callback that blocks (GetCertificate,
// GetConfigForClient, VerifyPeerCertificate, ... e.g. a certificate lookup
// keyed on the attacker-chosen SNI) blocks the poller goroutine that called
// AppendAndRead, past NonBlockingHandshakeTimeout and past Close of the
// connection, and with it every other connection on that poller.
func TestReview3BlockingCallbackStallsPoller(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	cfg := nbServerConfig(VersionTLS13)
	cert := cfg.Certificates[0]
	cfg.Certificates = nil
	cfg.GetCertificate = func(*ClientHelloInfo) (*Certificate, error) {
		<-release // e.g. a slow database or ACME lookup
		return &cert, nil
	}
	a, b := net.Pipe()
	go io.Copy(io.Discard, b)
	srv := NewConn(a, cfg, false, true)
	setHandshakeTimeout(t, 100*time.Millisecond, srv, b)

	returned := make(chan time.Duration, 1)
	start := time.Now()
	go func() {
		srv.AppendAndRead(clientHelloBytes(t), make([]byte, 64)) // the "poller"
		returned <- time.Since(start)
	}()
	time.Sleep(50 * time.Millisecond)
	srv.Close() // e.g. nbio's read deadline fired, or the server shuts down
	select {
	case d := <-returned:
		t.Logf("AppendAndRead returned after %v", d)
	case <-time.After(1 * time.Second):
		releaseOnce.Do(func() { close(release) })
		<-returned
		t.Fatalf("AppendAndRead (the poller) stayed blocked for %v after Close and %v after "+
			"NonBlockingHandshakeTimeout, until the callback returned",
			time.Since(start).Round(time.Millisecond), NonBlockingHandshakeTimeout)
	}
}

// R3-3 (not fixed; FIPS 140-only mode is unsupported, crypto/tls/README.md):
// shims under GODEBUG=fips140=only. The GCM shim builds AES-GCM with
// the public cipher.NewGCM, which refuses to work in FIPS 140-only mode, so
// aeadAESGCM/aeadAESGCMTLS13 panic: every AES-GCM connection fails (server:
// recovered panic on the handshake goroutine; blocking client: unrecovered
// panic in the caller). Go's own crypto/tls works in that mode.
func TestReview3FIPSOnlyGCM(t *testing.T) {
	if os.Getenv("ARCO_REVIEW3_CHILD") != "" {
		t.Skip("child")
	}
	if os.Getenv("ARCO_FIPS_ONLY_TEST") == "" {
		t.Skip("FIPS 140-only mode is unsupported (crypto/tls/README.md); set ARCO_FIPS_ONLY_TEST=1 to see the failure")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestReview3FIPSOnlyGCMChild$", "-test.v")
	cmd.Env = append(os.Environ(), "GODEBUG=fips140=only", "ARCO_REVIEW3_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("AES-GCM TLS AEAD unusable with GODEBUG=fips140=only: %v\n%s", err, out)
	}
}

func TestReview3FIPSOnlyGCMChild(t *testing.T) {
	if os.Getenv("ARCO_REVIEW3_CHILD") == "" {
		t.Skip("run by TestReview3FIPSOnlyGCM")
	}
	for name, f := range map[string]func(){
		"TLS 1.2": func() { aeadAESGCM(make([]byte, 16), make([]byte, noncePrefixLength)) },
		"TLS 1.3": func() { aeadAESGCMTLS13(make([]byte, 16), make([]byte, aeadNonceLength)) },
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s AES-GCM: panic: %v", name, r)
				}
			}()
			f()
		}()
	}
}

// R3-4: after the handshake failed, AppendAndRead used to append every input
// to f.pending (feed runs before the hsErr check) and never consumes it. A
// caller that keeps feeding a failed connection (any glue that does not close
// on the first error) buffers everything the peer sends. The nbhttp and
// extension/tls glue close on error, so this is not reachable through them.
func TestReview3FailedHandshakeKeepsBuffering(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	go io.Copy(io.Discard, b)
	srv := NewConn(a, testConfigServer.Clone(), false, true)
	defer srv.Close()
	out := make([]byte, 64)
	if _, _, err := srv.AppendAndRead([]byte("GET / HTTP/1.1\r\n\r\n"), out); err == nil {
		t.Fatal("garbage accepted")
	}
	chunk := bytes.Repeat([]byte{0x17}, 64<<10)
	for i := 0; i < 32; i++ {
		srv.AppendAndRead(chunk, out)
		srv.ResetOrFreeBuffer()
	}
	if n := len(srv.conn.(*nbConn).pending); n > 0 {
		t.Fatalf("failed connection buffers %d KiB of input it will never read", n>>10)
	}
}

// TestReview3SoundCloseRace (expected to PASS; kept as evidence for the
// "sound" list): Close and a server Write race a poller-driven handshake and
// data in tiny, aliased chunks. Must be clean under -race, never hang, and
// never leak the handshake goroutine.
func TestReview3SoundCloseRace(t *testing.T) {
	for i := 0; i < 200; i++ {
		cs, ss := net.Pipe()
		srv := NewConn(ss, nbServerConfig([]uint16{VersionTLS12, VersionTLS13}[i%2]), false, true)
		done := make(chan struct{})
		go func() {
			defer close(done)
			buf := make([]byte, 1+i%13)
			for {
				n, err := ss.Read(buf)
				if err != nil {
					return
				}
				in := buf[:n]
				for {
					_, m, err := srv.AppendAndRead(in, buf)
					in = nil
					if err != nil {
						ss.Close()
						return
					}
					if m == 0 {
						break
					}
				}
				srv.ResetOrFreeBuffer()
			}
		}()
		cli := Client(cs, testConfigClient.Clone())
		go func() {
			go io.Copy(io.Discard, cli)
			cli.Write(bytes.Repeat([]byte("x"), 3000))
		}()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			time.Sleep(time.Duration(rand.IntN(3000)) * time.Microsecond)
			srv.Close()
		}()
		go func() {
			defer wg.Done()
			time.Sleep(time.Duration(rand.IntN(3000)) * time.Microsecond)
			if _, err := srv.Write([]byte("push")); err != nil && !errors.Is(err, net.ErrClosed) &&
				!strings.Contains(err.Error(), "closed") && !errors.Is(err, errHandshakeNotDriven) &&
				!strings.Contains(err.Error(), "handshake") {
				t.Logf("write: %v", err)
			}
			srv.ReleaseBuffers()
		}()
		wg.Wait()
		cs.Close()
		<-done
	}
	waitHandshakesInFlight(t, 0)
}

// R3-6: NonBlockingHandshakeTimeout used to be read by every parked handshake
// goroutine (waitForInput) without synchronization, so changing it at
// runtime raced. It is now read once per handshake, on the poller.
func TestReview3TimeoutGlobalRace(t *testing.T) {
	old := NonBlockingHandshakeTimeout
	a, b := net.Pipe()
	go io.Copy(io.Discard, b)
	srv := NewConn(a, nbServerConfig(VersionTLS13), false, true)
	hello := clientHelloBytes(t)
	if _, _, err := srv.AppendAndRead(hello[:10], make([]byte, 64)); err != nil {
		t.Fatal(err)
	}
	NonBlockingHandshakeTimeout = 5 * time.Second // runtime reconfiguration
	srv.AppendAndRead(hello[10:20], make([]byte, 64))
	srv.Close()
	b.Close()
	waitHandshakesInFlight(t, 0)
	NonBlockingHandshakeTimeout = old
}

// TestReview3SoundGCMNonceChecks (expected to PASS): the GCM shim enforces
// strictly increasing TLS nonces like Go's FIPS-mode GCMWithCounterNonce /
// GCMWithXORCounterNonce, in every mode.
func TestReview3SoundGCMNonceChecks(t *testing.T) {
	mustPanic := func(name string, f func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s: no panic", name)
			}
		}()
		f()
	}
	key := make([]byte, 16)
	// TLS 1.2: explicit 8-byte nonce = record sequence number.
	a12 := aeadAESGCM(key, make([]byte, noncePrefixLength))
	seq := func(n uint64) []byte {
		b := make([]byte, 8)
		for i := 7; i >= 0; i-- {
			b[i] = byte(n)
			n >>= 8
		}
		return b
	}
	a12.Seal(nil, seq(0), []byte("a"), nil)
	a12.Seal(nil, seq(1), []byte("a"), nil)
	mustPanic("TLS 1.2 repeated seq", func() { a12.Seal(nil, seq(1), []byte("a"), nil) })
	mustPanic("TLS 1.2 decreasing seq", func() { a12.Seal(nil, seq(0), []byte("a"), nil) })
	// TLS 1.3: nonce = IV XOR seq; xorNonceAEAD takes the seq.
	a13 := aeadAESGCMTLS13(key, bytes.Repeat([]byte{0x5a}, aeadNonceLength))
	a13.Seal(nil, seq(0), []byte("a"), nil)
	a13.Seal(nil, seq(5), []byte("a"), nil) // gaps are allowed, as in Go
	mustPanic("TLS 1.3 repeated seq", func() { a13.Seal(nil, seq(5), []byte("a"), nil) })
	mustPanic("TLS 1.3 decreasing seq", func() { a13.Seal(nil, seq(4), []byte("a"), nil) })
}

// R3-2 follow-up: a handshake whose GetCertificate is slow (but within
// NonBlockingHandshakeTimeout) still completes, and data then flows both
// ways, for each version and read size.
func TestReview3SlowCallbackHandshakeCompletes(t *testing.T) {
	for _, vers := range []uint16{VersionTLS12, VersionTLS13} {
		for _, chunk := range []int{1, 65536} {
			cfg := nbServerConfig(vers)
			cert := cfg.Certificates[0]
			cfg.Certificates = nil
			cfg.GetCertificate = func(*ClientHelloInfo) (*Certificate, error) {
				time.Sleep(100 * time.Millisecond)
				return &cert, nil
			}
			s := startNonBlockServer(t, cfg, chunk, func(s *nbServer, b []byte) { s.conn.Write(b) })
			c, err := stdtls.Dial("tcp", s.ln.Addr().String(), stdClientConfig())
			if err != nil {
				t.Fatalf("vers %x chunk %d: %v", vers, chunk, err)
			}
			msg := []byte("hello after a slow GetCertificate")
			if _, err := c.Write(msg); err != nil {
				t.Fatal(err)
			}
			got := make([]byte, len(msg))
			c.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, err := io.ReadFull(c, got); err != nil || !bytes.Equal(got, msg) {
				t.Fatalf("vers %x chunk %d: echo %q, %v", vers, chunk, got, err)
			}
			c.Close()
		}
	}
	waitHandshakesInFlight(t, 0)
}
