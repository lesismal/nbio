package main

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

var (
	addr = "localhost:8888"

	tlsConfig = &tls.Config{
		InsecureSkipVerify: true,
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

	tlsConfigs := []*tls.Config{
		{
			InsecureSkipVerify: true,
			MaxVersion:         tls.VersionTLS10,
		},
		{
			InsecureSkipVerify: true,
			MaxVersion:         tls.VersionTLS11,
		},
		{
			InsecureSkipVerify: true,
			MaxVersion:         tls.VersionTLS12,
		},
		{
			InsecureSkipVerify: true,
			MaxVersion:         tls.VersionTLS13,
		},
		// SSL is not supported
		// &{
		// 	InsecureSkipVerify: true,
		// 	MaxVersion:         tls.VersionSSL30,
		// },
	}

	for i := 0; i < clientNum; i++ {
		wg.Add(1)

		tlsConfig := tlsConfigs[i%len(tlsConfigs)]
		go func() {
			wbuf := make([]byte, bufsize)
			rbuf := make([]byte, len(wbuf))
			conn, err := tls.Dial("tcp", "localhost:8888", tlsConfig)
			if err != nil {
				panic(err)
			}
			defer conn.Close()

			for {
				rand.Read(wbuf)
				n1, err := conn.Write(wbuf)
				if err != nil || n1 != len(wbuf) {
					log.Fatalf("conn.Write failed: %v, %v", n1, err)
				}

				n2, err := io.ReadFull(conn, rbuf)
				if err != nil || n2 != n1 || !bytes.Equal(wbuf, rbuf) {
					log.Fatalf("conn.Read failed: %v", err)
				}
				atomic.AddInt64(&qps, 1)
				atomic.AddInt64(&totalRead, int64(len(rbuf)))
				atomic.AddInt64(&totalWrite, int64(len(wbuf)))
			}
		}()
	}

	go func() {
		for {
			time.Sleep(time.Second)
			fmt.Printf("std/tls clients, qps: %v, total read: %.1f M, total write: %.1f M\n", atomic.SwapInt64(&qps, 0), float64(atomic.SwapInt64(&totalRead, 0))/1024/1024, float64(atomic.SwapInt64(&totalWrite, 0))/1024/1024)
		}
	}()

	wg.Wait()
}
