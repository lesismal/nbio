# NBIO - NON-BLOCKING IO


[![Slack][1]][2]

[![Mentioned in Awesome Go][3]][4] [![MIT licensed][5]][6] [![Go Version][7]][8] [![Build Status][9]][10] [![Go Report Card][11]][12] [![Coverage Statusd][13]][14]

[1]: https://img.shields.io/badge/join-us%20on%20slack-gray.svg?longCache=true&logo=slack&colorB=green
[2]: https://join.slack.com/t/arpcnbio/shared_invite/zt-vh3g1z2v-qqoDp1hQ45fJZqwPrSz4~Q
[3]: https://awesome.re/mentioned-badge-flat.svg
[4]: https://github.com/avelino/awesome-go#networking
[5]: https://img.shields.io/badge/license-MIT-blue.svg
[6]: LICENSE
[7]: https://img.shields.io/badge/go-%3E%3D1.16-30dff3?style=flat-square&logo=go
[8]: https://github.com/lesismal/nbio
[9]: https://img.shields.io/github/actions/workflow/status/lesismal/nbio/autobahn.yml?branch=master&style=flat-square&logo=github-actions
[10]: https://github.com/lesismal/nbio/actions?query=workflow%3autobahn
[11]: https://goreportcard.com/badge/github.com/lesismal/nbio
[12]: https://goreportcard.com/report/github.com/lesismal/nbio
[13]: https://codecov.io/gh/lesismal/nbio/branch/master/graph/badge.svg
[14]: https://codecov.io/gh/lesismal/nbio
[15]: https://godoc.org/github.com/lesismal/nbio?status.svg
[16]: https://godoc.org/github.com/lesismal/nbio


## Contents

