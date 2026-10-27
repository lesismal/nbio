//go:build linux
// +build linux

package nbio

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A datagram read that fills less than the whole buffer only means that the
// datagram was small. The read loop used to break out of the loop on such a
// short read, which under EPOLLET drops every datagram that was already queued
// behind it: the socket is still readable, but no new event is delivered until
// another datagram arrives.
func TestUDPEdgeReadsBurstDatagrams(t *testing.T) {
	const datagrams = 20

	g := NewEngine(Config{NPoller: 1, EpollMod: EPOLLET})
	var received int32
	all := make(chan struct{})
	var once sync.Once
	g.OnData(func(_ *Conn, _ []byte) {
		if atomic.AddInt32(&received, 1) == datagrams {
			once.Do(func() { close(all) })
		}
	})
	if err := g.Start(); err != nil {
		t.Fatalf("start engine: %v", err)
	}
	defer g.Stop()

	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	port := server.LocalAddr().(*net.UDPAddr).Port
	if _, err = g.AddConn(server); err != nil {
		t.Fatal(err)
	}

	client, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	// Queue the whole burst before the poller can run, so that every datagram
	// after the first one is already sitting in the receive buffer.
	for i := 0; i < datagrams; i++ {
		if _, err = client.Write([]byte("datagram")); err != nil {
			t.Fatal(err)
		}
	}

	select {
	case <-all:
	case <-time.After(3 * time.Second):
		t.Fatalf("received %d/%d datagrams", atomic.LoadInt32(&received), datagrams)
	}
}
