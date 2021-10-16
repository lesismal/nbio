package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/lesismal/nbio/nbhttp"
	"github.com/lesismal/nbio/nbhttp/websocket"
)

var compression = flag.Bool("compression", false, "allow compression")

func newUpgrader() *websocket.Upgrader {
	u := websocket.NewUpgrader()
	if *compression {
		u.EnableCompression(true)
	}
	var output io.WriteCloser
	id := 0
	u.OnDataFrame(func(c *websocket.Conn, messageType websocket.MessageType, fin bool, data []byte) {
		if output == nil {
			var err error
			deflateStr := ""
			if u.CompressionEnabled() {
				deflateStr = ".df"
			}
			filename := fmt.Sprintf("data_%d.%d%s", id, time.Now().UnixNano(), deflateStr)
			id++
			output, err = os.Create(filename)
			if err != nil {
				log.Println("failed to create file: ", filename, err)
				c.Close()
				return
			}
		}
		n, err := output.Write(data)
		if err != nil {
			log.Println("error writing to file: ", err)
			c.Close()
			return
		}
		if n != len(data) {
			log.Println("failed to write all frame data to file")
			c.Close()
			return
		}
		if fin {
			output.Close()
			output = nil
		}
	})

	u.OnClose(func(c *websocket.Conn, err error) {
		fmt.Println("OnClose:", c.RemoteAddr().String(), err)
		if output != nil {
			output.Close()
		}
	})
	return u
}

func onWebsocket(w http.ResponseWriter, r *http.Request) {
	// time.Sleep(time.Second * 5)
	upgrader := newUpgrader()
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		panic(err)
	}
	wsConn := conn.(*websocket.Conn)
	wsConn.SetReadDeadline(time.Time{})
	fmt.Println("OnOpen:", wsConn.RemoteAddr().String())
}

func main() {
	flag.Parse()
	mux := &http.ServeMux{}
	mux.HandleFunc("/ws", onWebsocket)

	svr := nbhttp.NewServer(nbhttp.Config{
		Network: "tcp",
		Addrs:   []string{"localhost:8888"},
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
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	svr.Shutdown(ctx)
}
