package main

import (
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
	svr *nbhttp.Server
)

func onWebsocket(w http.ResponseWriter, r *http.Request) {
	isTLS := false
	upgrader := websocket.NewUpgrader(isTLS)
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		panic(err)
	}
	wsConn := conn.(*websocket.Conn)
	wsConn.OnMessage(func(c *websocket.Conn, messageType int8, data []byte) {
		// echo
		c.WriteMessage(messageType, data)
		fmt.Println("OnMessage:", messageType, string(data))
		c.SetReadDeadline(time.Now().Add(time.Second * 60))
	})
	wsConn.OnClose(func(c *websocket.Conn, err error) {
		fmt.Println("OnClose:", c.RemoteAddr().String(), err)
	})
	fmt.Println("OnOpen:", wsConn.RemoteAddr().String())
}

func main() {
	mux := &http.ServeMux{}
	mux.HandleFunc("/ws", onWebsocket)

	svr = nbhttp.NewServer(nbhttp.Config{
		Network: "tcp",
		Addrs:   []string{"localhost:8888"},
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
