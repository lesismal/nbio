package main

import (
	"fmt"
	"io"
	"net/http"
	"runtime"
	"time"
)

func onEcho(w http.ResponseWriter, r *http.Request) {
	data, _ := io.ReadAll(r.Body)
	if len(data) > 0 {
		w.Write(data)
	} else {
		w.Write([]byte(time.Now().Format("20060102 15:04:05")))
	}
}

func serve(addr string) {
	mux := &http.ServeMux{}
	mux.HandleFunc("/echo", onEcho)
	server := http.Server{
		Addr:    addr,
		Handler: mux,
	}
	server.ListenAndServe()
}

func main() {
	go serve("localhost:9000")
	go serve("localhost:9001")
	go serve("localhost:9002")

	ticker := time.NewTicker(time.Second)
	for i := 1; true; i++ {
		<-ticker.C
		fmt.Printf("running for %v seconds, NumGoroutine: %v\n", i, runtime.NumGoroutine())
	}
}
