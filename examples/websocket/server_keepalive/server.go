package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/lesismal/nbio"
	"github.com/lesismal/nbio/nbhttp"
	"github.com/lesismal/nbio/nbhttp/websocket"
)

var (
	KeepaliveTime    = time.Second * 5
	KeepaliveTimeout = KeepaliveTime + time.Second*3

	server *nbhttp.Server
)

func onWebsocket(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.NewUpgrader()
	upgrader.OnMessage(func(c *websocket.Conn, messageType websocket.MessageType, data []byte) {
		log.Println("onMessage:", string(data))

		// step 2: reset ping timer
		keepaliveTimer := c.Session().(*nbio.Timer)
		keepaliveTimer.Reset(KeepaliveTime)

		// echo
		c.WriteMessage(messageType, data)

		// update read deadline
		c.SetReadDeadline(time.Now().Add(KeepaliveTimeout))

	})
	upgrader.SetPongHandler(func(c *websocket.Conn, s string) {
		log.Println("-- pone")

		// step 3: reset ping timer
		keepaliveTimer := c.Session().(*nbio.Timer)
		keepaliveTimer.Reset(KeepaliveTime)

		// update read deadline
		c.SetReadDeadline(time.Now().Add(KeepaliveTimeout))
	})

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		panic(err)
	}
	wsConn := conn.(*websocket.Conn)

	// step 1: set ping timer and save it
	closed := false
	var ping func()
	ping = func() {
		if closed {
			return
		}
		log.Println("++ ping")
		wsConn.WriteMessage(websocket.PingMessage, nil)

		keepaliveTimer := server.AfterFunc(KeepaliveTime, ping)
		wsConn.SetSession(keepaliveTimer)
	}
	keepaliveTimer := server.AfterFunc(KeepaliveTime, ping)
	wsConn.SetSession(keepaliveTimer)

	wsConn.OnClose(func(c *websocket.Conn, err error) {
		closed = true

		// step 4: clear ping timer
		keepaliveTimer := c.Session().(*nbio.Timer)
		keepaliveTimer.Stop()
	})
	// init read deadline
	wsConn.SetReadDeadline(time.Now().Add(KeepaliveTimeout))
}

func main() {
	mux := &http.ServeMux{}
	mux.HandleFunc("/ws", onWebsocket)

	server = nbhttp.NewServer(nbhttp.Config{
		Network: "tcp",
		Addrs:   []string{"localhost:8888"},
	}, mux, nil)

	err := server.Start()
	if err != nil {
		fmt.Printf("nbio.Start failed: %v\n", err)
		return
	}

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	server.Shutdown(ctx)
}
