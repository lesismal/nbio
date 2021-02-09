package nbio

import (
	"log"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

var addr = "localhost:8888"
var gopher *Gopher

func init() {
	addrs := []string{addr}
	g, err := NewGopher(Config{
		Network: "tcp",
		Addrs:   addrs,
		NPoller: 1,
	})
	if err != nil {
		log.Fatalf("NewGopher failed: %v\n", err)
	}

	g.OnOpen(func(c *Conn) {
		c.SetReadDeadline(time.Now().Add(time.Second / 5))
	})
	g.OnData(func(c *Conn, data []byte) {
		c.Write(append([]byte{}, data...))
	})
	g.OnClose(func(c *Conn, err error) {})

	err = g.Start()
	if err != nil {
		log.Fatalf("Start failed: %v\n", err)
	}

	gopher = g
}

func TestAll(t *testing.T) {
	test10k(t)
	gopher.Stop()
}

func test10k(t *testing.T) {
	g, err := NewGopher(Config{})
	if err != nil {
		log.Fatalf("NewGopher failed: %v\n", err)
	}

	var total int64 = 0
	var clientNum int64 = 1024 * 10
	var done = make(chan int)

	if runtime.GOOS == "windows" {
		clientNum = 1024
	}

	t.Log("testing concurrent:", clientNum, "connections")

	g.OnOpen(func(c *Conn) {
		c.Close()
		if atomic.AddInt64(&total, 1) == clientNum {
			close(done)
		}
	})

	err = g.Start()
	if err != nil {
		log.Fatalf("Start failed: %v\n", err)
	}

	go func() {
		for i := 0; i < int(clientNum); i++ {
			go func() {
				if runtime.GOOS == "windows" {
					go func() {
						c, err := Dial("tcp", addr)
						if err != nil {
							t.Fatalf("Dial failed: %v", err)
						}
						g.AddConn(c)
					}()
					// 	time.Sleep(time.Second / 1000)
				} else {
					c, err := Dial("tcp", addr)
					if err != nil {
						t.Fatalf("Dial failed: %v", err)
					}
					g.AddConn(c)
				}
			}()
		}
	}()

	<-done
}
