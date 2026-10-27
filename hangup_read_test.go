//go:build linux
// +build linux

package nbio

import (
	"bytes"
	"net"
	"sync"
	"testing"
	"time"
)

// A peer FIN/RST is reported by epoll together with the last readable bytes.
// Closing on EPOLLHUP/EPOLLRDHUP before reading them discarded everything the
// kernel had already buffered beyond the per-event read limit.
func TestPeerHangupDrainsBufferedData(t *testing.T) {
	for _, mode := range []struct {
		name     string
		epollMod uint32
	}{
		{name: "level-triggered", epollMod: EPOLLLT},
		{name: "edge-triggered", epollMod: EPOLLET},
	} {
		t.Run(mode.name, func(t *testing.T) {
			testPeerHangupDrain(t, mode.epollMod)
		})
	}
}

func testPeerHangupDrain(t *testing.T, epollMod uint32) {
	t.Helper()

	payload := bytes.Repeat([]byte("tail"), 256)
	var (
		mu       sync.Mutex
		received []byte
	)
	closed := make(chan struct{})
	g := NewEngine(Config{
		Network:                      "tcp",
		Addrs:                        []string{"127.0.0.1:0"},
		NPoller:                      1,
		ReadBufferSize:               64,
		MaxConnReadTimesPerEventLoop: 3,
		EpollMod:                     epollMod,
	})
	g.OnData(func(_ *Conn, data []byte) {
		mu.Lock()
		received = append(received, data...)
		mu.Unlock()
	})
	g.OnClose(func(_ *Conn, _ error) { close(closed) })
	if err := g.Start(); err != nil {
		t.Fatalf("start engine: %v", err)
	}
	defer g.Stop()

	client, err := net.DialTimeout("tcp", g.Addrs[0], time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if _, err = client.Write(payload); err != nil {
		_ = client.Close()
		t.Fatalf("write payload: %v", err)
	}
	if err = client.Close(); err != nil {
		t.Fatalf("close client: %v", err)
	}

	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("peer hangup did not close the server connection")
	}

	mu.Lock()
	got := append([]byte(nil), received...)
	mu.Unlock()
	if !bytes.Equal(got, payload) {
		t.Fatalf("received %d/%d bytes before close", len(got), len(payload))
	}
}
