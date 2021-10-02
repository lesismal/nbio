package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"time"

	"github.com/pkg/profile"
	"github.com/lesismal/nbio/nbhttp"
	"github.com/lesismal/nbio/nbhttp/websocket"
)

var (
	onDataFrame = flag.Bool("UseOnDataFrame", false, "Server will use OnDataFrame api instead of OnMessage")
	disableEcho = flag.Bool("disable-echo", false, "disable echo")
	verbose     = flag.Bool("verbose", false, "verbose mode")
	ack 		= flag.Bool("ack", true, "send back an acknowledgement")

	connections   uint64
	msgReceived   uint64
	bytesReceived uint64
)

func newUpgrader() *websocket.Upgrader {
	u := websocket.NewUpgrader()
	u.OnOpen(func(c *websocket.Conn) {
		atomic.AddUint64(&connections, 1)
	})
	if *onDataFrame {
		u.OnDataFrame(func(c *websocket.Conn, messageType websocket.MessageType, fin bool, data []byte) {
			// echo
			if !*disableEcho {
				c.WriteFrame(messageType, true, fin, data)
			}
			atomic.AddUint64(&bytesReceived, uint64(len(data)))
			if fin {
				atomic.AddUint64(&msgReceived, 1)
			}
		})
	} else {
		u.OnMessage(func(c *websocket.Conn, messageType websocket.MessageType, data []byte) {
			// echo
			if !*disableEcho {
				c.WriteMessage(messageType, data)
			}
			atomic.AddUint64(&bytesReceived, uint64(len(data)))
			atomic.AddUint64(&msgReceived, 1)
			if *ack {
				c.WriteMessage(messageType, []byte("a"))
			}
		})
	}

	u.OnClose(func(c *websocket.Conn, err error) {
		if *verbose {
			fmt.Println("OnClose:", c.RemoteAddr().String(), err)
		}
		atomic.AddUint64(&connections, ^uint64(0))
	})
	return u
}

func onWebsocket(w http.ResponseWriter, r *http.Request) {
	// time.Sleep(time.Second * 5)
	upgrader := newUpgrader()
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		panic(err)
	}
	wsConn := conn.(*websocket.Conn)
	wsConn.SetReadDeadline(time.Time{})
	if *verbose {
		fmt.Println("OnOpen:", wsConn.RemoteAddr().String())
	}
}

func main() {
	flag.Parse()
	mux := &http.ServeMux{}
	mux.HandleFunc("/ws", onWebsocket)

	svr := nbhttp.NewServer(nbhttp.Config{
		Network: "tcp",
		Addrs:   []string{"localhost:8888"},
	}, mux, nil)

	defer profile.Start(profile.MemProfile).Stop()

	err := svr.Start()
	if err != nil {
		fmt.Printf("nbio.Start failed: %v\n", err)
		return
	}
	go func() {
		ticker := time.NewTicker(time.Second * 5)
		for {
			select {
			case <-ticker.C:
				fmt.Printf("connection %d data received %d\n", atomic.LoadUint64(&connections), atomic.LoadUint64(&bytesReceived))
			}

		}

	}()

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	svr.Shutdown(ctx)
}
