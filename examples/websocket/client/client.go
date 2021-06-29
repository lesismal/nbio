package main

import (
	"flag"
	"io"
	"log"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

var addr = flag.String("addr", "localhost:8888", "http service address")
var message = flag.String("message", "hello would", "message send to the server")
var messageLen = flag.Int("mlen", 1024*1024*4, "if set, will override message setting and send message of the specified length")
var path = flag.String("path", "/ws", "path to connect to")
var binary = flag.Bool("binary", false, "if set, message is sent as binary")
var print = flag.Bool("print", false, "output to stdout")

func main() {
	flag.Parse()

	u := url.URL{Scheme: "ws", Host: *addr, Path: *path}
	log.Printf("connecting to %s", u.String())

	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer c.Close()

	text := *message
	messageType := websocket.TextMessage
	if *binary {
		messageType = websocket.BinaryMessage
	}
	if *messageLen > 0 {
		textBytes := make([]byte, *messageLen)
		for i := 0; i < *messageLen; i++ {
			textBytes[i] = (*message)[i%(len(*message)-1)]
			if messageType == websocket.BinaryMessage {
				textBytes[i] = 0xef
			}
		}
		text = string(textBytes)
	}
	for {
		err := c.WriteMessage(messageType, []byte(text))
		if err != nil {
			log.Fatalf("write: %v", err)
			return
		}
		if *print {
			log.Println("write:", text)

		}

		receiveType, reader, err := c.NextReader()
		if err != nil {
			log.Println("read:", err)
			return
		}
		if receiveType != messageType {
			log.Println("received type != messageType")
			return

		}
		line := make([]byte, *messageLen+10)
		i := 0
		read := 0
		output := ""
		for err == nil {
			var n int
			n, err = reader.Read(line)
			if err == nil {
				if *print {
					log.Println("read :", i, n, string(line))
				}
				read += n
			}
			output += string(line[:n])
			i++
		}
		if string(output) != text {
			log.Println("output not equal text:", len(output), len(text))
		}
		if err != io.EOF {
			log.Println("reader read error:", err)
			return
		} else {
			if *print {

				log.Println("readlen:", read)
			}

		}

		time.Sleep(time.Second)
	}
}
