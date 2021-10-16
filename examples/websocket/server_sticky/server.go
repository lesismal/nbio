package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/lesismal/nbio/nbhttp"
	"github.com/lesismal/nbio/nbhttp/websocket"
)

var (
	svr *nbhttp.Server
)

func newUpgrader() *websocket.Upgrader {
	u := websocket.NewUpgrader()
	u.OnMessage(func(c *websocket.Conn, messageType websocket.MessageType, data []byte) {
		// echo
		c.WriteMessage(messageType, data)
		fmt.Println("OnMessage:", messageType, string(data))
	})
	u.OnClose(func(c *websocket.Conn, err error) {
		fmt.Println("OnClose:", c.RemoteAddr().String(), err)
	})

	return u
}
func onWebsocket(w http.ResponseWriter, r *http.Request) {
	upgrader := newUpgrader()
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		panic(err)
	}
	wsConn := conn.(*websocket.Conn)
	fmt.Println("OnOpen:", wsConn.RemoteAddr().String())
}

func main() {
	mux := &http.ServeMux{}
	mux.HandleFunc("/ws", onWebsocket)

	svr = nbhttp.NewServer(nbhttp.Config{
		Network: "tcp",
		Addrs:   []string{"localhost:28001"},
		Handler: mux,
	})

	err := svr.Start()
	if err != nil {
		fmt.Printf("nbio.Start failed: %v\n", err)
		return
	}
	defer svr.Stop()

	go runProxy("localhost:28000", "localhost:28001")

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
	log.Println("exit")
}

func fuzzTunnel(clientConn *net.TCPConn, serverAddr string) {
	serverConn, dialErr := net.Dial("tcp", serverAddr)
	log.Printf("+ fuzzTunnel: [%v -> %v]\n", clientConn.LocalAddr().String(), serverAddr)
	if dialErr == nil {
		c2sCor := func() {
			defer func() {
				recover()
			}()

			var buf = make([]byte, 4096)
			for i := 0; true; i++ {
				nread, err := clientConn.Read(buf)
				if err != nil {
					clientConn.Close()
					serverConn.Close()
					break
				}
				tmp := buf[:nread]
				// for len(tmp) > 0 {
				// 	nSend := int(rand.Intn(len(tmp)) + 1)
				// 	sendBuf := tmp[:nSend]
				// 	_, err = serverConn.Write(sendBuf)
				// 	tmp = tmp[nSend:]
				// 	if err != nil {
				// 		clientConn.Close()
				// 		serverConn.Close()
				// 		return
				// 	}
				// 	time.Sleep(time.Second / 1000)
				// }
				for j := 0; j < len(tmp); j++ {
					_, err := serverConn.Write([]byte{tmp[j]})
					if err != nil {
						clientConn.Close()
						serverConn.Close()
						return
					}
					time.Sleep(time.Second / 1000)
				}
			}
		}

		s2cCor := func() {
			defer func() {
				recover()
			}()

			var buf = make([]byte, 4096)

			for i := 0; true; i++ {
				nread, err := serverConn.Read(buf)
				if err != nil {
					clientConn.Close()
					serverConn.Close()
					break
				}

				tmp := buf[:nread]
				// for len(tmp) > 0 {
				// 	nSend := int(rand.Intn(len(tmp)) + 1)
				// 	sendBuf := tmp[:nSend]
				// 	_, err = clientConn.Write(sendBuf)
				// 	tmp = tmp[nSend:]
				// 	if err != nil {
				// 		clientConn.Close()
				// 		serverConn.Close()
				// 		return
				// 	}
				// 	time.Sleep(time.Second / 1000)
				// }
				for j := 0; j < len(tmp); j++ {
					_, err := clientConn.Write([]byte{tmp[j]})
					if err != nil {
						clientConn.Close()
						serverConn.Close()
						return
					}
					time.Sleep(time.Second / 1000)
				}
			}
		}

		go c2sCor()
		go s2cCor()
	} else {
		clientConn.Close()
	}
}

func runProxy(agentAddr string, serverAddr string) {
	tcpAddr, err := net.ResolveTCPAddr("tcp4", agentAddr)
	if err != nil {
		fmt.Println("ResolveTCPAddr Error: ", err)
		return
	}

	listener, err2 := net.ListenTCP("tcp", tcpAddr)
	if err2 != nil {
		fmt.Println("ListenTCP Error: ", err2)
		return
	}

	defer listener.Close()

	fmt.Println(fmt.Sprintf("proxy running on: [%s -> %s]", agentAddr, serverAddr))
	for {
		conn, err := listener.AcceptTCP()

		if err != nil {
			fmt.Println("AcceptTCP Error: ", err2)
		} else {
			go fuzzTunnel(conn, serverAddr)
		}
	}
}
