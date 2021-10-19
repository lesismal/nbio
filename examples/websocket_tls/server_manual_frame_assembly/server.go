package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"time"

	"github.com/lesismal/llib/std/crypto/tls"
	"github.com/lesismal/nbio/examples/fixedbufferpool"
	"github.com/lesismal/nbio/nbhttp"
	"github.com/lesismal/nbio/nbhttp/websocket"
)

var (
	bufferPool       = fixedbufferpool.NewFixedBufferPool(2000, 1*1024*1024, time.Second*10)
	messagesReceived uint64
	bytesReceived    uint64
)

func newUpgrader() *websocket.Upgrader {
	u := websocket.NewUpgrader()

	onMessageFunc := func(c *websocket.Conn, data []byte) {
		c.WriteMessage(websocket.BinaryMessage, data)
		atomic.AddUint64(&messagesReceived, 1)
		atomic.AddUint64(&bytesReceived, uint64(len(data)))
	}
	u.OnDataFrame(func(c *websocket.Conn, messageType websocket.MessageType, fin bool, frameData []byte) {
		curBuf := c.Session()
		// frame == message
		if fin && curBuf == nil {
			onMessageFunc(c, frameData)
			return
		}
		if curBuf == nil {
			b, err := bufferPool.Get()
			if err != nil {
				c.WriteMessage(websocket.CloseMessage, []byte(fmt.Sprintf("%v", err)))
				return
			}
			curBuf = b
		}
		messageDataSoFar := curBuf.([]byte)
		if cap(messageDataSoFar) < len(messageDataSoFar)+len(frameData) {
			c.WriteMessage(websocket.CloseMessage, []byte("websocket message too large"))
			return
		}
		messageDataSoFar = append(messageDataSoFar, frameData...)
		if fin {
			onMessageFunc(c, messageDataSoFar)
			bufferPool.Put(messageDataSoFar)
			c.SetSession(nil)
		} else {
			c.SetSession(messageDataSoFar)
		}
	})

	u.OnClose(func(c *websocket.Conn, err error) {
		curBuf := c.Session()
		b, _ := curBuf.([]byte)
		if b != nil {
			bufferPool.Put(b)
		}
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
	fmt.Println("OnOpen:", wsConn.RemoteAddr().String())
}

func main() {
	flag.Parse()
	cert, err := tls.X509KeyPair(rsaCertPEM, rsaKeyPEM)
	if err != nil {
		log.Fatalf("tls.X509KeyPair failed: %v", err)
	}
	tlsConfig := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: true,
	}

	mux := &http.ServeMux{}
	mux.HandleFunc("/wss", onWebsocket)

	svr := nbhttp.NewServer(nbhttp.Config{
		Network:                 "tcp",
		AddrsTLS:                []string{"localhost:8888"},
		TLSConfig:               tlsConfig,
		ReleaseWebsocketPayload: true,
		Handler:                 mux,
	})

	err = svr.Start()
	if err != nil {
		fmt.Printf("nbio.Start failed: %v\n", err)
		return
	}
	go func() {
		t := time.NewTicker(time.Second * 5)
		for {
			select {
			case <-t.C:
				fmt.Printf("%d %d\n", atomic.LoadUint64(&messagesReceived), atomic.LoadUint64(&bytesReceived))
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
