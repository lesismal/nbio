package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

var qps int64
var totalRead int64
var totalWrite int64
var port = "127.0.0.1:8888"

func main() {
	wg := &sync.WaitGroup{}
	one := func(i int) {
		defer wg.Done()
		conn, err := net.Dial("tcp", port)
		if err != nil {
			fmt.Println("dial failed:", err)
			os.Exit(1)
		}

		defer conn.Close()

		str := fmt.Sprintf("hello_%v", i)
		data := make([]byte, 8000)
		copy(data, []byte(str))
		for {
			data = data[:]
			n, err := conn.Write(data)
			if err != nil {
				fmt.Println("write 111 failed:", n, err)
				return
			}

			if n > 0 {
				atomic.AddInt64(&totalRead, int64(n))
			}
			conn.SetReadDeadline(time.Now().Add(time.Second * 50))
			n, err = io.ReadFull(conn, data) // conn.Read(data)

			if n > 0 {
				atomic.AddInt64(&totalWrite, int64(n))
			}

			if err != nil {
				fmt.Println("read 222 failed:", n, err)
				return
			}

			atomic.AddInt64(&qps, 1)
		}
	}

	loop := 2000
	for i := 0; i < loop; i++ {
		wg.Add(1)
		go one(i)
	}

	go func() {
		for {
			time.Sleep(time.Second)
			fmt.Println("qps:", atomic.SwapInt64(&qps, 0), atomic.SwapInt64(&totalRead, 0), atomic.SwapInt64(&totalWrite, 0))
		}
	}()

	wg.Wait()
}
