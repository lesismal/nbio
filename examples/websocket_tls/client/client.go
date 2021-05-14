package main

import (
	"crypto/tls"
	"flag"
	"log"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var addr = flag.String("addr", "localhost:8888", "http service address")

func main() {
	flag.Parse()

	u := url.URL{Scheme: "wss", Host: *addr, Path: "/wss"}
	log.Printf("connecting to %s", u.String())

	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
	}
	dialer := &websocket.Dialer{
		TLSClientConfig: tlsConfig,
	}
	waitGroup := sync.WaitGroup{}

	waitGroup.Add(10)
	for i := 0; i < 100; i++ {
		go func() {
			defer waitGroup.Done()
			c, _, err := dialer.Dial(u.String(), nil)
			if err != nil {
				log.Fatal("dial:", err)
			}
			defer c.Close()

			text := "hello world"
			for {
				err := c.WriteMessage(websocket.TextMessage, []byte(text))
				if err != nil {
					log.Fatalf("write: %v", err)
					return
				}
				log.Println("write:", text)

				_, message, err := c.ReadMessage()
				if err != nil {
					log.Println("read:", err)
					return
				}
				if string(message) != text {
					log.Fatalf("message != text: %v, %v", len(message), string(message))
				} else {
					log.Println("read :", string(message))
				}
				time.Sleep(time.Millisecond * 500)
			}
		}()
	}
	waitGroup.Wait()
}
