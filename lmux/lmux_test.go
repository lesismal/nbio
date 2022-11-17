package lmux

import (
	"net"
	"sync"
	"testing"
	"time"
)

func TestListenerMux(t *testing.T) {
	maxOnlineA := 3
	totalConn := 5
	network := "tcp"
	addr1 := "localhost:8001"
	addr2 := "localhost:8002"
	lm := New(maxOnlineA)

	listen := func(addr string) net.Listener {
		l, err := net.Listen(network, addr)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	l1 := listen(addr1)
	listenerA, listenerB := lm.Mux(l1)
	l2 := listen(addr2)
	listenerC, listenerD := lm.Mux(l2)
	lm.Start()

	time.Sleep(time.Second / 5)
	wg := sync.WaitGroup{}
	chA := make(chan net.Conn, totalConn)
	chB := make(chan net.Conn, totalConn)
	chC := make(chan net.Conn, totalConn)
	chD := make(chan net.Conn, totalConn)
	chErr := make(chan error, totalConn)
	conns := make([]net.Conn, totalConn)[:0]

	accept := func(ln net.Listener, chConn chan net.Conn) {
		for {
			conn, err := ln.Accept()
			if err != nil {
				chErr <- err
				break
			}
			chConn <- conn
		}
	}
	go accept(listenerA, chA)
	go accept(listenerB, chB)
	go accept(listenerC, chC)
	go accept(listenerD, chD)

	dialN := func(n int, addr string) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < n; i++ {
				conn, err := net.Dial(network, addr)
				if err != nil {
					chErr <- err
					break
				}
				conns = append(conns, conn)
			}
		}()
	}
	closeConns := func() {
		for _, v := range conns {
			v.Close()
		}
	}
	dialN(totalConn, addr1)
	dialN(totalConn, addr2)

	wg.Wait()
	time.Sleep(time.Second / 5)

	if len(chErr) != 0 {
		t.Fatalf("len(chA) != maxOnlineA, want %v, got %v", 0, len(chErr))
	}

	if len(chA)+len(chC) != maxOnlineA {
		t.Fatalf("len(chA)+len(chC) != maxOnlineA, want %v, got %v[A=%v, C=%v]", maxOnlineA, len(chA)+len(chC), len(chA), len(chC))
	}

	if len(chB)+len(chD) != totalConn*2-maxOnlineA {
		t.Fatalf("len(chB)+len(chD) != maxOnlineA, want %v, got %v[B=%v, D=%v]", totalConn*2-maxOnlineA, len(chB)+len(chD), len(chB), len(chD))
	}

	closeConns()

	clean := func(ln *ChanListener, chConn chan net.Conn) {
		n := len(chConn)
		for i := 0; i < n; i++ {
			<-chConn
			ln.Decrease()
		}
	}
	clean(listenerA, chA)
	clean(listenerB, chB)
	clean(listenerC, chC)
	clean(listenerD, chD)

	conns = conns[:0]
	dialN(totalConn, addr1)
	dialN(totalConn, addr2)
	defer closeConns()

	wg.Wait()
	time.Sleep(time.Second / 5)

	if len(chA)+len(chC) != maxOnlineA {
		t.Fatalf("len(chA)+len(chC) != maxOnlineA, want %v, got %v[A=%v, C=%v]", maxOnlineA, len(chA)+len(chC), len(chA), len(chC))
	}

	if len(chB)+len(chD) != totalConn*2-maxOnlineA {
		t.Fatalf("len(chB)+len(chD) != maxOnlineA, want %v, got %v[B=%v, D=%v]", totalConn*2-maxOnlineA, len(chB)+len(chD), len(chB), len(chD))
	}

	lm.Stop()
}
