package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/lesismal/nbio/nbhttp"
	"github.com/lesismal/nbio/nbhttp/websocket"
)

var (
	keepaliveTime    = time.Second * 5
	keepaliveTimeout = keepaliveTime + time.Second*3
)

var clientMgr *ClientMgr

// ClientMgr .
type ClientMgr struct {
	mux           sync.Mutex
	chStop        chan struct{}
	clients       map[*websocket.Conn]struct{}
	keepaliveTime time.Duration
}

// NewClientMgr .
func NewClientMgr(keepaliveTime time.Duration) *ClientMgr {
	return &ClientMgr{
		chStop:        make(chan struct{}),
		clients:       map[*websocket.Conn]struct{}{},
		keepaliveTime: keepaliveTime,
	}
}

// Add .
func (cm *ClientMgr) Add(c *websocket.Conn) {
	cm.mux.Lock()
	defer cm.mux.Unlock()
	cm.clients[c] = struct{}{}
}

// Delete .
func (cm *ClientMgr) Delete(c *websocket.Conn) {
	cm.mux.Lock()
	defer cm.mux.Unlock()
	delete(cm.clients, c)
}

// Run .
func (cm *ClientMgr) Run() {
	ticker := time.NewTicker(cm.keepaliveTime)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			func() {
				cm.mux.Lock()
				defer cm.mux.Unlock()
				for wsConn := range cm.clients {
					wsConn.WriteMessage(websocket.PingMessage, nil)
				}
				fmt.Printf("keepalive: ping %v clients\n", len(cm.clients))
			}()
		case <-cm.chStop:
			return
		}
	}
}

// Stop .
func (cm *ClientMgr) Stop() {
	close(cm.chStop)
}

func onWebsocket(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.NewUpgrader()
	upgrader.OnMessage(func(c *websocket.Conn, messageType websocket.MessageType, data []byte) {
		// echo
		c.WriteMessage(messageType, data)

		// update read deadline
		c.SetReadDeadline(time.Now().Add(keepaliveTimeout))
	})
	upgrader.SetPongHandler(func(c *websocket.Conn, s string) {
		// update read deadline
		c.SetReadDeadline(time.Now().Add(keepaliveTimeout))
	})

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		panic(err)
	}
	wsConn := conn.(*websocket.Conn)

	// init read deadline
	wsConn.SetReadDeadline(time.Now().Add(keepaliveTimeout))

	clientMgr.Add(wsConn)
	wsConn.OnClose(func(c *websocket.Conn, err error) {
		clientMgr.Delete(c)
	})
}

func main() {
	clientMgr = NewClientMgr(keepaliveTime)
	go clientMgr.Run()
	defer clientMgr.Stop()

	mux := &http.ServeMux{}
	mux.HandleFunc("/ws", onWebsocket)

	svr := nbhttp.NewServer(nbhttp.Config{
		Network: "tcp",
		Addrs:   []string{"localhost:8888"},
		Handler: mux,
	})

	err := svr.Start()
	if err != nil {
		fmt.Printf("nbio.Start failed: %v\n", err)
		return
	}

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	svr.Shutdown(ctx)
}
