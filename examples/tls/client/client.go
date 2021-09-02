package main

import (
	"bytes"
	"fmt"
	"log"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/lesismal/nbio"
	"github.com/lesismal/nbio/extension/tls"
)

var (
	qps   int64 = 0
	total int64 = 0

	tlsConfig = &tls.Config{
		InsecureSkipVerify: true,
	}
)

func main() {
	var (
		wbuf = []byte("hello world")
		addr = "localhost:8888"
	)

	g := nbio.NewGopher(nbio.Config{})

	isClient := true
	g.OnOpen(tls.WrapOpen(tlsConfig, isClient, func(c *nbio.Conn, tlsConn *tls.Conn) {
		log.Println("OnOpen:", c.RemoteAddr().String())
		// tlsConn.Write(wbuf)
	}))
	g.OnClose(tls.WrapClose(func(c *nbio.Conn, tlsConn *tls.Conn, err error) {
		log.Println("OnClose:", c.RemoteAddr().String())
	}))
	g.OnData(tls.WrapData(func(c *nbio.Conn, tlsConn *tls.Conn, data []byte) {
		if bytes.Equal(wbuf, data) {
			tlsConn.Write(wbuf)
			atomic.AddInt64(&qps, 1)
		} else {
			c.Close()
		}
	}))

	err := g.Start()
	if err != nil {
		fmt.Printf("Start failed: %v\n", err)
	}
	defer g.Stop()

	for i := 0; i < 1; i++ {
		func() {
			// step 1: make a tls.Conn by tls.Dial
			tlsConn, err := tls.Dial("tcp", addr, tlsConfig)
			if err != nil {
				log.Fatalf("Dial failed: %v\n", err)
			}
			// step 2:
			// add tls.Conn.conn to gopher, and get the nbio.Conn. the new nbio.Conn is non-blocking
			nbConn, err := nbio.NBConn(tlsConn.Conn())
			if err != nil {
				log.Fatalf("AddConn failed: %v\n", err)
			}
			// step 3: set tls.Conn and nbio.Conn to each other, and add nbio.Conn to the gopher
			isNonblock := true
			nbConn.SetSession(tlsConn)
			tlsConn.ResetConn(nbConn, isNonblock)
			g.AddConn(nbConn)

			// step 4: write data here or in the OnOpen handler or anywhere
			tlsConn.Write(wbuf)
		}()
	}

	ticker := time.NewTicker(time.Second)
	for i := 1; true; i++ {
		<-ticker.C
		nSuccess := atomic.SwapInt64(&qps, 0)
		total += nSuccess
		fmt.Printf("running for %v seconds, NumGoroutine: %v, success: %v, totalSuccess: %v\n", i, runtime.NumGoroutine(), nSuccess, total)
	}
}
