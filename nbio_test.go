package nbio

import (
	"io"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var addr = "127.0.0.1:8888"
var testfile = "test_tmp.file"
var gopher *Gopher

func init() {
	if err := ioutil.WriteFile(testfile, make([]byte, 1024*100), 0666); err != nil {
		log.Panicf("write file failed: %v", err)
	}

	addrs := []string{addr}
	g := NewGopher(Config{
		Network: "tcp",
		Addrs:   addrs,
	})

	g.OnOpen(func(c *Conn) {
		c.SetReadDeadline(time.Now().Add(time.Second * 10))
	})
	g.OnData(func(c *Conn, data []byte) {
		if len(data) == 8 && string(data) == "sendfile" {
			fd, err := os.Open(testfile)
			if err != nil {
				log.Panicf("open file failed: %v", err)
			}

			if _, err = c.Sendfile(fd, 0); err != nil {
				panic(err)
			}

			if err := fd.Close(); err != nil {
				log.Panicf("close file failed: %v", err)
			}
		} else {
			c.Write(append([]byte{}, data...))
		}
	})
	g.OnClose(func(c *Conn, err error) {})

	err := g.Start()
	if err != nil {
		log.Panicf("Start failed: %v\n", err)
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
		log.Panicf("Start failed: %v\n", err)
	}
	defer g.Stop()

	g.OnOpen(func(c *Conn) {
		c.SetSession(1)
		if c.Session() != 1 {
			log.Panicf("invalid session: %v", c.Session())
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

	g.OnReadBufferAlloc(func(c *Conn) []byte {
		return make([]byte, 1024)
	})
	g.OnReadBufferFree(func(c *Conn, b []byte) {

	})

	one := func(n int) {
		var c net.Conn
		var err error
		if n%2 == 0 {
			c, err = Dial("tcp", addr)
			if err != nil {
				log.Panicf("Dial failed: %v", err)
			}
		} else {
			c, err = net.Dial("tcp", addr)
			if err != nil {
				log.Panicf("net.Dial failed: %v", err)
			}
		}
		g.AddConn(c)
		if n%2 == 0 {
			c.(*Conn).Writev([][]byte{make([]byte, msgSize)})
		} else {
			c.Write(make([]byte, msgSize))
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

func TestSendfile(t *testing.T) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		panic(err)
	}

	buf := make([]byte, 1024*100)

	for i := 0; i < 3; i++ {
		if _, err := conn.Write([]byte("sendfile")); err != nil {
			log.Panicf("write 'sendfile' failed: %v", err)
		}

		if _, err := io.ReadFull(conn, buf); err != nil {
			log.Panicf("read file failed: %v", err)
		}
	}
}

func TestTimeout(t *testing.T) {
	g := NewGopher(Config{})
	err := g.Start()
	if err != nil {
		log.Panicf("Start failed: %v\n", err)
	}
	defer g.Stop()

	var done = make(chan int)
	var begin time.Time
	var timeout = time.Second
	g.OnOpen(func(c *Conn) {
		begin = time.Now()
		c.SetReadDeadline(begin.Add(timeout))
	})
	g.OnClose(func(c *Conn, err error) {
		to := time.Since(begin)
		if to > timeout*2 {
			log.Panicf("timeout: %v, want: %v", to, timeout)
		}
		close(done)
	})

	one := func() {
		c, err := DialTimeout("tcp", addr, time.Second)
		if err != nil {
			log.Panicf("Dial failed: %v", err)
		}
		g.AddConn(c)
	}
	one()

	<-done
}

func TestFuzz(t *testing.T) {
	wg := sync.WaitGroup{}
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if idx%2 == 0 {
				Dial("tcp4", addr)
			} else {
				Dial("tcp4", addr)
			}
		}(i)
	}

	wg.Wait()

	readed := 0
	wg2 := sync.WaitGroup{}
	wg2.Add(1)
	g := NewGopher(Config{NPoller: 1})
	g.OnData(func(c *Conn, data []byte) {
		readed += len(data)
		if readed == 4 {
			wg2.Done()
		}
	})
	err := g.Start()
	if err != nil {
		log.Panicf("Start failed: %v", err)
	}

	c, err := Dial("tcp", addr)
	if err == nil {
		log.Printf("Dial tcp4: %v, %v, %v", c.LocalAddr(), c.RemoteAddr(), err)
		g.AddConn(c)
		c.SetWriteDeadline(time.Now().Add(time.Second))
		c.Write([]byte{1})

		time.Sleep(time.Second / 10)

		bs := [][]byte{}
		bs = append(bs, []byte{2})
		bs = append(bs, []byte{3})
		bs = append(bs, []byte{4})
		c.Writev(bs)

		time.Sleep(time.Second / 10)

		c.Close()
		c.Write([]byte{1})
	} else {
		log.Panicf("Dial tcp4: %v", err)
	}

	gErr := NewGopher(Config{
		Network: "tcp4",
		Addrs:   []string{"127.0.0.1:8889", "127.0.0.1:8889"},
	})
	gErr.Start()
}

func TestHeapTimer(t *testing.T) {
	g := NewGopher(Config{})
	g.Start()
	defer g.Stop()

	timeout := time.Second / 10

	testHeapTimerNormal(g, t, timeout)
	testHeapTimerExecPanic(g, t, timeout)
	testHeapTimerNormalExecMany(g, t, timeout)
	testHeapTimerExecManyRandtime(g, t, timeout)
}

func testHeapTimerNormal(g *Gopher, t *testing.T, timeout time.Duration) {
	t1 := time.Now()
	ch1 := make(chan int)
	g.AfterFunc(timeout*5, func() {
		close(ch1)
	})
	<-ch1
	to1 := time.Since(t1)
	if to1 < timeout*4 || to1 > timeout*10 {
		log.Panicf("invalid to1: %v", to1)
	}

	t2 := time.Now()
	ch2 := make(chan int)
	it2 := g.afterFunc(timeout, func() {
		close(ch2)
	})
	it2.Reset(timeout * 5)
	<-ch2
	to2 := time.Since(t2)
	if to2 < timeout*4 || to2 > timeout*10 {
		log.Panicf("invalid to2: %v", to2)
	}

	ch3 := make(chan int)
	it3 := g.afterFunc(timeout, func() {
		close(ch3)
	})
	it3.Stop()
	<-g.After(timeout * 2)
	select {
	case <-ch3:
		log.Panicf("stop failed")
	default:
	}
}

func testHeapTimerExecPanic(g *Gopher, t *testing.T, timeout time.Duration) {
	g.afterFunc(timeout, func() {
		panic("test")
	})
}

func testHeapTimerNormalExecMany(g *Gopher, t *testing.T, timeout time.Duration) {
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

	for i := 0; i < 5; i++ {
		n := <-ch4
		if n != i+1 {
			log.Panicf("invalid n: %v, %v", i, n)
		}
	}
}

func testHeapTimerExecManyRandtime(g *Gopher, t *testing.T, timeout time.Duration) {
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
		log.Panicf("invalid timers length: %v, %v", len(its), g.timers.Len())
	}
	for i := 0; i < 50; i++ {
		if its[0] == nil {
			log.Panicf("invalid its[0]")
		}
		its[0].Stop()
		its = its[1:]
	}
	if len(its) != 50 || g.timers.Len() != 50 {
		log.Panicf("invalid timers length: %v, %v", len(its), g.timers.Len())
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
		log.Panicf("invalid recved num: %v", recved)
	}

	it := &htimer{parent: g, index: -1}
	it.Stop()
}

func TestStop(t *testing.T) {
	gopher.Stop()
	os.Remove(testfile)
}
