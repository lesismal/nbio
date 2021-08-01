package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lesismal/nbio/nbhttp"
	"github.com/lesismal/nbio/nbhttp/websocket"
)

func main() {
	router := gin.New()
	router.GET("/hello", func(c *gin.Context) {
		c.String(http.StatusOK, "hello")
		fmt.Println("http: hello")
	})
	router.GET("/ws", func(c *gin.Context) {
		w := c.Writer
		r := c.Request
		upgrader := websocket.NewUpgrader()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			panic(err)
		}
		wsConn := conn.(*websocket.Conn)
		wsConn.OnMessage(func(c *websocket.Conn, messageType websocket.MessageType, data []byte) {
			// echo
			fmt.Println("websocket:", messageType, string(data), len(data))
			c.WriteMessage(messageType, data)
		})
		wsConn.OnClose(func(c *websocket.Conn, err error) {
			fmt.Println("OnClose:", c.RemoteAddr().String(), err)
		})
		fmt.Println("OnOpen:", wsConn.RemoteAddr().String())
	})

	svr := nbhttp.NewServer(nbhttp.Config{
		Network: "tcp",
		Addrs:   []string{"localhost:8888"},
	}, router, nil)

	err := svr.Start()
	if err != nil {
		fmt.Printf("nbio.Start failed: %v\n", err)
		return
	}

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()
	svr.Shutdown(ctx)

}
