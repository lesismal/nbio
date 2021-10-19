package main

import (
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/lesismal/nbio/nbhttp"
)

var (
	qps   uint64 = 0
	total uint64 = 0
)

func onEcho(w http.ResponseWriter, r *http.Request) {
	// time.Sleep(time.Second * 5)
	// data, _ := io.ReadAll(r.Body)
	data := r.Body.(*nbhttp.BodyReader).RawBody()
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

	svr := nbhttp.NewServer(nbhttp.Config{
		Network: "tcp",
		Addrs:   []string{"localhost:8888"},
		Handler: mux,
	}) // pool.Go)

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
