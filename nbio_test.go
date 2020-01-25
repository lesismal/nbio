package nbio

import (
	"io"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"testing"
	"time"
)

var addr = "127.0.0.1:8888"
var g *Gopher

func init() {
	g = echoServer(1024)
	go http.ListenAndServe(":6060", nil)
}

func Test1k(t *testing.T) {
	testEcho(t, 1024, 1024)
}
func Test2k(t *testing.T) {
	testEcho(t, 2048, 1024)
}

func Test4k(t *testing.T) {
	testEcho(t, 4096, 1024)
}

func Test8k(t *testing.T) {
	testEcho(t, 8192, 1024)
}

func Test10k(t *testing.T) {
	testEcho(t, 10240, 1024)
}

func Test12k(t *testing.T) {
	testEcho(t, 12288, 1024)
}

func Test1kTiny(t *testing.T) {
	testEcho(t, 1024, 16)
}

func Test2kTiny(t *testing.T) {
	testEcho(t, 2048, 16)
}

func Test4kTiny(t *testing.T) {
	testEcho(t, 4096, 16)
}

func BenchmarkEcho4K(b *testing.B) {
	benchmarkEcho(b, 4096)
}

func BenchmarkEcho64K(b *testing.B) {
	benchmarkEcho(b, 65536)
}

func BenchmarkEcho128K(b *testing.B) {
	benchmarkEcho(b, 128*1024)
}

func echoServer(bufsize int) *Gopher {
	g, err := NewGopher(Config{

		Network:      "tcp",
		Address:      addr,
		NPoller:      2,
		NWorker:      4,
		QueueSize:    1024,
		BufferSize:   uint32(bufsize),
		BufferNum:    1024 * 2,
		PollInterval: 200,    //ms
		MaxTimeout:   120000, //ms
	})
	if err != nil {
		log.Fatalf("NewGopher failed: %v\n", err)
	}

	g.OnOpen(func(c *Conn) {
		c.SetsockoptLinger(1, 0)
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

func testEcho(t *testing.T, clientNum int, bufsize int) {
	for i := 0; i < clientNum; i++ {
		func() {
			data := make([]byte, bufsize)
			conn, err := net.DialTimeout("tcp", addr, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()

			conn.SetWriteDeadline(time.Now().Add(time.Second * 5))
			n, err := conn.Write(data)
			if err != nil || n < bufsize {
				t.Fatalf("Write failed: %v, %v", err, n)
			}
			conn.SetReadDeadline(time.Now().Add(time.Second * 5))
			n, err = io.ReadFull(conn, data)
			if err != nil {
				t.Fatalf("Read failed: %v, %v", err, n)
			}
		}()
	}
}

func benchmarkEcho(b *testing.B, bufsize int) {
	data := make([]byte, bufsize)
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		b.Fatal(err)
	}
	defer conn.Close()

	for j := 0; j < b.N; j++ {
		// send
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
