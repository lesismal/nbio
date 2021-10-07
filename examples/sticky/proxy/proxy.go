package proxy

import (
	"fmt"
	"log"
	"net"
	"time"
)

func stickyTunnel(clientConn *net.TCPConn, dst string, interval time.Duration, f func(max int) int) {
	serverConn, dialErr := net.Dial("tcp", dst)
	log.Printf("+ stickyTunnel: [%v -> %v]\n", clientConn.LocalAddr().String(), dst)
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
				for len(tmp) > 0 {
					n := f(len(tmp))
					if n == 0 {
						n = len(tmp)
					}
					_, err := serverConn.Write(tmp[:n])
					if err != nil {
						clientConn.Close()
						serverConn.Close()
						return
					}
					if interval > 0 {
						time.Sleep(interval)
					}
					tmp = tmp[n:]
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
				for len(tmp) > 0 {
					n := f(len(tmp))
					if n == 0 {
						n = len(tmp)
					}
					_, err := clientConn.Write(tmp[:n])
					if err != nil {
						clientConn.Close()
						serverConn.Close()
						return
					}
					if interval > 0 {
						time.Sleep(interval)
					}
					tmp = tmp[n:]
				}
			}
		}

		go c2sCor()
		go s2cCor()
	} else {
		clientConn.Close()
	}
}

// Run .
func Run(src string, dst string, interval time.Duration, f func(max int) int) {
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
			go stickyTunnel(conn, dst, interval, f)
		}
	}
}
