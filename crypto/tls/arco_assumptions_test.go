// Copyright 2026 The nbio Authors. BSD-style license (same as Go).

// Canaries for the assumptions nonblock.go makes about Go's crypto/tls
// internals (crypto/tls/README.md). If a Go release breaks one, these fail with a
// message that says which assumption, before anything subtler does.

package tls

import (
	"bytes"
	"io"
	"net"
	"sync"
	"testing"
)

// Compile-time: the read-side buffers are the types ReleaseBuffers resets,
// and the halfConns are lockable.
var (
	_ *bytes.Buffer = &(&Conn{}).rawInput
	_ *bytes.Buffer = &(&Conn{}).hand
	_ *bytes.Reader = &(&Conn{}).input
	_ sync.Locker   = &(&Conn{}).in
	_ sync.Locker   = &(&Conn{}).out
)

// stutterConn returns errWouldBlock every other Read, then the next chunk.
type stutterConn struct {
	net.Conn
	mu    sync.Mutex
	block bool
}

func (s *stutterConn) Read(p []byte) (int, error) {
	s.mu.Lock()
	s.block = !s.block
	b := s.block
	s.mu.Unlock()
	if b {
		return 0, errWouldBlock
	}
	if len(p) > 3 {
		p = p[:3] // tiny reads: records always end up split
	}
	return s.Conn.Read(p)
}

// TestAssumptionTemporaryErrorsNotSticky: after the handshake, a temporary
// read error mid-record must leave the connection usable and the partial
// record in rawInput (Go's readRecordOrCCS skips setErrorLocked for
// Temporary() errors).
func TestAssumptionTemporaryErrorsNotSticky(t *testing.T) {
	c, s := localPipe(t)
	srvRaw := &stutterConn{Conn: s}
	srv := Server(srvRaw, testConfigServer.Clone())
	cli := Client(c, testConfigClient.Clone())
	go func() {
		if err := cli.Handshake(); err != nil {
			t.Error(err)
			return
		}
		cli.Write(bytes.Repeat([]byte("z"), 5000))
		io.Copy(io.Discard, cli)
	}()
	// The handshake itself must not see the stutter: Go makes handshake
	// errors sticky (that is why nonblock.go runs it on a coroutine).
	hsDone := make(chan error, 1)
	go func() { hsDone <- handshakeIgnoringStutter(srv) }()
	if err := <-hsDone; err != nil {
		t.Fatal(err)
	}
	got := 0
	buf := make([]byte, 1000)
	for got < 5000 {
		n, err := srv.Read(buf)
		got += n
		if err != nil && err != errWouldBlock {
			t.Fatalf("after %d bytes: %v (temporary errors became sticky?)", got, err)
		}
	}
	srv.Close()
}

// handshakeIgnoringStutter runs the handshake over a transport that does not
// stutter, then turns the stutter on.
func handshakeIgnoringStutter(srv *Conn) error {
	st := srv.conn.(*stutterConn)
	srv.conn = st.Conn
	err := srv.Handshake()
	srv.conn = st
	return err
}
