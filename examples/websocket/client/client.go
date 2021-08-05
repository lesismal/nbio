package main

import (
	"flag"
	"fmt"
	"log"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	flag.Parse()

	u := url.URL{Scheme: "ws", Host: "localhost:8888", Path: "/ws"}
	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer c.Close()

	i := 0
	for {
		{
			i++
			request := fmt.Sprintf("hello %v", i)
			err := c.WriteMessage(websocket.BinaryMessage, []byte(request))
			if err != nil {
				log.Fatalf("write: %v", err)
				return
			}

			receiveType, response, err := c.ReadMessage()
			if err != nil {
				log.Println("ReadMessage failed:", err)
				return
			}
			if receiveType != websocket.BinaryMessage {
				log.Println("received type != websocket.BinaryMessage")
				return

			}

			if string(response) != request {
				log.Printf("'%v' != '%v'", len(response), len(request))
				return
			}

			log.Println("success echo websocket.BinaryMessage:", request)
		}

		{
			i++
			request := fmt.Sprintf("hello %v", i)
			err := c.WriteMessage(websocket.TextMessage, []byte(request))
			if err != nil {
				log.Fatalf("write: %v", err)
				return
			}

			receiveType, response, err := c.ReadMessage()
			if err != nil {
				log.Println("ReadMessage failed:", err)
				return
			}
			if receiveType != websocket.TextMessage {
				log.Println("received type != websocket.TextMessage")
				return

			}

			if string(response) != request {
				log.Printf("'%v' != '%v'", len(response), len(request))
				return
			}

			log.Println("success echo websocket.TextMessage  :", request)
		}

		time.Sleep(time.Second)
	}
}
