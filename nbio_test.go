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

func TestEcho(t *testing.T) {
	var done = make(chan int)
	var clientNum = 2
	var msgSize = 1024
	var total int64 = 0

	g, err := NewGopher(Config{})
	if err != nil {
		log.Fatalf("NewGopher failed: %v\n", err)
	}
	err = g.Start()
	if err != nil {
		log.Fatalf("Start failed: %v\n", err)
	}
	defer g.Stop()

	g.OnOpen(func(c *Conn) {
		c.SetSession(1)
		if c.Session() != 1 {
			t.Fatalf("invalid session: %v", c.Session())
		}
		log.Printf("connected local addr 111: %v, remote addr: %v", c.LocalAddr(), c.RemoteAddr())
		c.SetLinger(1, 0)
		c.SetNoDelay(true)
		c.SetKeepAlive(true)
		c.SetKeepAlivePeriod(time.Second * 60)
		c.SetDeadline(time.Now().Add(time.Second))
		c.SetReadBuffer(1024 * 4)
		c.SetWriteBuffer(1024 * 4)
		log.Printf("connected local addr 222: %v, remote addr: %v", c.LocalAddr(), c.RemoteAddr())
	})
	g.OnData(func(c *Conn, data []byte) {
		recved := atomic.AddInt64(&total, int64(len(data)))
		if recved >= int64(clientNum*msgSize) {
			close(done)
		}
	})

	for i := 0; i < clientNum; i++ {
		n := i
		if runtime.GOOS == "windows" {
			go func() {
				c, err := Dial("tcp", addr)
				if err != nil {
					log.Fatalf("Dial failed: %v", err)
				}
				g.AddConn(c)
				if n%2 == 0 {
					c.Write(make([]byte, msgSize))
				} else {
					c.Writev([][]byte{make([]byte, msgSize)})
				}
			}()
		}
	}

	<-done
}

func Test10k(t *testing.T) {
	g, err := NewGopher(Config{})
	if err != nil {
		log.Fatalf("NewGopher failed: %v\n", err)
	}
	err = g.Start()
	if err != nil {
		log.Fatalf("Start failed: %v\n", err)
	}
	defer g.Stop()

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

	go func() {
		for i := 0; i < int(clientNum); i++ {
			go func() {
				if runtime.GOOS == "windows" {
					go func() {
						c, err := Dial("tcp", addr)
						if err != nil {
							log.Fatalf("Dial failed: %v", err)
						}
						g.AddConn(c)
					}()
					// 	time.Sleep(time.Second / 1000)
				} else {
					c, err := Dial("tcp", addr)
					if err != nil {
						log.Fatalf("Dial failed: %v", err)
					}
					g.AddConn(c)
				}
			}()
		}
	}()

	<-done
}

func TestStop(t *testing.T) {
	gopher.Stop()
}
