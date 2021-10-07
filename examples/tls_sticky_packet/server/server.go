package main

import (
	"fmt"
	"log"

	// "math/rand"
	"net"
	"net/http"
	_ "net/http/pprof"
	"time"

	"github.com/lesismal/llib/std/crypto/tls"
	"github.com/lesismal/nbio"
	ntls "github.com/lesismal/nbio/extension/tls"
)

func init() {
	go http.ListenAndServe("localhost:6060", nil)
}

func main() {
	cert, err := tls.X509KeyPair(rsaCertPEM, rsaKeyPEM)
	if err != nil {
		log.Fatalf("tls.X509KeyPair failed: %v", err)
	}
	tlsConfig := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: true,
	}
	tlsConfig.BuildNameToCertificate()

	g := nbio.NewGopher(nbio.Config{
		Network: "tcp",
		Addrs:   []string{"localhost:9999"},
	})

	g.OnOpen(ntls.WrapOpen(tlsConfig, false, func(c *nbio.Conn, tlsConn *tls.Conn) {
		log.Println("OnOpen:", c.RemoteAddr().String())
	}))
	g.OnClose(ntls.WrapClose(func(c *nbio.Conn, tlsConn *tls.Conn, err error) {
		log.Println("OnClose:", c.RemoteAddr().String())
	}))
	g.OnData(ntls.WrapData(func(c *nbio.Conn, tlsConn *tls.Conn, data []byte) {
		log.Printf("OnData: %v, data length: %v\n", c.RemoteAddr().String(), len(data))
		tlsConn.Write(data)
	}))

	err = g.Start()
	if err != nil {
		log.Fatalf("nbio.Start failed: %v\n", err)
		return
	}
	defer g.Stop()

	go runProxy("localhost:8888", "localhost:9999")

	g.Wait()
}

func stickyTunnel(clientConn *net.TCPConn, serverAddr string) {
	serverConn, dialErr := net.Dial("tcp", serverAddr)
	log.Printf("+ stickyTunnel: [%v -> %v]\n", clientConn.LocalAddr().String(), serverAddr)
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
			go stickyTunnel(conn, serverAddr)
		}
	}
}

var rsaCertPEM = []byte(`-----BEGIN CERTIFICATE-----
MIIDazCCAlOgAwIBAgIUJeohtgk8nnt8ofratXJg7kUJsI4wDQYJKoZIhvcNAQEL
BQAwRTELMAkGA1UEBhMCQVUxEzARBgNVBAgMClNvbWUtU3RhdGUxITAfBgNVBAoM
GEludGVybmV0IFdpZGdpdHMgUHR5IEx0ZDAeFw0yMDEyMDcwODIyNThaFw0zMDEy
MDUwODIyNThaMEUxCzAJBgNVBAYTAkFVMRMwEQYDVQQIDApTb21lLVN0YXRlMSEw
HwYDVQQKDBhJbnRlcm5ldCBXaWRnaXRzIFB0eSBMdGQwggEiMA0GCSqGSIb3DQEB
AQUAA4IBDwAwggEKAoIBAQCy+ZrIvwwiZv4bPmvKx/637ltZLwfgh3ouiEaTchGu
IQltthkqINHxFBqqJg44TUGHWthlrq6moQuKnWNjIsEc6wSD1df43NWBLgdxbPP0
x4tAH9pIJU7TQqbznjDBhzRbUjVXBIcn7bNknY2+5t784pPF9H1v7h8GqTWpNH9l
cz/v+snoqm9HC+qlsFLa4A3X9l5v05F1uoBfUALlP6bWyjHAfctpiJkoB9Yw1TJa
gpq7E50kfttwfKNkkAZIbib10HugkMoQJAs2EsGkje98druIl8IXmuvBIF6nZHuM
lt3UIZjS9RwPPLXhRHt1P0mR7BoBcOjiHgtSEs7Wk+j7AgMBAAGjUzBRMB0GA1Ud
DgQWBBQdheJv73XSOhgMQtkwdYPnfO02+TAfBgNVHSMEGDAWgBQdheJv73XSOhgM
QtkwdYPnfO02+TAPBgNVHRMBAf8EBTADAQH/MA0GCSqGSIb3DQEBCwUAA4IBAQBf
SKVNMdmBpD9m53kCrguo9iKQqmhnI0WLkpdWszc/vBgtpOE5ENOfHGAufHZve871
2fzTXrgR0TF6UZWsQOqCm5Oh3URsCdXWewVMKgJ3DCii6QJ0MnhSFt6+xZE9C6Hi
WhcywgdR8t/JXKDam6miohW8Rum/IZo5HK9Jz/R9icKDGumcqoaPj/ONvY4EUwgB
irKKB7YgFogBmCtgi30beLVkXgk0GEcAf19lHHtX2Pv/lh3m34li1C9eBm1ca3kk
M2tcQtm1G89NROEjcG92cg+GX3GiWIjbI0jD1wnVy2LCOXMgOVbKfGfVKISFt0b1
DNn00G8C6ttLoGU2snyk
-----END CERTIFICATE-----
`)

