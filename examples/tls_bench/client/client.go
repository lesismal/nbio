package main

import (
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lesismal/llib/bytes"
	ltls "github.com/lesismal/llib/std/crypto/tls"
	"github.com/lesismal/nbio"
	"github.com/lesismal/nbio/extension/tls"
)

var (
	addr = "localhost:8888"

	configs = []*tls.Config{
		// sth wrong with TLS 1.0
		// {
		// 	InsecureSkipVerify: true,
		// 	MaxVersion:         ltls.VersionTLS10,
		// },
		{
			InsecureSkipVerify: true,
			MaxVersion:         ltls.VersionTLS11,
		},
		{
			InsecureSkipVerify: true,
			MaxVersion:         ltls.VersionTLS12,
		},
		{
			InsecureSkipVerify: true,
			MaxVersion:         ltls.VersionTLS13,
		},
		// SSL is not supported
		// {
		// 	InsecureSkipVerify: true,
		// 	MaxVersion:         ltls.VersionSSL30,
		// },
	}
)

// Session .
type Session struct {
	Conn   *tls.Conn
	Buffer *bytes.Buffer
}

// wrapData .
func wrapData(h func(c *nbio.Conn, tlsConn *tls.Conn, data []byte)) func(c *nbio.Conn, data []byte) {
	return func(c *nbio.Conn, data []byte) {

		if isession := c.Session(); isession != nil {
			if session, ok := isession.(*Session); ok {
				session.Conn.Append(data)
				buffer := make([]byte, 2048)
				for {
					n, err := session.Conn.Read(buffer)
					if err != nil {
						c.Close()
						return
					}
					if h != nil && n > 0 {
						h(c, session.Conn, buffer[:n])
					}
					if n < len(buffer) {
						return
					}
				}
			}
		}
	}
}

func main() {
	var (
		wg         sync.WaitGroup
		qps        int64
		bufsize    = 64 //1024 * 8
		clientNum  = 128
		totalRead  int64
		totalWrite int64
	)

	g := nbio.NewGopher(nbio.Config{})
	g.OnData(wrapData(func(c *nbio.Conn, tlsConn *tls.Conn, data []byte) {
		session := c.Session().(*Session)
		session.Buffer.Push(data)
		for session.Buffer.Len() >= bufsize {
			buf, _ := session.Buffer.Pop(bufsize)
			tlsConn.Write(buf)
			atomic.AddInt64(&qps, 1)
			atomic.AddInt64(&totalRead, int64(bufsize))
			atomic.AddInt64(&totalWrite, int64(bufsize))
		}
	}))

	err := g.Start()
	if err != nil {
		fmt.Printf("Start failed: %v\n", err)
	}
	defer g.Stop()

	for i := 0; i < clientNum; i++ {
		wg.Add(1)

		tlsConfig := configs[i%len(configs)]
		go func() {
			data := make([]byte, bufsize)

			tlsConn, err := tls.Dial("tcp", addr, tlsConfig)
			if err != nil {
				log.Fatalf("Dial failed: %v\n", err)
			}

			nbConn, err := nbio.NBConn(tlsConn.Conn())
			if err != nil {
				log.Fatalf("AddConn failed: %v\n", err)
			}

			nbConn.SetSession(&Session{
				Conn:   tlsConn,
				Buffer: bytes.NewBuffer(),
			})
			nonBlock := true
			tlsConn.ResetConn(nbConn, nonBlock)
			g.AddConn(nbConn)

			tlsConn.Write(data)
			atomic.AddInt64(&totalWrite, int64(len(data)))
		}()
	}

	go func() {
		for {
			time.Sleep(time.Second)
			fmt.Printf("nbio tls clients, qps: %v, total read: %.1f M, total write: %.1f M\n", atomic.SwapInt64(&qps, 0), float64(atomic.SwapInt64(&totalRead, 0))/1024/1024, float64(atomic.SwapInt64(&totalWrite, 0))/1024/1024)
		}
	}()

	wg.Wait()
}
