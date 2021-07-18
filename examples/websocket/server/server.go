package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/lesismal/nbio/nbhttp"
	"github.com/lesismal/nbio/nbhttp/websocket"
)

var (
	svr   *nbhttp.Server
	addr  = flag.String("addr", ":8888", "listening addr")
	path  = flag.String("path", "/ws", "url path")
	print = flag.Bool("print", false, "output input to standardout")
)

func onWebsocket(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.NewUpgrader()
	upgrader.EnableCompression = true
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		panic(err)
	}
	wsConn := conn.(*websocket.Conn)
	wsConn.EnableWriteCompression(true)
	wsConn.SetReadDeadline(time.Time{})
	wsConn.OnMessage(func(c *websocket.Conn, messageType websocket.MessageType, data []byte) {
		// echo
		if *print {
			switch messageType {
			case websocket.TextMessage:
				fmt.Println("OnMessage:", messageType, string(data), len(data))
			case websocket.BinaryMessage:
				fmt.Println("OnMessage:", messageType, data, len(data))
			}
		}
		c.WriteMessage(messageType, data)
	})
	wsConn.OnClose(func(c *websocket.Conn, err error) {
		if *print {
			fmt.Println("OnClose:", c.RemoteAddr().String(), err)
		}
	})
	if *print {
		fmt.Println("OnOpen:", wsConn.RemoteAddr().String())
	}
}

func main() {
	flag.Parse()
	mux := &http.ServeMux{}
	mux.HandleFunc(*path, onWebsocket)

	svr = nbhttp.NewServer(nbhttp.Config{
		Network: "tcp",
		Addrs:   []string{*addr},
	}, mux, nil)

	err := svr.Start()
	if err != nil {
		fmt.Printf("nbio.Start failed: %v\n", err)
		return
	}
	defer svr.Stop()

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
	log.Println("exit")
}
