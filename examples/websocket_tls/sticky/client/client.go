package main

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"flag"
	"log"
	"net/url"
	"sort"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/lesismal/nbio/taskpool"
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

	nn := []int{0, 1, 2, 128, 65535, 65537}
	for i := 1; i <= 130; i++ {
		nn = append(nn, 512*i)
	}
	clientNum := 4
	pool := taskpool.NewFixedPool(clientNum, 1024)
	wg := sync.WaitGroup{}
	success := []int{}

	conns := []*websocket.Conn{}
	for i := 0; i < clientNum; i++ {
		c, _, err := dialer.Dial(u.String(), nil)
		if err != nil {
			log.Fatal("dial:", err)
		}
		conns = append(conns, c)
		defer c.Close()
	}

	for i, l := range nn {
		wg.Add(1)
		n := l
		idx := i % clientNum
		pool.GoByIndex(idx, func() {
			c := conns[idx]
			defer wg.Done()
			data := make([]byte, n)
			for j := 0; j < n; j++ {
				data[j] = 'a' + byte(j%26)
			}

			err := c.WriteMessage(websocket.TextMessage, data)
			if err != nil {
				log.Fatalf("write length: %v, %v", n, err)
				return
			}

			typ, message, err := c.ReadMessage()
			if err != nil {
				log.Fatalf("read length: %v, %v", n, err)
				return
			}
			if typ != websocket.TextMessage || !bytes.Equal(message, data) {
				log.Fatalf("length: %v, typ: %v != %v, message != text: %v, %v", n, typ, websocket.TextMessage, len(message), string(message))
			}

			nr, err := rand.Read(data)
			if err != nil || nr != n {
				log.Fatalf("rand read failed length: %v, %v, %v", n, nr, err)
			}
			err = c.WriteMessage(websocket.BinaryMessage, data)
			if err != nil {
				log.Fatalf("write length: %v, %v", n, err)
				return
			}

			typ, message, err = c.ReadMessage()
			if typ != websocket.BinaryMessage || !bytes.Equal(message, data) {
				log.Fatalf("length: %v, typ: %v != %v, message != text: %v, %v", n, typ, websocket.TextMessage, len(message), string(message))
			}
			success = append(success, n)
			log.Println("success with data length:", n)
		})
	}
	wg.Wait()
	sort.Ints(success)
	log.Println("success all with data length:", success)
}
