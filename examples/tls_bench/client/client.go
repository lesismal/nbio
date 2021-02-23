package main

import (
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	ltls "github.com/lesismal/llib/std/crypto/tls"
	"github.com/lesismal/nbio"
	"github.com/lesismal/nbio/extension/tls"
)

var (
	addr = "localhost:8888"

	tlsConfigs = []*tls.Config{
		&tls.Config{
			InsecureSkipVerify: true,
			MaxVersion:         ltls.VersionTLS10,
		},
		&tls.Config{
			InsecureSkipVerify: true,
			MaxVersion:         ltls.VersionTLS11,
		},
		&tls.Config{
			InsecureSkipVerify: true,
			MaxVersion:         ltls.VersionTLS12,
		},
		&tls.Config{
			InsecureSkipVerify: true,
			MaxVersion:         ltls.VersionTLS13,
		},
		// SSL is not supported
		// &tls.Config{
		// 	InsecureSkipVerify: true,
		// 	MaxVersion:         ltls.VersionSSL30,
		// },
	}
)

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
	g.OnData(tls.WrapData(func(c *nbio.Conn, tlsConn *tls.Conn, data []byte) {
		atomic.AddInt64(&qps, 1)
		atomic.AddInt64(&totalRead, int64(len(data)))
		atomic.AddInt64(&totalWrite, int64(len(data)))
		tlsConn.Write(append([]byte{}, data...))
	}))

	err := g.Start()
	if err != nil {
		fmt.Printf("Start failed: %v\n", err)
	}
	defer g.Stop()

	for i := 0; i < clientNum; i++ {
		wg.Add(1)

		tlsConfig := tlsConfigs[i%len(tlsConfigs)]
		go func() {
			data := make([]byte, bufsize)

			tlsConn, err := tls.Dial("tcp", addr, tlsConfig)
			if err != nil {
				log.Fatalf("Dial failed: %v\n", err)
			}

			nbConn, err := g.Conn(tlsConn.Conn())
			if err != nil {
				log.Fatalf("AddConn failed: %v\n", err)
			}

			nbConn.SetSession(tlsConn)
			tlsConn.ResetConn(nbConn, 8192)
			g.AddConn(nbConn)

			tlsConn.Write(data)
			atomic.AddInt64(&totalWrite, int64(len(data)))
		}()
	}

	go func() {
		for {
			time.Sleep(time.Second * 5)
			fmt.Println(g.State().String())
		}
	}()

	go func() {
		for {
			time.Sleep(time.Second)
			fmt.Printf("nbio tls clients, qps: %v, total read: %.1f M, total write: %.1f M\n", atomic.SwapInt64(&qps, 0), float64(atomic.SwapInt64(&totalRead, 0))/1024/1024, float64(atomic.SwapInt64(&totalWrite, 0))/1024/1024)
		}
	}()

	wg.Wait()
}
