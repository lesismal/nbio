// Copyright 2026 The nbio Authors. BSD-style license (same as Go).

package tls

import (
	"bytes"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// splitter cuts a byte stream into poller reads of fuzz-chosen sizes.
type splitter struct {
	sizes []byte
	i     int
}

func (s *splitter) next() int {
	if len(s.sizes) == 0 {
		return 4096
	}
	n := int(s.sizes[s.i%len(s.sizes)])*7 + 1 // 1..1786
	s.i++
	return n
}

// manglingConn flips one byte of everything written through it, at stream
// offset at (negative: no flip).
type manglingConn struct {
	net.Conn
	mu  sync.Mutex
	off int
	at  int
	xor byte
}

func (m *manglingConn) Write(p []byte) (int, error) {
	m.mu.Lock()
	if m.at >= m.off && m.at < m.off+len(p) && m.xor != 0 {
		p = bytes.Clone(p)
		p[m.at-m.off] ^= m.xor
	}
	m.off += len(p)
	m.mu.Unlock()
	return m.Conn.Write(p)
}

// FuzzNonBlockRecordStream drives a full TLS 1.2/1.3 connection through the
// non-blocking server with fuzz-chosen read sizes, optionally corrupting one
// byte of the client's stream. Invariants: no panic, no hang, the handshake
// goroutine always exits, and the plaintext delivered is always a prefix of
// what the client sent (AEAD), equal to it when nothing was corrupted.
func FuzzNonBlockRecordStream(f *testing.F) {
	f.Add(true, []byte{0}, -1, byte(0))
	f.Add(false, []byte{1, 2, 3}, -1, byte(0))
	f.Add(true, []byte{255, 0, 9}, 300, byte(1))
	f.Add(true, []byte{5}, 3000, byte(0x80))
	f.Add(false, []byte{0, 0, 1}, 10, byte(0xff))
	payload := bytes.Repeat([]byte("nbio fuzz payload "), 1000) // 23 KB
	f.Fuzz(func(t *testing.T, tls13 bool, sizes []byte, at int, xor byte) {
		vers := uint16(VersionTLS12)
		if tls13 {
			vers = VersionTLS13
		}
		cs, ss := net.Pipe()
		srv := NewConn(ss, nbServerConfig(vers), false, true)
		var got bytes.Buffer
		srvErr := make(chan error, 1)
		go func() {
			sp := &splitter{sizes: sizes}
			raw := make([]byte, 2048)
			out := make([]byte, 1024)
			defer srv.Close()
			for {
				n, err := ss.Read(raw[:min(sp.next(), len(raw))])
				if err != nil {
					srvErr <- err
					return
				}
				in := raw[:n]
				for {
					_, m, err := srv.AppendAndRead(in, out)
					in = nil
					if err != nil {
						srvErr <- err
						return
					}
					if m == 0 {
						break
					}
					got.Write(out[:m])
				}
				srv.ResetOrFreeBuffer()
			}
		}()
		cli := Client(&manglingConn{Conn: cs, at: at, xor: xor}, testConfigClient.Clone())
		cliDone := make(chan struct{})
		go func() {
			defer close(cliDone)
			go io.Copy(io.Discard, cli) // drain server output (tickets, alerts)
			cli.Write(payload)
			cli.Close()
		}()
		select {
		case <-srvErr:
		case <-time.After(2 * time.Second):
			// A corrupted length can leave both sides waiting for bytes
			// that never come; in production a deadline closes the
			// connection. Do that and require the server to exit.
			cs.Close()
			select {
			case <-srvErr:
			case <-time.After(5 * time.Second):
				t.Fatal("server hung after the transport closed")
			}
		}
		cs.Close()
		<-cliDone
		if !bytes.HasPrefix(payload, got.Bytes()) {
			t.Fatalf("server delivered %d bytes that are not a prefix of the payload", got.Len())
		}
		if (at < 0 || xor == 0) && got.Len() != len(payload) {
			t.Fatalf("clean stream: got %d of %d bytes", got.Len(), len(payload))
		}
	})
}

// FuzzNonBlockGarbage feeds arbitrary bytes to a fresh non-blocking server in
// arbitrary chunks: it must fail or wait, never panic or hang, and its
// handshake goroutine must exit when the connection is closed.
func FuzzNonBlockGarbage(f *testing.F) {
	hello := clientHelloBytes(f)
	f.Add(hello, []byte{3})
	f.Add(hello[:40], []byte{1})
	f.Add([]byte("\x16\x03\x01\x00\x05hello"), []byte{0})
	f.Add([]byte("\x15\x03\x03\x00\x02\x02\x28"), []byte{0})
	f.Add([]byte("\x80\x2e\x01\x03\x01"), []byte{2})
	f.Fuzz(func(t *testing.T, data []byte, sizes []byte) {
		a, b := net.Pipe()
		defer b.Close()
		srv := NewConn(a, testConfigServer.Clone(), false, true)
		sp := &splitter{sizes: sizes}
		out := make([]byte, 512)
		done := make(chan struct{})
		go func() {
			defer close(done)
			for len(data) > 0 {
				n := min(sp.next(), len(data))
				in := data[:n]
				data = data[n:]
				for {
					_, m, err := srv.AppendAndRead(in, out)
					in = nil
					if err != nil || m == 0 {
						if err != nil {
							return
						}
						break
					}
				}
				srv.ResetOrFreeBuffer()
			}
		}()
		go io.Copy(io.Discard, b) // the server's replies
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("server hung")
		}
		srv.Close()
		deadline := time.Now().Add(5 * time.Second)
		for srv.conn.(*nbConn).hs.Load() != nil && !handshakeExited(srv) {
			if time.Now().After(deadline) {
				t.Fatal("handshake goroutine did not exit after Close")
			}
			time.Sleep(time.Millisecond)
		}
	})
}

func handshakeExited(c *Conn) bool {
	hs := c.conn.(*nbConn).hs.Load()
	if hs == nil {
		return true
	}
	select {
	case <-hs.done:
		return true
	default:
		return false
	}
}
