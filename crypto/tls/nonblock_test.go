// Copyright 2026 The nbio Authors. BSD-style license (same as Go).

package tls

import (
	"bytes"
	stdtls "crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// pollerLoop plays nbio's poller for one server connection: it reads the raw
// transport into a reused buffer of size chunk and hands each read to
// AppendAndRead, like nbhttp's TLSDataHandler. Plaintext goes to onData.
// When the transport fails it closes the TLS connection.
func pollerLoop(raw net.Conn, srv *Conn, chunk int, onData func([]byte)) error {
	buf := make([]byte, chunk)
	out := make([]byte, 2048)
	defer srv.Close()
	for {
		n, err := raw.Read(buf)
		if err != nil {
			return err
		}
		in := buf[:n]
		for {
			_, m, err := srv.AppendAndRead(in, out)
			in = nil
			if err != nil {
				return err
			}
			if m == 0 {
				break
			}
			onData(out[:m])
		}
		srv.ResetOrFreeBuffer()
		// Scribble over the poller buffer: nothing may keep pointing into it.
		for i := range buf {
			buf[i] = 0xAA
		}
	}
}

// newNonBlockServer accepts one TCP connection on a loopback listener and
// serves it with a non-blocking Conn driven by pollerLoop.
type nbServer struct {
	ln     net.Listener
	conn   *Conn
	raw    net.Conn
	ready  chan struct{}
	done   chan error
	mu     sync.Mutex
	got    bytes.Buffer
	onData func(*nbServer, []byte)
}

func startNonBlockServer(t testing.TB, cfg *Config, chunk int, onData func(*nbServer, []byte)) *nbServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &nbServer{ln: ln, ready: make(chan struct{}), done: make(chan error, 1), onData: onData}
	go func() {
		raw, err := ln.Accept()
		if err != nil {
			s.done <- err
			close(s.ready)
			return
		}
		s.raw = raw
		s.conn = NewConn(raw, cfg, false, true)
		close(s.ready)
		s.done <- pollerLoop(raw, s.conn, chunk, func(b []byte) {
			s.mu.Lock()
			s.got.Write(b)
			s.mu.Unlock()
			if s.onData != nil {
				s.onData(s, b)
			}
		})
	}()
	t.Cleanup(func() { ln.Close() })
	return s
}

func stdClientConfig() *stdtls.Config {
	pool := x509.NewCertPool()
	pool.AddCert(testConfigServer.Certificates[0].Leaf)
	return &stdtls.Config{RootCAs: pool, ServerName: "example.golang", InsecureSkipVerify: true}
}

func nbServerConfig(vers uint16) *Config {
	cfg := testConfigServer.Clone()
	cfg.MinVersion, cfg.MaxVersion = vers, vers
	return cfg
}

// TestNonBlockEchoStdClient runs the non-blocking server against Go's
// standard library client (an independent copy of the code) for each
// protocol version and several poller read sizes, including 1 byte.
func TestNonBlockEchoStdClient(t *testing.T) {
	for _, vers := range []uint16{VersionTLS12, VersionTLS13} {
		for _, chunk := range []int{1, 7, 100, 512, 4096, 65536} {
			t.Run(fmt.Sprintf("%s/chunk=%d", VersionName(vers), chunk), func(t *testing.T) {
				echo := func(s *nbServer, b []byte) {
					if _, err := s.conn.Write(b); err != nil {
						t.Errorf("server write: %v", err)
					}
				}
				s := startNonBlockServer(t, nbServerConfig(vers), chunk, echo)
				c, err := stdtls.Dial("tcp", s.ln.Addr().String(), stdClientConfig())
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				if got := c.ConnectionState().Version; got != vers {
					t.Fatalf("negotiated %x, want %x", got, vers)
				}
				msg := bytes.Repeat([]byte("0123456789abcdef"), 3000) // 48 KB: several records
				go func() {
					if _, err := c.Write(msg); err != nil {
						t.Errorf("client write: %v", err)
					}
				}()
				back := make([]byte, len(msg))
				if _, err := io.ReadFull(c, back); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(back, msg) {
					t.Fatal("echo mismatch")
				}
			})
		}
	}
}

