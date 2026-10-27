//go:build linux
// +build linux

package nbio

import (
	"bytes"
	"net"
	"testing"
	"time"
)

// The IO task pool that performs asynchronous reads used to be created with a
// zero-length buffer, so AsyncRead always read 0 bytes and no data was ever
// delivered for the EPOLLET + AsyncReadInPoller combination.
func TestETAsyncReadReceivesData(t *testing.T) {
	g := NewEngine(Config{
		Network: "tcp",
		Addrs:   []string{"127.0.0.1:0"},
	})
	g.SetETAsyncRead()

	payload := []byte("et-async-read-payload")
	received := make(chan []byte, 1)
	g.OnData(func(_ *Conn, data []byte) {
		select {
		case received <- append([]byte(nil), data...):
		default:
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
	if _, err = client.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}

	select {
	case data := <-received:
		if !bytes.Equal(data, payload) {
			t.Fatalf("payload = %q, want %q", data, payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no data delivered with SetETAsyncRead")
	}
}
