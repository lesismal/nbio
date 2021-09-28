package main

import (
	"context"
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

func newUpgrader() *websocket.Upgrader {
	u := websocket.NewUpgrader()
	u.OnMessage(func(c *websocket.Conn, messageType websocket.MessageType, data []byte) {
		// echo
		time.AfterFunc(time.Second, func() {
			c.WriteMessage(messageType, data)
		})
		log.Println("onEcho:", string(data))
	})

	u.OnClose(func(c *websocket.Conn, err error) {
		fmt.Println("OnClose:", c.RemoteAddr().String(), err)
	})

	return u
}

func main() {
	engine := nbhttp.NewEngineTLS(nbhttp.Config{
		SupportClient: true,
	})
	err := engine.Start()
	if err != nil {
		fmt.Printf("nbio.Start failed: %v\n", err)
		return
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
	}
	for i := 0; i < 1; i++ {
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
		c.WriteMessage(websocket.TextMessage, []byte("hello"))
	}

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	engine.Shutdown(ctx)
}
