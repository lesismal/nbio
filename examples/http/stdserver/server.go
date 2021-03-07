package main

import (
	"io"
	"net/http"
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

func main() {
	mux := &http.ServeMux{}
	mux.HandleFunc("/echo", onEcho)
	http.ListenAndServe("localhost:9999", mux)
}