- [NBIO - NON-BLOCKING IO](#nbio---non-blocking-io)
	- [Contents](#contents)
	- [Features](#features)
		- [Cross Platform](#cross-platform)
		- [Protocols Supported](#protocols-supported)
		- [Interfaces](#interfaces)
	- [Quick Start](#quick-start)
	- [TCP Echo Examples](#tcp-echo-examples)
	- [UDP Echo Examples](#udp-echo-examples)
	- [TLS Examples](#tls-examples)
	- [HTTP Examples](#http-examples)
	- [HTTPS Examples](#https-examples)
	- [Websocket Examples](#websocket-examples)
	- [Websocket TLS Examples](#websocket-tls-examples)
	- [Use With Other STD Based Frameworkds](#use-with-other-std-based-frameworkds)
	- [Magics For HTTP and Websocket](#magics-for-http-and-websocket)
		- [Different IOMod](#different-iomod)
		- [Using Websocket With Std Server](#using-websocket-with-std-server)
	- [More Examples](#more-examples)
	- [Credits](#credits)

## Features
### Cross Platform
- [x] Linux: Epoll, both ET/LT(as default) supported
- [x] BSD(MacOS): Kqueue
- [x] Windows: Based on std net, for debugging only

### Protocols Supported
- [x] TCP/UDP/Unix Socket supported
- [x] TLS supported
- [x] HTTP/HTTPS 1.x supported
- [x] Websocket supported, [Passes the Autobahn Test Suite](https://lesismal.github.io/nbio/websocket/autobahn), `OnOpen/OnMessage/OnClose` order guaranteed

### Interfaces
- [x] Implements a non-blocking net.Conn(except windows)
- [x] SetDeadline/SetReadDeadline/SetWriteDeadline supported
- [x] Concurrent Write/Close supported(both nbio.Conn and nbio/nbhttp/websocket.Conn)


## Quick Start

```golang
package main

import (
	"log"

	"github.com/lesismal/nbio"
)

func main() {
	engine := nbio.NewEngine(nbio.Config{
		Network:            "tcp",//"udp", "unix"
		Addrs:              []string{":8888"},
		MaxWriteBufferSize: 6 * 1024 * 1024,
	})

	// hanlde new connection
	engine.OnOpen(func(c *nbio.Conn) {
		log.Println("OnOpen:", c.RemoteAddr().String())
	})
	// hanlde connection closed
	engine.OnClose(func(c *nbio.Conn, err error) {
		log.Println("OnClose:", c.RemoteAddr().String(), err)
	})
	// handle data
	engine.OnData(func(c *nbio.Conn, data []byte) {
		c.Write(append([]byte{}, data...))
	})

	err := engine.Start()
	if err != nil {
		log.Fatalf("nbio.Start failed: %v\n", err)
		return
	}
	defer engine.Stop()

	<-make(chan int)
}
```

## TCP Echo Examples

- [echo-server](https://github.com/lesismal/nbio_examples/blob/master/echo/server/server.go)
- [echo-client](https://github.com/lesismal/nbio_examples/blob/master/echo/client/client.go)

## UDP Echo Examples

- [udp-server](https://github.com/lesismal/nbio-examples/blob/master/udp/server/server.go)
- [udp-client](https://github.com/lesismal/nbio-examples/blob/master/udp/client/client.go)

## TLS Examples

- [tls-server](https://github.com/lesismal/nbio_examples/blob/master/tls/server/server.go)
- [tls-client](https://github.com/lesismal/nbio_examples/blob/master/tls/client/client.go)

## HTTP Examples

- [http-server](https://github.com/lesismal/nbio_examples/blob/master/http/server/server.go)
- [http-client](https://github.com/lesismal/nbio_examples/blob/master/http/client/client.go)

## HTTPS Examples

- [http-tls_server](https://github.com/lesismal/nbio_examples/blob/master/http/server_tls/server.go)
- visit: https://localhost:8888/echo

## Websocket Examples

- [websocket-server](https://github.com/lesismal/nbio_examples/blob/master/websocket/server/server.go)
- [websocket-client](https://github.com/lesismal/nbio_examples/blob/master/websocket/client/client.go)

## Websocket TLS Examples

- [websocket-tls-server](https://github.com/lesismal/nbio_examples/blob/master/websocket_tls/server/server.go)
- [websocket-tls-client](https://github.com/lesismal/nbio_examples/blob/master/websocket_tls/client/client.go)

## Use With Other STD Based Frameworkds

- [echo-http-and-websocket-server](https://github.com/lesismal/nbio_examples/blob/master/http_with_other_frameworks/echo_server/echo_server.go)
- [gin-http-and-websocket-server](https://github.com/lesismal/nbio_examples/blob/master/http_with_other_frameworks/gin_server/gin_server.go)
- [go-chi-http-and-websocket-server](https://github.com/lesismal/nbio_examples/blob/master/http_with_other_frameworks/go-chi_server/go-chi_server.go)

## Magics For HTTP and Websocket

### Different IOMod

| IOMod            |                                                                                                                  Remarks                                                                                                                   |
| ---------------- | :----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------: |
| IOModNonBlocking |                                                         There's no difference between this IOMod and the old version with no IOMod. All the connections will be handled by poller.                                                         |
| IOModBlocking    | All the connections will be handled by at least one goroutine, for websocket, we can set Upgrader.BlockingModAsyncWrite=true to handle writting with a separated goroutine and then avoid Head-of-line blocking on broadcasting scenarios. |
| IOModMixed       |                   We set the Engine.MaxBlockingOnline, if the online num is smaller than it, the new connection will be handled by single goroutine as IOModBlocking, else the new connection will be handled by poller.                   |

The `IOModBlocking` aims to improve the performance for low online service, it runs faster than std. 
The `IOModMixed` aims to keep a balance between performance and cpu/mem cost in different scenarios: when there are not too many online connections, it performs better than std, or else it can serve lots of online connections and keep healthy.

### Using Websocket With Std Server

```golang
package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/lesismal/nbio/nbhttp/websocket"
)

func echo(w http.ResponseWriter, r *http.Request) {
	u := websocket.NewUpgrader()
	u.OnMessage(func(c *websocket.Conn, mt websocket.MessageType, data []byte) {
		c.WriteMessage(mt, data)
	})
	_, err := u.Upgrade(w, r, nil)
	if err != nil {
		log.Print("upgrade:", err)
		return
	}
}

func main() {
	mux := &http.ServeMux{}
	mux.HandleFunc("/ws", echo)
	server := http.Server{
		Addr:    "localhost:8080",
		Handler: mux,
	}
	fmt.Println("server exit:", server.ListenAndServe())
}
```

## More Examples

- [nbio-examples](https://github.com/lesismal/nbio-examples)


## Credits
- [xtaci/gaio](https://github.com/xtaci/gaio)
- [gorilla/websocket](https://github.com/gorilla/websocket)
- [crossbario/autobahn](https://github.com/crossbario)
