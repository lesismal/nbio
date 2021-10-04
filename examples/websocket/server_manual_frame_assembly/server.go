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
	bufferPool = NewFixedBufferPool(2000, 1*1024*1024, time.Second*10)
)

func newUpgrader() *websocket.Upgrader {
	u := websocket.NewUpgrader()

	onMessageFunc := func(c *websocket.Conn, data []byte) {
		c.WriteMessage(websocket.BinaryMessage, data)
	}
	u.OnDataFrame(func(c *websocket.Conn, messageType websocket.MessageType, fin bool, data []byte) {
		curBuf := c.Session()
		// frame == message
		if fin && curBuf == nil {
			onMessageFunc(c, data)
			return
		}
		if curBuf == nil {
			b, err := bufferPool.Get()
			if err != nil {
				c.WriteMessage(websocket.CloseMessage, []byte(fmt.Sprintf("%v", err)))
				return
			}
			curBuf = b
		}
		b := curBuf.([]byte)
		if cap(b) < len(b)+len(data) {
			c.WriteMessage(websocket.CloseMessage, []byte("websocket message too large"))
			return
		}
		b = append(b, data...)
		if fin {
			onMessageFunc(c, data)
			bufferPool.Put(b)
			c.SetSession(nil)
		} else {
			c.SetSession(b)
		}
	})

	u.OnClose(func(c *websocket.Conn, err error) {
		curBuf := c.Session()
		b := curBuf.([]byte)
		bufferPool.Put(b)
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
		Network:                 "tcp",
		Addrs:                   []string{"localhost:8888"},
		ReleaseWebsocketPayload: true,
	}, mux, nil)

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
