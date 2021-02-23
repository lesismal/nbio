package main

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	wg := sync.WaitGroup{}

	count := int64(0)
	total := count
	connNum := 10
	loopTimes := 10

	configs := []*tls.Config{
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
		// {
		// 	InsecureSkipVerify: true,
		// 	MaxVersion:         tls.VersionSSL30,
		// },
	}
	for i := 0; i < connNum; i++ {
		tlsConfig := configs[i%len(configs)]
		wg.Add(1)
		go func() {
			defer wg.Done()

			conn, err := tls.Dial("tcp", "localhost:8888", tlsConfig)
			if err != nil {
				panic(err)
			}
			defer conn.Close()

			randBuf := make([]byte, 64)
			rand.Read(randBuf)
			wbuf := []byte(hex.EncodeToString(randBuf))
			// log.Println("wbuf:", string(wbuf))
			for j := 0; j < loopTimes; j++ {
				n1, err := conn.Write(wbuf)
				if err != nil || n1 != len(wbuf) {
					log.Fatalf("conn.Write failed: %v, %v", n1, err)
				}

				rbuf := make([]byte, len(wbuf))
				n2, err := io.ReadFull(conn, rbuf)
				if err != nil {
					log.Fatalf("conn.Read failed: %v", err)
				}
				if n2 != n1 || string(rbuf) != string(wbuf) {
					log.Fatalf("conn.Read failed: %v, %v", n2, string(wbuf))
				} else {
					atomic.AddInt64(&count, 1)
					// log.Println("response:", string(rbuf))
				}
				time.Sleep(time.Second / 10)
			}
		}()
	}

	ticker := time.NewTicker(time.Second)
	for {
		<-ticker.C
		curr := atomic.SwapInt64(&count, 0)
		total += curr
		log.Println("request count:", curr, "total:", total)
		if total >= int64(connNum*loopTimes) {
			break
		}
	}
}
