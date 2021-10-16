package main

import (
	"context"
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
	proxyServerAddr = "localhost:8888"

	appServerAddr = "ws://localhost:9999/ws"

	proxyServer *nbhttp.Server
)

func msgType(messageType websocket.MessageType) string {
	switch messageType {
	case websocket.BinaryMessage:
		return "[websocket.BinaryMessage]"
	case websocket.TextMessage:
		return "[websocket.TextMessage]"
	default:
	}
	return "[]"
}

func newUpgrader() *websocket.Upgrader {
	u := websocket.NewUpgrader()
	u.OnMessage(func(c *websocket.Conn, messageType websocket.MessageType, data []byte) {
		peer, ok := c.Session().(*websocket.Conn)
		if ok {
			log.Printf("proxy server onMessage [%v -> %v]: %v, %v", c.RemoteAddr(), peer.RemoteAddr(), msgType(messageType), string(data))
			peer.WriteMessage(messageType, data)
		}
	})
	u.OnClose(func(c *websocket.Conn, err error) {
		peer, ok := c.Session().(*websocket.Conn)
		if ok {
			log.Printf("proxy server onClose [%v -> %v]", c.RemoteAddr(), peer.RemoteAddr())
			peer.Close()
		} else {
			log.Printf("proxy server onClose [%v -> %v]", c.RemoteAddr(), "")
		}
	})
	return u
}

func onWebsocket(w http.ResponseWriter, r *http.Request) {
	upgrader := newUpgrader()
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		panic(err)
	}
	srcConn := conn.(*websocket.Conn)

	dialer := &websocket.Dialer{
		Engine:      proxyServer.Engine,
		Upgrader:    newUpgrader(),
		DialTimeout: time.Second * 3,
	}
	dstConn, _, err := dialer.Dial(appServerAddr, nil)
	if err != nil {
		log.Printf("Dial failed: %v, %v", appServerAddr, err)
		srcConn.Close()
		return
	}

	srcConn.SetSession(dstConn)
	dstConn.SetSession(srcConn)
}

func main() {
	mux := &http.ServeMux{}
	mux.HandleFunc("/ws", onWebsocket)

	proxyServer = nbhttp.NewServer(nbhttp.Config{
		Network: "tcp",
		Addrs:   []string{proxyServerAddr},
		Handler: mux,
	})

	err := proxyServer.Start()
	if err != nil {
		fmt.Printf("nbio.Start failed: %v\n", err)
		return
	}

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	proxyServer.Shutdown(ctx)
}
