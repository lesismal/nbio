package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"time"

	"github.com/lesismal/nbio/nbhttp"
	"github.com/lesismal/nbio/nbhttp/websocket"

	"github.com/lesismal/llib/std/crypto/tls"
)

var (
	clients           = flag.Int("clients", 1, "number of clients")
	floodLargeMessage = flag.Bool("flood", false, "flood server with large messages")
	noEcho            = flag.Bool("no-echo", false, "disables echo server message")
	connectedClients  chan *websocket.Conn
)

func newUpgrader() *websocket.Upgrader {
	u := websocket.NewUpgrader()
	u.OnMessage(func(c *websocket.Conn, messageType websocket.MessageType, data []byte) {
		// echo
		if !*noEcho {
			time.AfterFunc(time.Second, func() {
				c.WriteMessage(messageType, data)
			})
			log.Println("onEcho:", string(data))
		}
		connectedClients <- c
	})

	u.OnClose(func(c *websocket.Conn, err error) {
	})

	return u
}

func main() {
	flag.Parse()
	engine := nbhttp.NewEngine(nbhttp.Config{})
	err := engine.Start()
	if err != nil {
		fmt.Printf("nbio.Start failed: %v\n", err)
		return
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
	}

	connectedClients = make(chan *websocket.Conn, *clients)
	for i := 0; i < *clients; i++ {
		u := url.URL{Scheme: "wss", Host: "localhost:8888", Path: "/wss"}
		dialer := &websocket.Dialer{
			Engine:          engine,
			Upgrader:        newUpgrader(),
			DialTimeout:     time.Second * 3,
			TLSClientConfig: tlsConfig,
		}
		c, _, err := dialer.Dial(u.String(), nil)
		if err != nil {
			panic(fmt.Errorf("dial: %v", err))
		}
		connectedClients <- c
	}

	if *floodLargeMessage {
		payload := make([]byte, 1024*1024)
		for i := 0; i < 100; i++ {
			go func() {
				for {
					select {
					case c := <-connectedClients:
						c.WriteMessage(websocket.BinaryMessage, payload)
					}
				}
			}()

		}
	}

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	engine.Shutdown(ctx)
}
