package main

import (
	"fmt"
	"io"
	"net/http"
	"runtime"
	"time"

	"github.com/lesismal/nbio/nbhttp"
)

func onEcho(w http.ResponseWriter, r *http.Request) {
	data, _ := io.ReadAll(r.Body)
	if len(data) > 0 {
		w.Write(data)
	} else {
		w.Write([]byte(time.Now().Format("20060102 15:04:05")))
	}
}

func main() {
	mux := &http.ServeMux{}
	mux.HandleFunc("/echo", onEcho)

	g := nbhttp.NewServer(nbhttp.Config{
		Network:      "tcp",
		Addrs:        []string{"localhost:8000", "localhost:8001", "localhost:8002"},
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
		fmt.Printf("running for %v seconds, NumGoroutine: %v\n", i, runtime.NumGoroutine())
		fmt.Println(g.State().String())
	}
}
