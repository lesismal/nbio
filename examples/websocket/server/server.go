package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/lesismal/nbio/nbhttp"
	"github.com/lesismal/nbio/nbhttp/websocket"
)

var (
	onDataFrame = flag.Bool("UseOnDataFrame", false, "Server will use OnDataFrame api instead of OnMessage")
	errBeforeUpgrade = flag.Bool("error-before-upgrade", false, "return an error on upgrade with body")
)

func newUpgrader() *websocket.Upgrader {
	u := websocket.NewUpgrader()
	if *onDataFrame {
		u.OnDataFrame(func(c *websocket.Conn, messageType websocket.MessageType, fin bool, data []byte) {
			// echo
			c.WriteFrame(messageType, true, fin, data)
		})
	} else {
		u.OnMessage(func(c *websocket.Conn, messageType websocket.MessageType, data []byte) {
			// echo
			c.WriteMessage(messageType, data)
		})
	}

	u.OnClose(func(c *websocket.Conn, err error) {
		fmt.Println("OnClose:", c.RemoteAddr().String(), err)
	})
	return u
}

func onWebsocket(w http.ResponseWriter, r *http.Request) {
	if *errBeforeUpgrade {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("returning an error"))
		return 
	}
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
