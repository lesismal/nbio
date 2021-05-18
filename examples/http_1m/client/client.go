package main

import (
	"flag"
	"fmt"
	"net"
	"runtime"
	"sync/atomic"
	"time"
)

var (
	connected    uint64 = 0
	success      uint64 = 0
	failed       uint64 = 0
	totalSuccess uint64 = 0
	totalFailed  uint64 = 0

	numClient    = flag.Int("c", 500000, "client num")
	numGoroutine = flag.Int("g", 1000, "goroutine num")
)

func main() {
	flag.Parse()

	connNum := *numClient
	goroutineNum := *numGoroutine

	for i := 0; i < goroutineNum; i++ {
		go loop(addrs[i%len(addrs)], connNum/goroutineNum)
	}

	ticker := time.NewTicker(time.Second)
	for i := 1; true; i++ {
		<-ticker.C
		nSuccess := atomic.SwapUint64(&success, 0)
		nFailed := atomic.SwapUint64(&failed, 0)
		totalSuccess += nSuccess
		totalFailed += nFailed
		fmt.Printf("running for %v seconds, online: %v, NumGoroutine: %v, success: %v, totalSuccess: %v, failed: %v, totalFailed: %v\n", i, connected, runtime.NumGoroutine(), nSuccess, totalSuccess, failed, totalFailed)
	}
}

func loop(addr string, connNum int) {
	conns := make([]net.Conn, connNum)
	for i := 0; i < connNum; i++ {
		for {
			conn, err := net.Dial("tcp", addr)
			if err == nil {
				conns[i] = conn
				atomic.AddUint64(&connected, 1)
				break
			}
			time.Sleep(time.Second / 10)
		}
	}
	for {
		for i := 0; i < connNum; i++ {
			post(conns[i], addr)
		}
	}
}

func post(conn net.Conn, addr string) {
	reqData := []byte(fmt.Sprintf("POST /echo HTTP/1.1\r\nHost: %v\r\nContent-Length: 5\r\nAccept-Encoding: gzip\r\n\r\nhello", addr))
	resData := make([]byte, 1024)
	n, err := conn.Write(reqData)
	if err != nil || n < len(reqData) {
		atomic.AddUint64(&failed, 1)
		fmt.Println("write failed:", n, err)
		return
	}
	// time.Sleep(time.Second / 10)
	// n, err = io.ReadFull(conn, resData)
	n, err = conn.Read(resData)
	if err != nil || string(resData[n-5:n]) != "hello" {
		atomic.AddUint64(&failed, 1)
		fmt.Println("read failed:", n, err)
	}
	atomic.AddUint64(&success, 1)
}

var addrs = []string{
	"localhost:28001",
	"localhost:28002",
	"localhost:28003",
	"localhost:28004",
	"localhost:28005",
	"localhost:28006",
	"localhost:28007",
	"localhost:28008",
	"localhost:28009",
	"localhost:28010",

	"localhost:28011",
	"localhost:28012",
	"localhost:28013",
	"localhost:28014",
	"localhost:28015",
	"localhost:28016",
	"localhost:28017",
	"localhost:28018",
	"localhost:28019",
	"localhost:28020",

	"localhost:28021",
	"localhost:28022",
	"localhost:28023",
	"localhost:28024",
	"localhost:28025",
	"localhost:28026",
	"localhost:28027",
	"localhost:28028",
	"localhost:28029",
	"localhost:28030",

	"localhost:28031",
	"localhost:28032",
	"localhost:28033",
	"localhost:28034",
	"localhost:28035",
	"localhost:28036",
	"localhost:28037",
	"localhost:28038",
	"localhost:28039",
	"localhost:28040",

	"localhost:28041",
	"localhost:28042",
	"localhost:28043",
	"localhost:28044",
	"localhost:28045",
	"localhost:28046",
	"localhost:28047",
	"localhost:28048",
	"localhost:28049",
	"localhost:28050",
}
