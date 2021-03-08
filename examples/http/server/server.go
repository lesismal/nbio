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

	g := nbhttp.NewServer(nbhttp.Config{
		Network: "tcp",
		Addrs: []string{
			"localhost:28000",
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
		},
		MaxLoad:      1000000,
		NPoller:      runtime.NumCPU() * 2,
		NParser:      runtime.NumCPU() * 4,
		TaskPoolSize: runtime.NumCPU() * 512,
		TaskIdleTime: time.Second * 120,
		LockThread:   true,
	}, mux, nil)

	err := g.Start()
	if err != nil {
		fmt.Printf("nbio.Start failed: %v\n", err)
		return
	}
	defer g.Stop()

	ticker := time.NewTicker(time.Second)
	for i := 1; true; i++ {
		<-ticker.C
		n := atomic.SwapUint64(&qps, 0)
		total += n
		fmt.Printf("running for %v seconds, online: %v, NumGoroutine: %v, qps: %v, total: %v\n", i, g.State().Online, runtime.NumGoroutine(), n, total)
		// if i%10 == 1 {
		// 	fmt.Println(g.State().String())
		// }
	}
}
