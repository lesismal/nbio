//go:build linux || darwin || netbsd || freebsd || openbsd || dragonfly
// +build linux darwin netbsd freebsd openbsd dragonfly

package nbio

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// write() only queued the unwritten remainder for ConnTypeTCP, so a short write
// or EAGAIN on a Unix stream socket dropped bytes while reporting success.
func TestUnixWriteKeepsRemainder(t *testing.T) {
	const (
		total = 8 << 20
		chunk = 64 << 10
	)

	// Keep the path short: sun_path is limited to 104 bytes on darwin.
	dir, err := os.MkdirTemp("", "nb")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(dir)
	addr := filepath.Join(dir, "s")

	g := NewEngine(Config{
		Network: "unix",
		Addrs:   []string{addr},
		NPoller: 1,
	})
	var written int64
	writtenDone := make(chan struct{})
	var writtenOnce sync.Once
	opened := make(chan *Conn, 1)
	g.OnOpen(func(c *Conn) {
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

	client, err := net.DialTimeout("unix", addr, time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = client.Close() }()

	var server *Conn
	select {
	case server = <-opened:
	case <-time.After(time.Second):
		t.Fatal("server connection did not open")
	}

	payload := make([]byte, total)
	for i := range payload {
		payload[i] = byte(i % 251)
	}

	// The client is not reading yet, so the socket buffer fills and later
	// writes hit short writes and EAGAIN.
	for off := 0; off < total; off += chunk {
		n, err := server.Write(payload[off : off+chunk])
		if err != nil {
			t.Fatalf("server write at %d: %v", off, err)
		}
		if n != chunk {
			t.Fatalf("server write at %d: n=%d", off, n)
		}
	}

	got := make([]byte, 0, total)
	buf := make([]byte, 64<<10)
	deadline := time.Now().Add(5 * time.Second)
	for len(got) < total && time.Now().Before(deadline) {
		_ = client.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		n, readErr := client.Read(buf)
		got = append(got, buf[:n]...)
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
			t.Fatalf("client read: read=%d err=%v", len(got), readErr)
		}
	}

	if len(got) != total {
		t.Fatalf("client read %d/%d bytes, written=%d", len(got), total, atomic.LoadInt64(&written))
	}
	for i, b := range got {
		if b != byte(i%251) {
			t.Fatalf("data mismatch at offset %d", i)
		}
	}
	select {
	case <-writtenDone:
	case <-time.After(2 * time.Second):
	}
	if w := atomic.LoadInt64(&written); w != total {
		t.Fatalf("OnWrittenSize reported %d/%d bytes", w, total)
	}
}
