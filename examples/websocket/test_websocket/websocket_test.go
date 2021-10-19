package test

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gwebsocket "github.com/gorilla/websocket"
	"github.com/lesismal/nbio/nbhttp"
	"github.com/lesismal/nbio/nbhttp/websocket"
)

var addr = flag.String("addr", "localhost:28004", "http service address")
var (
	svr *nbhttp.Server
)

func onWebsocket(cancelFunc context.CancelFunc, maxCount int, w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.NewUpgrader()
	count := int32(0)
	upgrader.OnMessage(func(c *websocket.Conn, messageType websocket.MessageType, data []byte) {
		// echo
		fmt.Println("OnMessage:", messageType, string(data))
		if int(atomic.AddInt32(&count, 1)) == maxCount {
			cancelFunc()
		}
		c.SetReadDeadline(time.Now().Add(time.Second * 60))
	})
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		panic(err)
	}
	wsConn := conn.(*websocket.Conn)
	wsConn.OnClose(func(c *websocket.Conn, err error) {
		fmt.Println("OnClose:", c.RemoteAddr().String(), err)
	})
	fmt.Println("OnOpen:", wsConn.RemoteAddr().String())
}

func server(ctx context.Context, cancelFunc context.CancelFunc, readCount int) {
	mux := &http.ServeMux{}
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		onWebsocket(cancelFunc, readCount, w, r)
	})

	svr = nbhttp.NewServer(nbhttp.Config{
		Network: "tcp",
		Addrs:   []string{*addr},
		Handler: mux,
	})

	err := svr.Start()
	if err != nil {
		fmt.Printf("nbio.Start failed: %v\n", err)
		return
	}
	defer svr.Stop()
	<-ctx.Done()
	log.Println("server shutdown")
}

func client(ctx context.Context, count int) {
	flag.Parse()

	u := url.URL{Scheme: "ws", Host: *addr, Path: "/ws"}
	log.Printf("connecting to %s", u.String())

	dialer := gwebsocket.DefaultDialer
	c, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer c.Close()

	for i := 0; i < count; i++ {
		text := "hello world"
		err := c.WriteMessage(gwebsocket.TextMessage, []byte(text))
		if err != nil {
			log.Fatalf("write: %v", err)
			return
		}
		log.Println("write:", text)
	}
	<-ctx.Done()
}

func TestWebsocketTwoRead(t *testing.T) {
	ctx, cancelFunc := context.WithCancel(context.Background())
	waitGrp := sync.WaitGroup{}
	waitGrp.Add(1)
	go func() {
		server(ctx, cancelFunc, 10)
		waitGrp.Done()
	}()
	time.Sleep(time.Second)
	log.Println("done sleep")
	client(ctx, 10)
	waitGrp.Done()
}
