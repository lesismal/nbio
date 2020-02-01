package main

import (
	"fmt"
	"github.com/lesismal/nbio"
	"sync"
	"sync/atomic"
	"time"
)

var (
	addr = "127.0.0.1:8888"
)

func main() {
	var (
		wg        sync.WaitGroup
		bufsize   = 1024 * 8
		clientNum = 1000
		totalSend int64
		totalRecv int64
	)

	g, err := nbio.NewGopher(nbio.Config{
		NPoller: 4,
		NWorker: 8,
	})
	if err != nil {
		fmt.Printf("NewGopher failed: %v\n", err)
	}
	defer g.Stop()

	g.OnOpen(func(c *nbio.Conn) {
		c.SetLinger(1, 0)
	})
	g.OnData(func(c *nbio.Conn, data []byte) {
		atomic.AddInt64(&totalRecv, int64(len(data)))
		atomic.AddInt64(&totalSend, int64(len(data)))
		c.Write(data)
	})
	g.OnClose(func(c *nbio.Conn, err error) {
		fmt.Printf("OnClose: %v, %v\n", c.LocalAddr().String(), c.RemoteAddr().String())
	})
	err = g.Start()
	if err != nil {
		fmt.Printf("Start failed: %v\n", err)
	}

	for i := 0; i < clientNum; i++ {
		wg.Add(1)
		data := make([]byte, bufsize)
		go func() {
			c, err := nbio.Dial("tcp", addr)
			if err != nil {
				fmt.Printf("Dial failed: %v\n", err)
			}
			g.AddConn(c)
			c.Write([]byte(data))
			atomic.AddInt64(&totalSend, int64(len(data)))
		}()
	}

	go func() {
		for {
			time.Sleep(time.Second * 5)
			fmt.Println(g.State().String())
		}
	}()

	go func() {
		lstTr, lstTs := int64(0), int64(0)
		for {
			time.Sleep(time.Second)
			tr, ts := atomic.LoadInt64(&totalRecv), atomic.LoadInt64(&totalSend)
			fmt.Printf("totalRecv & totalSend: %v / %v, qps: %v / %v\n", tr, ts, tr-lstTr, ts-lstTs)
			lstTr, lstTs = tr, ts
		}
	}()

	wg.Wait()
}
