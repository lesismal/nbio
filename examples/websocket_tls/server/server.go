package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/lesismal/llib/std/crypto/tls"
	"github.com/lesismal/nbio/nbhttp"
	"github.com/lesismal/nbio/nbhttp/websocket"
)

var (
	svr *nbhttp.Server
)

func onWebsocket(w http.ResponseWriter, r *http.Request) {
	upgrader := &websocket.Upgrader{}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		panic(err)
	}
	wsConn := conn.(*websocket.Conn)
	wsConn.OnMessage(func(c *websocket.Conn, messageType int8, data []byte) {
		svr.MessageHandlerExecutor(func() {
			// echo
			c.WriteMessage(messageType, data)
			fmt.Println("OnMessage:", messageType, string(data))

			c.SetReadDeadline(time.Now().Add(nbhttp.DefaultKeepaliveTime))
		})
	})
	wsConn.OnClose(func(c *websocket.Conn, err error) {
		fmt.Println("OnClose:", c.RemoteAddr().String(), err)
	})
	fmt.Println("OnOpen:", wsConn.RemoteAddr().String())
}

func main() {
	cert, err := tls.LoadX509KeyPair("server.crt", "server.key")
	if err != nil {
		log.Fatalf("tls.X509KeyPair failed: %v", err)
	}
	tlsConfig := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: true,
	}
	tlsConfig.BuildNameToCertificate()

	mux := &http.ServeMux{}
	mux.HandleFunc("/wss", onWebsocket)

	svr = nbhttp.NewServerTLS(nbhttp.Config{
		Network: "tcp",
		Addrs:   []string{"localhost:8888"},
	}, mux, nil, tlsConfig)

	// to improve performance if you need
	// parserPool := taskpool.NewFixedPool(runtime.NumCPU()*4, 1024)
	// svr.ParserExecutor = parserPool.GoByIndex

	err = svr.Start()
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
