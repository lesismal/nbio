package main

import (
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/valyala/fasthttp"
)

var (
	qps   uint64 = 0
	total uint64 = 0
)

func onEcho(ctx *fasthttp.RequestCtx) {
	data := ctx.PostBody()
	if len(data) > 0 {
		ctx.Write(data)
	} else {
		ctx.Write([]byte(time.Now().Format("20060102 15:04:05")))
	}
	atomic.AddUint64(&qps, 1)
}

func main() {
	go func() {
		if err := http.ListenAndServe(":6060", nil); err != nil {
			panic(err)
		}
	}()

	go fasthttp.ListenAndServe("localhost:8888", onEcho)

	ticker := time.NewTicker(time.Second)
	for i := 1; true; i++ {
		<-ticker.C
		n := atomic.SwapUint64(&qps, 0)
		total += n
		fmt.Printf("running for %v seconds, NumGoroutine: %v, qps: %v, total: %v\n", i, runtime.NumGoroutine(), n, total)
	}
}
