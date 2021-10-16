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
	keepaliveTime    = time.Second * 5
	keepaliveTimeout = keepaliveTime + time.Second*3

	server *nbhttp.Server
)

func onWebsocket(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.NewUpgrader()
	upgrader.OnMessage(func(c *websocket.Conn, messageType websocket.MessageType, data []byte) {
		log.Println("onMessage:", string(data))

		// step 2: reset ping timer
		keepaliveTimer := c.Session().(*nbio.Timer)
		keepaliveTimer.Reset(keepaliveTime)

		// echo
		c.WriteMessage(messageType, data)

		// update read deadline
		c.SetReadDeadline(time.Now().Add(keepaliveTimeout))

	})
	upgrader.SetPongHandler(func(c *websocket.Conn, s string) {
		log.Println("-- pone")

		// step 3: reset ping timer
		keepaliveTimer := c.Session().(*nbio.Timer)
		keepaliveTimer.Reset(keepaliveTime)

		// update read deadline
		c.SetReadDeadline(time.Now().Add(keepaliveTimeout))
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

		keepaliveTimer := server.AfterFunc(keepaliveTime, ping)
		wsConn.SetSession(keepaliveTimer)
	}
	keepaliveTimer := server.AfterFunc(keepaliveTime, ping)
	wsConn.SetSession(keepaliveTimer)

	wsConn.OnClose(func(c *websocket.Conn, err error) {
		closed = true

		// step 4: clear ping timer
		keepaliveTimer := c.Session().(*nbio.Timer)
		keepaliveTimer.Stop()
	})
	// init read deadline
	wsConn.SetReadDeadline(time.Now().Add(keepaliveTimeout))
}

func main() {
	mux := &http.ServeMux{}
	mux.HandleFunc("/ws", onWebsocket)

	server = nbhttp.NewServer(nbhttp.Config{
		Network: "tcp",
		Addrs:   []string{"localhost:8888"},
		Handler: mux,
	})

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
