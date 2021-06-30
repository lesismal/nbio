package main

import (
	"bytes"
	"crypto/tls"
	"errors"
	"flag"
	"io"
	"log"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var addr = flag.String("addr", "localhost:8888", "http service address")
var mesgLen = flag.Int("message-len", 4*1048576, "length of message sent")
var clients = flag.Int("clients", 1, "number of clients to simulate")
var print = flag.Bool("print", false, "stdout input and output")

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

	waitGroup.Add(*clients)

	text := make([]byte, *mesgLen, *mesgLen)
	for i := 0; i < *mesgLen; i++ {
		text[i] = 'A'
	}
	for i := 0; i < *clients; i++ {
		go func() {
			defer waitGroup.Done()
			c, _, err := dialer.Dial(u.String(), nil)
			if err != nil {
				log.Fatal("dial:", err)
			}
			defer c.Close()

			for {
				err := c.WriteMessage(websocket.TextMessage, text)
				if err != nil {
					log.Fatalf("write: %v", err)
					return
				}
				log.Println("wrote")
				if *print {
					log.Println("write:", text)
				}

				_, reader, err := c.NextReader()
				if err != nil {
					log.Println("read:", err)
					return
				}
				var message []byte
				line := make([]byte, 65535)
				i := 0
				for {
					log.Printf("pre read")
					l, err := reader.Read(line)
					log.Printf("post read")
					if err != nil {
						if errors.Is(err, io.EOF) {
							break
						}
						log.Fatalf("error while reading %s", err)
					}
					message = append(message, line[:l]...)
					log.Printf("read %d %d %d", i, l, len(message))
					i++

				}
				log.Println("read:", len(message))
				res := bytes.Compare(message, text)
				if res != 0 {
					log.Fatalf("message != text: at offset %d, %v\n", res, message[res:])
				} else {
					if *print {
						log.Println("read :", string(message))
					}
					log.Println("good")
				}
				time.Sleep(time.Millisecond * 500)
			}
		}()
	}
	waitGroup.Wait()
}
