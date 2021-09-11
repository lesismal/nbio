package main

import (
	"fmt"
	"io"
	"net/http"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/lesismal/nbio/nbhttp"
)

var (
	qps   uint64 = 0
	total uint64 = 0
)

// visit: https://localhost:8888/echo
func onEcho(w http.ResponseWriter, r *http.Request) {
	data, _ := io.ReadAll(r.Body)
	if len(data) > 0 {
		w.Write(data)
	} else {
		w.Write([]byte(time.Now().Format("20060102 15:04:05")))
	}
	atomic.AddUint64(&qps, 1)
}

func main() {

	mux := &http.ServeMux{}
	mux.HandleFunc("/echo", onEcho)

	svr := nbhttp.NewServerTLS(nbhttp.Config{
		Network: "tcp",
		Addrs:   []string{"localhost:8888"},
	}, mux, nil,
		"/home/mobus/projects/sunvim/prs/nbio/examples/http/server_tls/server.crt",
		"/home/mobus/projects/sunvim/prs/nbio/examples/http/server_tls/server.key")

	err := svr.Start()
	if err != nil {
		fmt.Printf("nbio.Start failed: %v\n", err)
		return
	}
	defer svr.Stop()

	ticker := time.NewTicker(time.Second)
	for i := 1; true; i++ {
		<-ticker.C
		n := atomic.SwapUint64(&qps, 0)
		total += n
		fmt.Printf("running for %v seconds, NumGoroutine: %v, qps: %v, total: %v\n", i, runtime.NumGoroutine(), n, total)
	}
}