// TestNonBlockFirstDataWithFinished checks the common TLS 1.3 case where the
// client's first application data arrives in the same read as its Finished,
// so the poller must read it after the handshake goroutine has exited.
func TestNonBlockFirstDataWithFinished(t *testing.T) {
	s := startNonBlockServer(t, nbServerConfig(VersionTLS13), 65536, nil)
	rawClient, err := net.Dial("tcp", s.ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	// Buffer the client's writes so Finished and the request go out together.
	bw := &batchConn{Conn: rawClient}
	c := stdtls.Client(bw, stdClientConfig())
	bw.hold = true
	go func() {
		// The client handshake writes ClientHello, then waits for the
		// server flight, then writes Finished; we flush only after the
		// request is written too.
		if err := c.Handshake(); err != nil {
			t.Error(err)
			return
		}
		c.Write([]byte("GET / HTTP/1.1\r\n\r\n"))
		bw.flush()
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		n := s.got.Len()
		s.mu.Unlock()
		if n == len("GET / HTTP/1.1\r\n\r\n") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server got %d bytes", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
	c.Close()
}

// batchConn holds writes after the first one (ClientHello) until flush.
type batchConn struct {
	net.Conn
	mu     sync.Mutex
	hold   bool
	writes int
	buf    bytes.Buffer
}

func (b *batchConn) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.writes++
	if b.hold && b.writes > 1 {
		return b.buf.Write(p)
	}
	return b.Conn.Write(p)
}

func (b *batchConn) flush() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Conn.Write(b.buf.Bytes())
	b.buf.Reset()
	b.hold = false
}

// TestNonBlockOwnClient runs this package's client against the non-blocking
// server, including session resumption with tickets.
func TestNonBlockOwnClient(t *testing.T) {
	for _, vers := range []uint16{VersionTLS12, VersionTLS13} {
		t.Run(VersionName(vers), func(t *testing.T) {
			cfg := nbServerConfig(vers)
			clientCfg := testConfigClient.Clone()
			clientCfg.ClientSessionCache = NewLRUClientSessionCache(4)
			for i := 0; i < 2; i++ {
				s := startNonBlockServer(t, cfg, 333, func(s *nbServer, b []byte) { s.conn.Write(b) })
				c, err := Dial("tcp", s.ln.Addr().String(), clientCfg)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := c.Write([]byte("ping")); err != nil {
					t.Fatal(err)
				}
				b := make([]byte, 4)
				if _, err := io.ReadFull(c, b); err != nil || string(b) != "ping" {
					t.Fatalf("got %q, %v", b, err)
				}
				if resumed := c.ConnectionState().DidResume; resumed != (i == 1) {
					t.Fatalf("round %d: DidResume = %v", i, resumed)
				}
				c.Close()
			}
		})
	}
}

