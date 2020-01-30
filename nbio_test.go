package nbio

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var addr = "127.0.0.1:8888"
var g *Gopher
var testQps int64 = 100000

func init() {
	g = echoServer(1024 * 64)
	go http.ListenAndServe(":6060", nil)
}

func TestHuge1V100M(t *testing.T) {
	testHuge(t, 1, 1024*1024*100)
}

func TestClient(t *testing.T) {
	testClient(t, 1024)
}

func Test1k16B(t *testing.T) {
	testEcho(t, 1024, testQps, 16)
}

func Test1k64B(t *testing.T) {
	testEcho(t, 1024, testQps, 64)
}

func Test1k128B(t *testing.T) {
	testEcho(t, 1024, testQps, 128)
}

func Test1k1k(t *testing.T) {
	testEcho(t, 1024, testQps, 1024)
}

func Test1k2k(t *testing.T) {
	testEcho(t, 1024, testQps, 1024*2)
}

func Test1k4k(t *testing.T) {
	testEcho(t, 1024, testQps, 1024*4)
}

func Test1k8k(t *testing.T) {
	testEcho(t, 1024, testQps, 1024*8)
}

func Test2k16B(t *testing.T) {
	testEcho(t, 1024*2, testQps, 16)
}

func Test2k64B(t *testing.T) {
	testEcho(t, 1024*2, testQps, 64)
}

func Test2k128B(t *testing.T) {
	testEcho(t, 1024*2, testQps, 128)
}

func Test2k1k(t *testing.T) {
	testEcho(t, 1024*2, testQps, 1024)
}

func Test2k2k(t *testing.T) {
	testEcho(t, 1024*2, testQps, 1024*2)
}

func Test2k4k(t *testing.T) {
	testEcho(t, 1024*2, testQps, 1024*4)
}

func Test2k8k(t *testing.T) {
	testEcho(t, 1024*2, testQps, 1024*8)
}

func BenchmarkEcho4K(b *testing.B) {
	benchmarkEcho(b, 1024*4)
}

func BenchmarkEcho8K(b *testing.B) {
	benchmarkEcho(b, 1024*8)
}

func Test10k(t *testing.T) {
	test10k(t, 10240, 1024)
}

func test10k(t *testing.T, par int, msgsize int) {
	t.Log("testing concurrent:", par, "connections")

	addr := "127.0.0.1:18888"

	g, err := NewGopher(Config{
		Network:        "tcp",
		Address:        addr,
		NPoller:        1,
		NWorker:        1,
		QueueSize:      1024,
		BufferSize:     1024,
		BufferNum:      1024 * 2,
		PollInterval:   time.Millisecond * 200,
		MaxTimeout:     time.Second * 10,
		MaxWriteBuffer: 1024 * 1024 * 100,
	})
	if err != nil {
		log.Fatalf("NewGopher failed: %v\n", err)
	}

	total := 0
	die := make(chan struct{})

	g.OnData(func(c *Conn, data []byte) {
		total += len(data)
		if total >= par*msgsize {
			t.Logf("total: %v", total)
			close(die)
		}
	})

	err = g.Start()
	if err != nil {
		log.Fatalf("Start failed: %v\n", err)
	}

	go func() {
		for i := 0; i < par; i++ {
			data := make([]byte, msgsize)
			c, err := Dial("tcp", addr)
			if err != nil {
				t.Fatalf("Dial failed: %v", err)
			}
			g.AddConn(c)
			c.Write(data)
		}
	}()
	<-die
}

func echoServer(bufsize int) *Gopher {
	g, err := NewGopher(Config{
		Network:        "tcp",
		Address:        addr,
		NPoller:        1,
		NWorker:        1,
		QueueSize:      1024,
		BufferSize:     uint32(bufsize),
		BufferNum:      1024 * 2,
		PollInterval:   time.Millisecond * 200,
		MaxTimeout:     time.Second * 10,
		MaxWriteBuffer: 1024 * 1024 * 100,
	})
	if err != nil {
		log.Fatalf("NewGopher failed: %v\n", err)
	}

	g.OnOpen(func(c *Conn) {
		c.SetLinger(1, 0)
	})
	g.OnData(func(c *Conn, data []byte) {
		dataCopy := append([]byte{}, data...)
		c.Write(dataCopy)
	})

	err = g.Start()
	if err != nil {
		log.Fatalf("Start failed: %v\n", err)
	}

	return g
}

func testEcho(t *testing.T, clientNum int, total int64, bufsize int) {
	var (
		qps int64
		wg  sync.WaitGroup
	)
	for i := 0; i < clientNum; i++ {

		wg.Add(1)
		go func() {
			defer wg.Done()
			data := make([]byte, bufsize)
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				t.Log(err)
				return
			}
			defer conn.Close()

			for {
				n, err := conn.Write(data)
				if err != nil || n < bufsize {
					t.Logf("Write failed: %v, %v", err, n)
					break
				}
				n, err = io.ReadFull(conn, data)
				if err != nil {
					t.Logf("Read failed: %v, %v", err, n)
					break
				}
				if atomic.AddInt64(&qps, 1) >= total {
					return
				}
			}
		}()
	}

	wg.Wait()

	if atomic.LoadInt64(&qps) < total {
		t.Fatalf("test %v %v failed, qps: %v", clientNum, bufsize, atomic.LoadInt64(&qps))
	}
}

func testClient(t *testing.T, clientNum int) {
	wg := sync.WaitGroup{}

	g, err := NewGopher(Config{
		NPoller: 1,
		NWorker: 1,
	})
	if err != nil {
		log.Fatalf("NewGopher failed: %v\n", err)
	}
	defer g.Stop()

	g.OnOpen(func(c *Conn) {
		c.SetLinger(1, 0)
	})
	g.OnData(func(c *Conn, data []byte) {
		c.Close()
	})
	g.OnClose(func(c *Conn, err error) {
		wg.Done()
	})
	err = g.Start()
	if err != nil {
		log.Fatalf("Start failed: %v\n", err)
	}

	for i := 0; i < clientNum; i++ {
		idx := i
		wg.Add(1)
		go func() {
			c, err := Dial("tcp", addr)
			if err != nil {
				t.Fatalf("Dial failed: %v", err)
			}
			g.AddConn(c)
			c.Write([]byte(fmt.Sprintf("client %v", idx)))
		}()
	}

	wg.Wait()
}

func testHuge(t *testing.T, clientNum int, bufsize int) {
	var (
		wg sync.WaitGroup
	)
	for i := 0; i < clientNum; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wx := make([]byte, bufsize)
			rx := make([]byte, bufsize)
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				t.Log(err)
				return
			}
			defer conn.Close()

			go func() {
				n, err := conn.Write(wx)
				if err != nil || n < bufsize {
					t.Logf("Write failed: %v, %v", err, n)
				}
			}()

			n, err := io.ReadFull(conn, rx)
			t.Log("pong size:", n)
			if err != nil {
				t.Logf("Read failed: %v", err)
			}

			if !bytes.Equal(wx, rx) {
				t.Fatal("incorrect receiving")
			}
			t.Log("bytes compare successful")
		}()
	}

	wg.Wait()
}

func benchmarkEcho(b *testing.B, bufsize int) {
	data := make([]byte, bufsize)
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		b.Fatal(err)
	}
	defer conn.Close()

	for i := 0; i < b.N; i++ {
		n, err := conn.Write(data)
		if err != nil || n < bufsize {
			b.Fatalf("Write failed: %v, %v", err, n)
		}

		n, err = io.ReadFull(conn, data)
		if err != nil {
			b.Fatalf("Read failed: %v, %v", err, n)
		}
	}
}
