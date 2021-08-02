package main

import (
	"fmt"
	"io"
	"net/http"
	_ "net/http/pprof"
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
	for _, v := range addrs {
		go func(addr string) {
			mux := &http.ServeMux{}
			mux.HandleFunc("/echo", onEcho)

			server := http.Server{
				Addr:    addr,
				Handler: mux,
			}
			server.ListenAndServe()
		}(v)
	}
}

func main() {
	go func() {
		if err := http.ListenAndServe(":6060", nil); err != nil {
			panic(err)
		}
	}()

	addrs := []string{"localhost:8888"}
	serve(addrs)

	ticker := time.NewTicker(time.Second)
	for i := 1; true; i++ {
		<-ticker.C
		n := atomic.SwapUint64(&qps, 0)
		total += n
		fmt.Printf("running for %v seconds, NumGoroutine: %v, qps: %v, total: %v\n", i, runtime.NumGoroutine(), n, total)
	}
}