var rsaKeyPEM = []byte(`-----BEGIN RSA PRIVATE KEY-----
MIIEogIBAAKCAQEAsvmayL8MImb+Gz5rysf+t+5bWS8H4Id6LohGk3IRriEJbbYZ
KiDR8RQaqiYOOE1Bh1rYZa6upqELip1jYyLBHOsEg9XX+NzVgS4HcWzz9MeLQB/a
SCVO00Km854wwYc0W1I1VwSHJ+2zZJ2Nvube/OKTxfR9b+4fBqk1qTR/ZXM/7/rJ
6KpvRwvqpbBS2uAN1/Zeb9ORdbqAX1AC5T+m1soxwH3LaYiZKAfWMNUyWoKauxOd
JH7bcHyjZJAGSG4m9dB7oJDKECQLNhLBpI3vfHa7iJfCF5rrwSBep2R7jJbd1CGY
0vUcDzy14UR7dT9JkewaAXDo4h4LUhLO1pPo+wIDAQABAoIBAF6yWwekrlL1k7Xu
jTI6J7hCUesaS1yt0iQUzuLtFBXCPS7jjuUPgIXCUWl9wUBhAC8SDjWe+6IGzAiH
xjKKDQuz/iuTVjbDAeTb6exF7b6yZieDswdBVjfJqHR2Wu3LEBTRpo9oQesKhkTS
aFF97rZ3XCD9f/FdWOU5Wr8wm8edFK0zGsZ2N6r57yf1N6ocKlGBLBZ0v1Sc5ShV
1PVAxeephQvwL5DrOgkArnuAzwRXwJQG78L0aldWY2q6xABQZQb5+ml7H/kyytef
i+uGo3jHKepVALHmdpCGr9Yv+yCElup+ekv6cPy8qcmMBqGMISL1i1FEONxLcKWp
GEJi6QECgYEA3ZPGMdUm3f2spdHn3C+/+xskQpz6efiPYpnqFys2TZD7j5OOnpcP
ftNokA5oEgETg9ExJQ8aOCykseDc/abHerYyGw6SQxmDbyBLmkZmp9O3iMv2N8Pb
Nrn9kQKSr6LXZ3gXzlrDvvRoYUlfWuLSxF4b4PYifkA5AfsdiKkj+5sCgYEAzseF
XDTRKHHJnzxZDDdHQcwA0G9agsNj64BGUEjsAGmDiDyqOZnIjDLRt0O2X3oiIE5S
TXySSEiIkxjfErVJMumLaIwqVvlS4pYKdQo1dkM7Jbt8wKRQdleRXOPPN7msoEUk
Ta9ZsftHVUknPqblz9Uthb5h+sRaxIaE1llqDiECgYATS4oHzuL6k9uT+Qpyzymt
qThoIJljQ7TgxjxvVhD9gjGV2CikQM1Vov1JBigj4Toc0XuxGXaUC7cv0kAMSpi2
Y+VLG+K6ux8J70sGHTlVRgeGfxRq2MBfLKUbGplBeDG/zeJs0tSW7VullSkblgL6
nKNa3LQ2QEt2k7KHswryHwKBgENDxk8bY1q7wTHKiNEffk+aFD25q4DUHMH0JWti
fVsY98+upFU+gG2S7oOmREJE0aser0lDl7Zp2fu34IEOdfRY4p+s0O0gB+Vrl5VB
L+j7r9bzaX6lNQN6MvA7ryHahZxRQaD/xLbQHgFRXbHUyvdTyo4yQ1821qwNclLk
HUrhAoGAUtjR3nPFR4TEHlpTSQQovS8QtGTnOi7s7EzzdPWmjHPATrdLhMA0ezPj
Mr+u5TRncZBIzAZtButlh1AHnpN/qO3P0c0Rbdep3XBc/82JWO8qdb5QvAkxga3X
BpA7MNLxiqss+rCbwf3NbWxEMiDQ2zRwVoafVFys7tjmv6t2Xck=
-----END RSA PRIVATE KEY-----
`)
