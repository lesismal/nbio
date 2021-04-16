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
	go func() {
		if err := http.ListenAndServe(":6060", nil); err != nil {
			panic(err)
		}
	}()

	mux := &http.ServeMux{}
	mux.HandleFunc("/echo", onEcho)

	// router := httprouter.New()
	// router.POST("/echo", onEcho)

	// pool := taskpool.NewFastPool(4096)
	svr := nbhttp.NewServer(nbhttp.Config{
		Network: "tcp",
		Addrs:   []string{"localhost:8888"},
	}, mux, nil) // pool.Go)

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
		fmt.Printf("running for %v seconds, online: %v, NumGoroutine: %v, qps: %v, total: %v\n", i, svr.State().Online, runtime.NumGoroutine(), n, total)
	}
}
