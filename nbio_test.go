package nbio

import (
	"log"
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var addr = ":8888"
var gopher *Gopher

func init() {
	addrs := []string{addr}
	g := NewGopher(Config{
		Network: "tcp",
		Addrs:   addrs,
		MaxLoad: 10,
	})
	g.maxLoad = 1024 * 100

	g.OnOpen(func(c *Conn) {
		c.SetReadDeadline(time.Now().Add(time.Second * 10))
	})
	g.OnData(func(c *Conn, data []byte) {
		c.Write(append([]byte{}, data...))
	})
	g.OnClose(func(c *Conn, err error) {})

	err := g.Start()
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

	g := NewGopher(Config{})
	err := g.Start()
	if err != nil {
		log.Fatalf("Start failed: %v\n", err)
	}
	defer g.Stop()

	g.OnOpen(func(c *Conn) {
		c.SetSession(1)
		if c.Session() != 1 {
			log.Fatalf("invalid session: %v", c.Session())
		}
		c.SetLinger(1, 0)
		c.SetNoDelay(true)
		c.SetKeepAlive(true)
		c.SetKeepAlivePeriod(time.Second * 60)
		c.SetDeadline(time.Now().Add(time.Second))
		c.SetReadBuffer(1024 * 4)
		c.SetWriteBuffer(1024 * 4)
		log.Printf("connected, local addr: %v, remote addr: %v", c.LocalAddr(), c.RemoteAddr())
	})
	g.OnData(func(c *Conn, data []byte) {
		recved := atomic.AddInt64(&total, int64(len(data)))
		if recved >= int64(clientNum*msgSize) {
			close(done)
		}
	})

	g.OnMemAlloc(func(c *Conn) []byte {
		return make([]byte, 1024)
	})
	g.OnMemFree(func(c *Conn, b []byte) {

	})

	one := func(n int) {
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
	}

	for i := 0; i < clientNum; i++ {
		if runtime.GOOS != "windows" {
			one(i)
		} else {
			go one(i)
		}
	}

	<-done
}

func Test10k(t *testing.T) {
	g := NewGopher(Config{NPoller: 2})
	err := g.Start()
	if err != nil {
		log.Fatalf("Start failed: %v\n", err)
	}
	defer g.Stop()

	var total int64 = 0
	var clientNum int64 = 1024 * 10
	var done = make(chan int)

	if runtime.GOOS == "windows" {
		clientNum = 100
	}

	t.Log("testing concurrent:", clientNum, "connections")

	g.OnOpen(func(c *Conn) {
		c.Close()
		c.Write([]byte{1})
		if atomic.AddInt64(&total, 1) == clientNum {
			close(done)
		}
	})

	one := func() {
		c, err := Dial("tcp", addr)
		if err != nil {
			log.Fatalf("Dial failed: %v", err)
		}
		g.AddConn(c)
	}
	go func() {
		for i := 0; i < int(clientNum); i++ {
			if runtime.GOOS == "linux" {
				one()
			} else {
				go one()
				// 	time.Sleep(time.Second / 1000)
			}
		}
	}()

	log.Println("== Test10k before done 111")
	<-done
	log.Println("== Test10k before done 222")
}

func TestTimeout(t *testing.T) {
	g := NewGopher(Config{})
	err := g.Start()
	if err != nil {
		log.Fatalf("Start failed: %v\n", err)
	}
	defer g.Stop()

	var done = make(chan int)
	var begin time.Time
	var timeout = time.Second / 20
	g.OnOpen(func(c *Conn) {
		begin = time.Now()
		c.SetReadDeadline(begin.Add(timeout))
	})
	g.OnClose(func(c *Conn, err error) {
		to := time.Since(begin)
		if to > timeout+time.Second/10 {
			log.Fatalf("timeout: %v, want: %v", to, timeout)
		}
		close(done)
	})

	one := func() {
		c, err := Dial("tcp", addr)
		if err != nil {
			log.Fatalf("Dial failed: %v", err)
		}
		g.AddConn(c)
	}
	one()

	<-done
}

func TestHeapTimer(t *testing.T) {
	g := NewGopher(Config{})
	g.Start()
	defer g.Stop()

	timeout := time.Second / 20

	t1 := time.Now()
	ch1 := make(chan int)
	g.afterFunc(timeout, func() {
		close(ch1)
	})
	<-ch1
	to1 := time.Since(t1)
	if to1 < timeout-timeout/5 || to1 > timeout+timeout/5 {
		log.Fatalf("invalid to1: %v", to1)
	}

	t2 := time.Now()
	ch2 := make(chan int)
	it2 := g.afterFunc(timeout, func() {
		close(ch2)
	})
	it2.Reset(timeout * 5)
	<-ch2
	to2 := time.Since(t2)
	if to2 < timeout*4 || to2 > timeout*6 {
		log.Fatalf("invalid to2: %v", to2)
	}

	ch3 := make(chan int)
	it3 := g.afterFunc(timeout, func() {
		close(ch3)
	})
	it3.Stop()
	<-time.After(timeout + timeout/4)
	select {
	case <-ch3:
		log.Fatalf("stop failed")
	default:
	}

	ch4 := make(chan int, 5)
	for i := 0; i < 5; i++ {
		n := i + 1
		if n == 3 {
			n = 5
		} else if n == 5 {
			n = 3
		}

		g.afterFunc(timeout*time.Duration(n), func() {
			ch4 <- n
		})
	}

	g.afterFunc(timeout, func() {
		panic("test")
	})

	for i := 0; i < 5; i++ {
		n := <-ch4
		if n != i+1 {
			log.Fatalf("invalid n: %v, %v", i, n)
		}
	}

	its := make([]*htimer, 100)[0:0]
	ch5 := make(chan int, 100)
	for i := 0; i < 100; i++ {
		n := 500 + rand.Int()%200
		to := time.Duration(n) * time.Second / 1000
		its = append(its, g.afterFunc(to, func() {
			ch5 <- n
		}))
	}
	if len(its) != 100 || g.timers.Len() != 100 {
		log.Fatalf("invalid timers length: %v, %v", len(its), g.timers.Len())
	}
	for i := 0; i < 50; i++ {
		if its[0] == nil {
			log.Fatalf("invalid its[0]")
		}
		its[0].Stop()
		its = its[1:]
	}
	if len(its) != 50 || g.timers.Len() != 50 {
		log.Fatalf("invalid timers length: %v, %v", len(its), g.timers.Len())
	}
	recved := 0
LOOP_RECV:
	for {
		select {
		case <-ch5:
			recved++
		case <-time.After(time.Second):
			break LOOP_RECV
		}
	}
	if recved != 50 {
		log.Fatalf("invalid recved num: %v", recved)
	}

	it := &htimer{parent: g, index: -1}
	it.Stop()
}

func TestFuzz(t *testing.T) {
	gopher.maxLoad = 10
	wg := sync.WaitGroup{}
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			Dial("tcp4", addr)
		}()
	}
	c, err := Dial("tcp6", addr)
	if err == nil {
		log.Printf("Dial tcp6: %v, %v, %v", c.LocalAddr(), c.RemoteAddr(), err)
		gopher.AddConn(c)
		c.SetWriteDeadline(time.Now().Add(time.Second))
		c.Write([]byte{1})
		c.Close()
		c.Write([]byte{1})
		bs := [][]byte{}
		bs = append(bs, []byte{1})
		c.Writev(bs)
	} else {
		log.Printf("Dial tcp6: %v", err)
	}

	g := NewGopher(Config{
		Network: "tcp4",
		Addrs:   []string{"localhost:8889", "localhost:8889"},
	})
	g.Start()

	wg.Wait()
}

func TestStop(t *testing.T) {
	log.Println(gopher.State().String())
	gopher.Stop()
}
