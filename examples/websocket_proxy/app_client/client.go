package main

import (
	"fmt"
	"log"
	"time"

	"github.com/gorilla/websocket"
)

var (
	proxyServerAddr = "ws://localhost:8888/ws"
)

func main() {
	c, _, err := websocket.DefaultDialer.Dial(proxyServerAddr, nil)
	if err != nil {
		log.Fatalf("Dial failed: %v, %v", proxyServerAddr, err)
	}
	defer c.Close()

	for i := 0; i < 10; i++ {
		{
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
				log.Printf("received type(%d) != websocket.BinaryMessage(%d)\n", receiveType, websocket.BinaryMessage)
				return

			}

			if string(response) != request {
				log.Printf("'%v' != '%v'", len(response), len(request))
				return
			}

			log.Printf("success echo: [websocket.BinaryMessage], %v", request)
		}

		{
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
				log.Printf("received type(%d) != websocket.TextMessage(%d)\n", receiveType, websocket.TextMessage)
				return

			}

			if string(response) != request {
				log.Printf("'%v' != '%v'", len(response), len(request))
				return
			}

			log.Printf("success echo: [websocket.TextMessage], %v", request)
		}
		time.Sleep(time.Second)
	}
}
