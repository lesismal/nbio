//go:build linux
// +build linux

package nbio

import (
	"bytes"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// With EPOLLONESHOT every event is disarmed when it is delivered. flush() kept
// writing until EAGAIN and then returned without re-arming EPOLLOUT, so a
// write-only flow stalled with most of the payload still queued.
func TestOneshotFlushRearmsWrite(t *testing.T) {
	const total = 8 << 20

	g := NewEngine(Config{
		Network:           "tcp",
		Addrs:             []string{"127.0.0.1:0"},
		NPoller:           1,
		ReadBufferSize:    32 << 10,
		EpollMod:          EPOLLET,
		EPOLLONESHOT:      EPOLLONESHOT,
		AsyncReadInPoller: true,
	})
	var written int64
	writtenDone := make(chan struct{})
	var writtenOnce sync.Once
	opened := make(chan *Conn, 1)
	writeErrs := make(chan error, 1)
	g.OnOpen(func(c *Conn) {
		_ = c.SetNoDelay(true)
		// Keep the socket backlogged so flush() has to hit EAGAIN repeatedly.
		_ = c.SetWriteBuffer(32 << 10)
		opened <- c
	})
	g.OnWrittenSize(func(_ *Conn, _ []byte, n int) {
		if atomic.AddInt64(&written, int64(n)) >= total {
			writtenOnce.Do(func() { close(writtenDone) })
		}
	})
	if err := g.Start(); err != nil {
		t.Fatalf("start engine: %v", err)
	}
	defer g.Stop()

	client, err := net.DialTimeout("tcp", g.Addrs[0], time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = client.Close() }()
	if tcpConn, ok := client.(*net.TCPConn); ok {
		_ = tcpConn.SetReadBuffer(32 << 10)
	}

	var server *Conn
	select {
	case server = <-opened:
	case <-time.After(time.Second):
		t.Fatal("server connection did not open")
	}

	payload := bytes.Repeat([]byte("x"), total)
	go func() {
		_, err := server.Write(payload)
		writeErrs <- err
	}()
	select {
	case err := <-writeErrs:
		if err != nil && !errors.Is(err, syscall.EAGAIN) {
			t.Fatalf("server write: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server write did not return")
	}

	buf := make([]byte, 32<<10)
	var read int64
	deadline := time.Now().Add(5 * time.Second)
	for read < total && time.Now().Before(deadline) {
		_ = client.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		n, readErr := client.Read(buf)
		read += int64(n)
		if n > 0 {
			continue
		}
		if readErr != nil {
			if ne, ok := readErr.(net.Error); ok && ne.Timeout() {
				continue
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
			t.Fatalf("client read: read=%d written=%d err=%v", read, atomic.LoadInt64(&written), readErr)
		}
	}

	select {
	case <-writtenDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("write stalled at %d/%d bytes; client read %d", atomic.LoadInt64(&written), total, read)
	}
	if read != total {
		t.Fatalf("client read %d/%d", read, total)
	}
}
