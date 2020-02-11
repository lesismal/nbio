package main

import (
	"fmt"
	"github.com/lesismal/nbio"
	"sync"
	"sync/atomic"
	"time"
)

var (
	addrs = []string{"127.0.0.1:8888", "127.0.0.1:9999"}
)

func main() {
	var (
		wg         sync.WaitGroup
		qps        int64
		bufsize    = 1024 * 8
		clientNum  = 1024
		totalRead  int64
		totalWrite int64
	)

	g, err := nbio.NewGopher(nbio.Config{})
	if err != nil {
		fmt.Printf("NewGopher failed: %v\n", err)
	}
	defer g.Stop()

	g.OnOpen(func(c *nbio.Conn) {
		c.SetLinger(1, 0)
	})
	g.OnData(func(c *nbio.Conn, data []byte) {
		atomic.AddInt64(&qps, 1)
		atomic.AddInt64(&totalRead, int64(len(data)))
		atomic.AddInt64(&totalWrite, int64(len(data)))
		c.Write(append([]byte(nil), data...))
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
		idx := i
		data := make([]byte, bufsize)
		go func() {
			c, err := nbio.Dial("tcp", addrs[idx%2])
			if err != nil {
				fmt.Printf("Dial failed: %v\n", err)
			}
			g.AddConn(c)
			c.Write([]byte(data))
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
			fmt.Printf("qps: %v, total read: %.1f M, total write: %.1f M\n", atomic.SwapInt64(&qps, 0), float64(atomic.SwapInt64(&totalRead, 0))/1024/1024, float64(atomic.SwapInt64(&totalWrite, 0))/1024/1024)
		}
	}()

	wg.Wait()
}
