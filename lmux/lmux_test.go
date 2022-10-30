package lmux

import (
	"net"
	"sync"
	"testing"
	"time"
)

//go:norace
func TestListenerMux(t *testing.T) {
	maxOnlineA := 3
	totalConn := 5
	network, addr := "tcp", "localhost:8888"
	lm := New(maxOnlineA)
	l, err := net.Listen(network, addr)
	if err != nil {
		t.Fatal(err)
	}
	listenerA, listenerB := lm.Mux(l)
	lm.Start()

	time.Sleep(time.Second / 5)
	wg := sync.WaitGroup{}
	chA := make(chan net.Conn, totalConn)
	chB := make(chan net.Conn, totalConn)
	chErr := make(chan error, totalConn)
	conns := make([]net.Conn, totalConn)[:0]

	wg.Add(1)

	go func() {
		for {
			conn, err := listenerA.Accept()
			if err != nil {
				chErr <- err
				break
			}
			chA <- conn
		}
	}()
	go func() {
		for {
			conn, err := listenerB.Accept()
			if err != nil {
				chErr <- err
				break
			}
			chB <- conn
		}
	}()

	dialN := func(n int) {
		defer wg.Done()
		for i := 0; i < n; i++ {
			conn, err := net.Dial(network, addr)
			if err != nil {
				chErr <- err
				break
			}
			conns = append(conns, conn)
		}
	}
	closeConns := func() {
		for _, v := range conns {
			v.Close()
		}
	}
	go dialN(totalConn)

	wg.Wait()
	time.Sleep(time.Second / 10)

	if len(chErr) != 0 {
		t.Fatalf("len(chA) != maxOnlineA, want %v, got %v", 0, len(chErr))
	}

	if len(chA) != maxOnlineA {
		t.Fatalf("len(chA) != maxOnlineA, want %v, got %v", maxOnlineA, len(chA))
	}

	if len(chB) != totalConn-maxOnlineA {
		t.Fatalf("len(chA) != maxOnlineA, want %v, got %v", totalConn-maxOnlineA, len(chB))
	}

	wg.Add(1)

	closeConns()

	na := len(chA)
	for i := 0; i < na; i++ {
		<-chA
		listenerA.Decrease()
	}

	chA = make(chan net.Conn, totalConn)
	chB = make(chan net.Conn, totalConn)
	chErr = make(chan error, totalConn)
	conns = conns[:0]
	go dialN(totalConn)
	defer closeConns()

	wg.Wait()
	time.Sleep(time.Second / 10)

	if len(chErr) != 0 {
		t.Fatalf("len(chA) != maxOnlineA, want %v, got %v", 0, len(chErr))
	}

	if len(chA) != maxOnlineA {
		t.Fatalf("len(chA) != maxOnlineA, want %v, got %v", maxOnlineA, len(chA))
	}

	if len(chB) != totalConn-maxOnlineA {
		t.Fatalf("len(chA) != maxOnlineA, want %v, got %v", totalConn-maxOnlineA, len(chB))
	}
	lm.Stop()
}