// TestNonBlockBadClientHello: garbage instead of a ClientHello fails the
// handshake with an error, without hanging.
func TestNonBlockBadClientHello(t *testing.T) {
	s := startNonBlockServer(t, nbServerConfig(VersionTLS13), 512, nil)
	c, err := net.Dial("tcp", s.ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n"))
	select {
	case err := <-s.done:
		if err == nil || errors.Is(err, io.EOF) {
			t.Fatalf("got %v, want a handshake error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not fail the handshake")
	}
	waitHandshakesInFlight(t, 0)
}

func waitHandshakesInFlight(t testing.TB, want int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for HandshakesInFlight() != want {
		if time.Now().After(deadline) {
			t.Fatalf("HandshakesInFlight = %d, want %d", HandshakesInFlight(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestNonBlockAbandonedHandshakeNoLeak: clients that send part of a
// ClientHello and disconnect must not leave handshake goroutines behind.
func TestNonBlockAbandonedHandshakeNoLeak(t *testing.T) {
	hello := clientHelloBytes(t)
	before := runtime.NumGoroutine()
	for i := 0; i < 50; i++ {
		s := startNonBlockServer(t, nbServerConfig(VersionTLS13), 64, nil)
		c, err := net.Dial("tcp", s.ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		c.Write(hello[:len(hello)/2])
		time.Sleep(time.Millisecond)
		c.Close()
		<-s.done
		s.ln.Close()
	}
	waitHandshakesInFlight(t, 0)
	time.Sleep(50 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+5 {
		t.Fatalf("goroutines: %d before, %d after", before, after)
	}
}

// TestNonBlockHandshakeTimeout: a client that stops mid-handshake and keeps
// the TCP connection open is cut off by NonBlockingHandshakeTimeout.
func TestNonBlockHandshakeTimeout(t *testing.T) {
	defer func(d time.Duration) { NonBlockingHandshakeTimeout = d }(NonBlockingHandshakeTimeout)
	NonBlockingHandshakeTimeout = 200 * time.Millisecond
	hello := clientHelloBytes(t)
	s := startNonBlockServer(t, nbServerConfig(VersionTLS13), 64, nil)
	c, err := net.Dial("tcp", s.ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write(hello[:len(hello)-1])
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		t.Fatal("handshake did not time out")
	}
	waitHandshakesInFlight(t, 0)
}

// clientHelloBytes captures a real ClientHello from this package's client.
func clientHelloBytes(t testing.TB) []byte {
	a, b := net.Pipe()
	defer a.Close()
	go func() {
		Client(a, testConfigClient.Clone()).Handshake()
	}()
	hdr := make([]byte, 5)
	if _, err := io.ReadFull(b, hdr); err != nil {
		t.Fatal(err)
	}
	body := make([]byte, int(hdr[3])<<8|int(hdr[4]))
	if _, err := io.ReadFull(b, body); err != nil {
		t.Fatal(err)
	}
	b.Close()
	return append(hdr, body...)
}

// TestNonBlockDirectHandshakeFails: calling Handshake (or Read/Write) on a
// non-blocking connection before AppendAndRead fails instead of blocking.
func TestNonBlockDirectHandshakeFails(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	srv := NewConn(a, testConfigServer.Clone(), false, true)
	errc := make(chan error, 1)
	go func() { errc <- srv.Handshake() }()
	select {
	case err := <-errc:
		if !errors.Is(err, errHandshakeNotDriven) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Handshake blocked")
	}
}

// TestNonBlockReleaseBuffers: after the poller has consumed everything, an
// idle connection keeps no read buffers.
func TestNonBlockReleaseBuffers(t *testing.T) {
	s := startNonBlockServer(t, nbServerConfig(VersionTLS13), 4096, nil)
	c, err := stdtls.Dial("tcp", s.ln.Addr().String(), stdClientConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write(bytes.Repeat([]byte{'x'}, 20000))
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		n := s.got.Len()
		s.mu.Unlock()
		if n == 20000 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("got %d", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond) // let the poller return from ResetOrFreeBuffer
	srv := s.conn
	srv.in.Lock()
	defer srv.in.Unlock()
	if srv.rawInput.Cap() != 0 || srv.hand.Cap() != 0 || srv.input.Size() != 0 {
		t.Fatalf("buffers kept: rawInput cap %d, hand cap %d, input size %d",
			srv.rawInput.Cap(), srv.hand.Cap(), srv.input.Size())
	}
	if f := srv.conn.(*nbConn); f.pending != nil {
		t.Fatalf("pending kept: %d bytes", len(f.pending))
	}
}

// TestNonBlockLlibAPI covers the rest of the llib-compatible surface.
func TestNonBlockLlibAPI(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	c := NewConn(a, testConfigServer.Clone(), false, true, nil)
	if !c.IsNonBlock() || c.Conn() != a {
		t.Fatal("IsNonBlock/Conn")
	}
	c.SetSession(42)
	if c.Session() != 42 {
		t.Fatal("Session")
	}
	c.ResetConn(b, false)
	if c.IsNonBlock() || c.Conn() != b {
		t.Fatal("ResetConn")
	}
	plain := Server(a, testConfigServer.Clone())
	if plain.Session() != nil {
		t.Fatal("Session on plain conn")
	}
	plain.SetSession("x")
	if plain.Session() != "x" || plain.Conn() != a {
		t.Fatal("SetSession on plain conn")
	}
}

// TestNonBlockAliasedBuffers: nbio passes the poller's buffer as both the
// input and the output of AppendAndRead. Plaintext written to out must never
// overwrite ciphertext not yet consumed (found by review; large uploads failed
// with "bad record MAC").
func TestNonBlockAliasedBuffers(t *testing.T) {
	for _, vers := range []uint16{VersionTLS12, VersionTLS13} {
		for _, size := range []int{3000, 17000, 40000} {
			t.Run(fmt.Sprintf("%s/buf=%d", VersionName(vers), size), func(t *testing.T) {
				ln, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer ln.Close()
				var got bytes.Buffer
				done := make(chan error, 1)
				go func() {
					raw, err := ln.Accept()
					if err != nil {
						done <- err
						return
					}
					srv := NewConn(raw, nbServerConfig(vers), false, true)
					defer srv.Close()
					buf := make([]byte, size)
					for got.Len() < 256<<10 {
						n, err := raw.Read(buf)
						if err != nil {
							done <- err
							return
						}
						in := buf[:n]
						for {
							_, m, err := srv.AppendAndRead(in, buf) // aliased, like nbhttp
							in = nil
							if err != nil {
								done <- err
								return
							}
							if m == 0 {
								break
							}
							got.Write(buf[:m])
						}
						srv.ResetOrFreeBuffer()
					}
					done <- nil
				}()
				c, err := stdtls.Dial("tcp", ln.Addr().String(), stdClientConfig())
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				msg := make([]byte, 256<<10)
				for i := range msg {
					msg[i] = byte(i * 7)
				}
				go c.Write(msg)
				if err := <-done; err != nil {
					t.Fatalf("after %d bytes: %v", got.Len(), err)
				}
				if !bytes.Equal(got.Bytes(), msg) {
					t.Fatal("plaintext mismatch")
				}
			})
		}
	}
}

// TestMeasureStalledHandshakeCost measures the memory held by connections
// that send a ClientHello and then stall (a slowloris): each one holds a
// parked handshake goroutine and its handshake state until the deadline.
// Run with ARCO_MEASURE=1.
func TestMeasureStalledHandshakeCost(t *testing.T) {
	if os.Getenv("ARCO_MEASURE") == "" {
		t.Skip("set ARCO_MEASURE=1")
	}
	const n = 2000
	hello := clientHelloBytes(t)
	cfg := nbServerConfig(VersionTLS13)
	var conns []*Conn
	var peers []net.Conn
	measure := func() (heap, stack uint64) {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc, m.StackInuse
	}
	// Transports and Conns first, so that only the handshakes are measured.
	for i := 0; i < n; i++ {
		a, b := net.Pipe()
		go io.Copy(io.Discard, b) // swallow the server flight
		conns, peers = append(conns, NewConn(a, cfg, false, true)), append(peers, b)
	}
	out := make([]byte, 64)
	h0, s0 := measure()
	for _, srv := range conns {
		if _, _, err := srv.AppendAndRead(hello, out); err != nil {
			t.Fatal(err)
		}
	}
	waitHandshakesInFlight(t, n)
	h1, s1 := measure()
	t.Logf("%d stalled handshakes: heap %.1f KiB/conn, goroutine stacks %.1f KiB/conn, total %.1f KiB/conn",
		n, float64(h1-h0)/1024/n, float64(s1-s0)/1024/n, float64(h1-h0+s1-s0)/1024/n)
	for i := range conns {
		conns[i].Close()
		peers[i].Close()
	}
	waitHandshakesInFlight(t, 0)
}

// BenchmarkNonBlockHandshake measures a full TLS 1.3 handshake (ECDSA P-256,
// X25519MLKEM768) against the non-blocking server driven like a poller, and
// against Go's standard library server, both over net.Pipe with the standard
// library client. The difference is the cost of the coroutine hand-offs; the
// server's share of the time is how long a poller is busy per new connection.
func BenchmarkNonBlockHandshake(b *testing.B) {
	cert := testECDSAP256Cert
	ourCfg := &Config{Certificates: []Certificate{cert}}
	stdCfg := &stdtls.Config{Certificates: []stdtls.Certificate{{Certificate: cert.Certificate, PrivateKey: cert.PrivateKey}}}
	cliCfg := &stdtls.Config{InsecureSkipVerify: true}
	run := func(b *testing.B, server func(net.Conn) error) {
		for i := 0; i < b.N; i++ {
			a, s := net.Pipe()
			errc := make(chan error, 1)
			go func() { errc <- stdtls.Client(a, cliCfg).Handshake() }()
			if err := server(s); err != nil {
				b.Fatal(err)
			}
			if err := <-errc; err != nil {
				b.Fatal(err)
			}
			a.Close()
			s.Close()
		}
	}
	b.Run("nonblock", func(b *testing.B) {
		run(b, func(s net.Conn) error {
			srv := NewConn(s, ourCfg, false, true)
			buf := make([]byte, 4096)
			out := make([]byte, 64)
			for !srv.HandshakeComplete() {
				n, err := s.Read(buf)
				if err != nil {
					return err
				}
				if _, _, err := srv.AppendAndRead(buf[:n], out); err != nil {
					return err
				}
			}
			return nil
		})
	})
	b.Run("stdlib", func(b *testing.B) {
		run(b, func(s net.Conn) error { return stdtls.Server(s, stdCfg).Handshake() })
	})
}

// TestNonBlockHandshakePanicRecovered: a panicking callback (here
// GetConfigForClient) during a non-blocking handshake must fail that
// connection only. Before the fix it crashed the process: the handshake
// goroutine had no recover, unlike llib's handshake on nbio's poller.
func TestNonBlockHandshakePanicRecovered(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	go io.Copy(io.Discard, b)
	cfg := testConfigServer.Clone()
	cfg.GetConfigForClient = func(*ClientHelloInfo) (*Config, error) { panic("boom from callback") }
	srv := NewConn(a, cfg, false, true)
	_, _, err := srv.AppendAndRead(clientHelloBytes(t), make([]byte, 64))
	if err == nil || !strings.Contains(err.Error(), "boom from callback") {
		t.Fatalf("got %v, want the recovered panic", err)
	}
	waitHandshakesInFlight(t, 0)
}

// TestNonBlockFailedHandshakeErrorSticky: once the handshake failed, every
// later AppendAndRead returns the error (it used to look like "need more
// input" when called without new input).
func TestNonBlockFailedHandshakeErrorSticky(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	go io.Copy(io.Discard, b)
	srv := NewConn(a, testConfigServer.Clone(), false, true)
	out := make([]byte, 64)
	if _, _, err := srv.AppendAndRead([]byte("GET / HTTP/1.1\r\n\r\n"), out); err == nil {
		t.Fatal("garbage accepted")
	}
	for _, in := range [][]byte{nil, []byte("more")} {
		if _, _, err := srv.AppendAndRead(in, out); err == nil {
			t.Fatalf("AppendAndRead(%q) after a failed handshake returned nil", in)
		}
	}
	waitHandshakesInFlight(t, 0)
}
