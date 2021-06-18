package proxy

import (
	"fmt"
	"log"
	"net"
	"time"
)

func stickyTunnel(clientConn *net.TCPConn, dst string) {
	serverConn, dailErr := net.Dial("tcp", dst)
	log.Printf("+ stickyTunnel: [%v -> %v]\n", clientConn.LocalAddr().String(), dst)
	if dailErr == nil {
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

func Run(src string, dst string) {
	tcpAddr, err := net.ResolveTCPAddr("tcp4", src)
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

	fmt.Println(fmt.Sprintf("proxy running on: [%s -> %s]", src, dst))
	for {
		conn, err := listener.AcceptTCP()

		if err != nil {
			fmt.Println("AcceptTCP Error: ", err2)
		} else {
			go stickyTunnel(conn, dst)
		}
	}
}
