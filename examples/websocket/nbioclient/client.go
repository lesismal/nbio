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
)

var (
	disableEcho = flag.Bool("disable-echo", false, "diable echo incoming message")
	verbose     = flag.Bool("verbose", false, "verbose mode")
	connections = flag.Int("connections", 1000, "number of connecions")
	msgSize     = flag.Int("msg-size", 1*1024*1024, "message size")
)

func newUpgrader(connectedChannel *chan *websocket.Conn) *websocket.Upgrader {
	u := websocket.NewUpgrader()
	u.OnMessage(func(c *websocket.Conn, messageType websocket.MessageType, data []byte) {
		// echo
		if !*disableEcho {
			time.AfterFunc(time.Second, func() {
				c.WriteMessage(messageType, data)
			})
			log.Println("onEcho:", string(data))
		}
		*connectedChannel <- c
	})
	u.OnClose(func(c *websocket.Conn, err error) {
		if *verbose {
			fmt.Println("OnClose:", c.RemoteAddr().String(), err)
		}
	})
	return u
}

func main() {
	flag.Parse()
	engine := nbhttp.NewEngine(nbhttp.Config{
		SupportClient: true,
	})
	err := engine.Start()
	if err != nil {
		fmt.Printf("nbio.Start failed: %v\n", err)
		return
	}

	connectionChannels := make(chan *websocket.Conn, *connections)
	for i := 0; i < *connections; i++ {
		u := url.URL{Scheme: "ws", Host: "localhost:8888", Path: "/ws"}
		dialer := &websocket.Dialer{
			Engine:      engine,
			Upgrader:    newUpgrader(&connectionChannels),
			DialTimeout: time.Second * 3,
		}
		c, _, err := dialer.Dial(u.String(), nil)
		if err != nil {
			panic(fmt.Errorf("dial: %v", err))
		}
		connectionChannels <- c
	}
	output := make([]byte, *msgSize, *msgSize)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	for i := 0; i < 10; i++ {
		go func() {
			for {
			select {
			case c := <-connectionChannels:
				time.Sleep(1000)
				c.WriteMessage(websocket.BinaryMessage, output)
			case <-ctx.Done():
				return
			}
		}
		}()
	}
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
	defer cancel()
	engine.Shutdown(ctx)
}
