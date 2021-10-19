package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"time"

	"github.com/lesismal/nbio/nbhttp"
)

func onEcho(w http.ResponseWriter, r *http.Request) {
	data := r.Body.(*nbhttp.BodyReader).RawBody()
	if len(data) > 0 {
		w.Write(data)
	} else {
		w.Write([]byte(time.Now().Format("20060102 15:04:05")))
	}
}

func main() {
	mux := &http.ServeMux{}
	mux.HandleFunc("/echo", onEcho)
	svr := nbhttp.NewServer(nbhttp.Config{
		Network: "tcp",
		Addrs:   []string{"localhost:8888"},
		MaxLoad: 1000000,
		NPoller: runtime.NumCPU() * 2,
		Handler: mux,
	})

	err := svr.Start()
	if err != nil {
		fmt.Printf("nbio.Start failed: %v\n", err)
		return
	}

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()
	svr.Shutdown(ctx)
}
