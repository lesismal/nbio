package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

var compression = flag.Bool("compression", false, "allow compression")
var filename1 = flag.String("file1", "", "file to upload")
var filename2 = flag.String("file2", "", "file to upload")

func main() {
	flag.Parse()

	u := url.URL{Scheme: "ws", Host: "localhost:8888", Path: "/ws"}
	dialer := websocket.DefaultDialer
	if *compression {
		dialer.EnableCompression = true
	}

	c, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer c.Close()

	i := 0
	data1, err := ioutil.ReadFile(*filename1)
	if err != nil {
		panic(fmt.Sprintf("failed to read file1: %v", err))
	}
	data2, err := ioutil.ReadFile(*filename2)
	if err != nil {
		panic(fmt.Sprintf("failed to read file2: %v", err))
	}
	for {
		i++
		err := c.WriteMessage(websocket.BinaryMessage, data1)
		if err != nil {
			log.Println("ReadMessage failed:", err)
			return
		}
		err = c.WriteMessage(websocket.BinaryMessage, data2)
		if err != nil {
			log.Println("ReadMessage failed:", err)
			return
		}
		time.Sleep(time.Second)
	}
}
