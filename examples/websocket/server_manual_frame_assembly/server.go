package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/lesismal/nbio/examples/fixedbufferpool"
	"github.com/lesismal/nbio/nbhttp"
	"github.com/lesismal/nbio/nbhttp/websocket"
)

var (
	bufferPool = fixedbufferpool.NewFixedBufferPool(2000, 1*1024*1024, time.Second*10)
)

func newUpgrader() *websocket.Upgrader {
	u := websocket.NewUpgrader()

	onMessageFunc := func(c *websocket.Conn, data []byte) {
		c.WriteMessage(websocket.BinaryMessage, data)
	}
	u.OnDataFrame(func(c *websocket.Conn, messageType websocket.MessageType, fin bool, frameData []byte) {
		curBuf := c.Session()
		// frame == message
		if fin && curBuf == nil {
			onMessageFunc(c, frameData)
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
		messageDataSoFar := curBuf.([]byte)
		if cap(messageDataSoFar) < len(messageDataSoFar)+len(frameData) {
			c.WriteMessage(websocket.CloseMessage, []byte("websocket message too large"))
			return
		}
		messageDataSoFar = append(messageDataSoFar, frameData...)
		if fin {
			onMessageFunc(c, messageDataSoFar)
			bufferPool.Put(messageDataSoFar)
			c.SetSession(nil)
		} else {
			c.SetSession(messageDataSoFar)
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
		Handler:                 mux,
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
