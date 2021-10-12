package main

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"log"
	"net/url"

	"github.com/gorilla/websocket"
)

var (
	addrNonTLS = "localhost:8080"
	addrTLS    = "localhost:8443"
)

func main() {
	clientNonTLS()
	clientTLS()
}

func clientNonTLS() {
	u := url.URL{Scheme: "ws", Host: addrNonTLS, Path: "/ws"}
	log.Printf("connecting to %s", u.String())

	dialer := &websocket.Dialer{}

	c, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer c.Close()

	for i := 0; i < 3; i++ {
		echo(addrNonTLS, c, websocket.TextMessage)
		echo(addrNonTLS, c, websocket.BinaryMessage)
	}
}

func clientTLS() {
	u := url.URL{Scheme: "wss", Host: addrTLS, Path: "/wss"}
	log.Printf("connecting to %s", u.String())

	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
	}
	dialer := &websocket.Dialer{
		TLSClientConfig: tlsConfig,
	}

	c, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer c.Close()

	for i := 0; i < 3; i++ {
		echo(addrTLS, c, websocket.TextMessage)
		echo(addrTLS, c, websocket.BinaryMessage)
	}
}

func echo(addr string, c *websocket.Conn, messageType int) {
	request := make([]byte, 1024)
	if messageType == websocket.TextMessage {
		for i := 0; i < len(request); i++ {
			request[i] = 'a' + byte(i)%26
		}
	} else {
		rand.Read(request)
	}

	err := c.WriteMessage(messageType, request)
	if err != nil {
		log.Fatalf("write: %v", err)
		return
	}

	mt, response, err := c.ReadMessage()
	if err != nil {
		log.Panicf("ReadMessage failed: %v", err)
	}
	if mt != messageType {
		log.Panicf("invalid response messageType: [%v != %v]", mt, messageType)
	}
	if !bytes.Equal(request, response) {
		log.Panicf("invalid response: not equal")
	}
	log.Printf("echo to %v with message type [%v] success", addr, messageType)
}
