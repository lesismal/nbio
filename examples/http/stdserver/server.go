package main

import (
	"fmt"
	"io"
	"net/http"
	"runtime"
	"sync/atomic"
	"time"
)

var (
	qps   uint64 = 0
	total uint64 = 0
)

func onEcho(w http.ResponseWriter, r *http.Request) {
	data, _ := io.ReadAll(r.Body)
	if len(data) > 0 {
		w.Write(data)
	} else {
		w.Write([]byte(time.Now().Format("20060102 15:04:05")))
	}
	atomic.AddUint64(&qps, 1)
}

func serve(addrs []string) {
	for _, addr := range addrs {
		go func() {
			mux := &http.ServeMux{}
			mux.HandleFunc("/echo", onEcho)
			server := http.Server{
				Addr:    addr,
				Handler: mux,
			}
			server.ListenAndServe()
		}()
	}
}

func main() {
	addrs := []string{
		"localhost:29000",
		"localhost:29001",
		"localhost:29002",
		"localhost:29003",
		"localhost:29004",
		"localhost:29005",
		"localhost:29006",
		"localhost:29007",
		"localhost:29008",
		"localhost:29009",
		"localhost:29010",
		"localhost:29011",
		"localhost:29012",
		"localhost:29013",
		"localhost:29014",
		"localhost:29015",
		"localhost:29016",
		"localhost:29017",
		"localhost:29018",
		"localhost:29019",
		"localhost:29020",
		"localhost:29021",
		"localhost:29022",
		"localhost:29023",
		"localhost:29024",
	}
	serve(addrs)

	ticker := time.NewTicker(time.Second)
	for i := 1; true; i++ {
		<-ticker.C
		n := atomic.SwapUint64(&qps, 0)
		total += n
		fmt.Printf("running for %v seconds, NumGoroutine: %v, qps: %v, total: %v\n", i, runtime.NumGoroutine(), n, total)
	}
}
