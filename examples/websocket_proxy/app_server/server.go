package main

import (
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

func msgType(messageType int) string {
	switch messageType {
	case websocket.BinaryMessage:
		return "[websocket.BinaryMessage]"
	case websocket.TextMessage:
		return "[websocket.TextMessage]"
	default:
	}
	return "[]"
}

func echo(w http.ResponseWriter, r *http.Request) {
	upgrader := &websocket.Upgrader{}
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Print("upgrade:", err)
		return
	}
	defer c.Close()
	for {
		messageType, message, err := c.ReadMessage()
		if err != nil {
			log.Println("read failed:", err)
			break
		}

		log.Printf("app server onMessage: %v, %v", msgType(messageType), string(message))

		err = c.WriteMessage(messageType, message)
		if err != nil {
			log.Println("write failed:", err)
			break
		}
	}
}

var (
	appServerAddr = "localhost:9999"
)

func main() {
	mux := &http.ServeMux{}
	mux.HandleFunc("/ws", echo)
	server := http.Server{
		Addr:    appServerAddr,
		Handler: mux,
	}
	log.Println("server exit:", server.ListenAndServe())
}
