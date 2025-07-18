# NBIO - NON-BLOCKING IO


<!-- [![Slack][1]][2] -->

[![Mentioned in Awesome Go][3]][4] [![MIT licensed][5]][6] [![Go Version][7]][8] [![Build Status][9]][10] [![Go Report Card][11]][12]

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
	- [Examples](#examples)
		- [TCP Echo Examples](#tcp-echo-examples)
		- [UDP Echo Examples](#udp-echo-examples)
		- [TLS Examples](#tls-examples)
		- [HTTP Examples](#http-examples)
		- [HTTPS Examples](#https-examples)
		- [Websocket Examples](#websocket-examples)
		- [Websocket TLS Examples](#websocket-tls-examples)
		- [Use With Other STD Based Frameworkds](#use-with-other-std-based-frameworkds)
		- [More Examples](#more-examples)
	- [1M Websocket Connections Benchmark](#1m-websocket-connections-benchmark)
	- [Magics For HTTP and Websocket](#magics-for-http-and-websocket)
		- [Different IOMod](#different-iomod)
		- [Using Websocket With Std Server](#using-websocket-with-std-server)
	- [Credits](#credits)
	- [Contributors](#contributors)
	- [Star History](#star-history)

## Features
### Cross Platform
- [x] Linux: Epoll with LT/ET/ET+ONESHOT supported, LT as default
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

	// handle new connection
	engine.OnOpen(func(c *nbio.Conn) {
		log.Println("OnOpen:", c.RemoteAddr().String())
	})
	// handle connection closed
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

## Examples
### TCP Echo Examples

- [echo-server](https://github.com/lesismal/nbio_examples/blob/master/echo/server/server.go)
- [echo-client](https://github.com/lesismal/nbio_examples/blob/master/echo/client/client.go)

### UDP Echo Examples

- [udp-server](https://github.com/lesismal/nbio-examples/blob/master/udp/server/server.go)
- [udp-client](https://github.com/lesismal/nbio-examples/blob/master/udp/client/client.go)

### TLS Examples

- [tls-server](https://github.com/lesismal/nbio_examples/blob/master/tls/server/server.go)
- [tls-client](https://github.com/lesismal/nbio_examples/blob/master/tls/client/client.go)

### HTTP Examples

- [http-server](https://github.com/lesismal/nbio_examples/blob/master/http/server/server.go)
- [http-client](https://github.com/lesismal/nbio_examples/blob/master/http/client/client.go)

### HTTPS Examples

- [http-tls_server](https://github.com/lesismal/nbio_examples/blob/master/http/server_tls/server.go)
- visit: https://localhost:8888/echo

### Websocket Examples

- [websocket-server](https://github.com/lesismal/nbio_examples/blob/master/websocket/server/server.go)
- [websocket-client](https://github.com/lesismal/nbio_examples/blob/master/websocket/client/client.go)

### Websocket TLS Examples

- [websocket-tls-server](https://github.com/lesismal/nbio_examples/blob/master/websocket_tls/server/server.go)
- [websocket-tls-client](https://github.com/lesismal/nbio_examples/blob/master/websocket_tls/client/client.go)

### Use With Other STD Based Frameworkds

- [echo-http-and-websocket-server](https://github.com/lesismal/nbio_examples/blob/master/http_with_other_frameworks/echo_server/echo_server.go)
- [gin-http-and-websocket-server](https://github.com/lesismal/nbio_examples/blob/master/http_with_other_frameworks/gin_server/gin_server.go)
- [go-chi-http-and-websocket-server](https://github.com/lesismal/nbio_examples/blob/master/http_with_other_frameworks/go-chi_server/go-chi_server.go)

### More Examples

- [nbio-examples](https://github.com/lesismal/nbio-examples)



## 1M Websocket Connections Benchmark

For more details: [go-websocket-benchmark](https://github.com/lesismal/go-websocket-benchmark)

```sh
# lsb_release -a
LSB Version:    core-11.1.0ubuntu2-noarch:security-11.1.0ubuntu2-noarch
Distributor ID: Ubuntu
Description:    Ubuntu 20.04.6 LTS
Release:        20.04
Codename:       focal

# free
              total        used        free      shared  buff/cache   available
Mem:       24969564    15656352     3422212        1880     5891000     8899604
Swap:             0           0           0

# cat /proc/cpuinfo | grep processor
processor       : 0
processor       : 1
processor       : 2
processor       : 3
processor       : 4
processor       : 5
processor       : 6
processor       : 7
processor       : 8
processor       : 9
processor       : 10
processor       : 11
processor       : 12
processor       : 13
processor       : 14
processor       : 15


# taskset
run nbio_nonblocking server on cpu 0-7

--------------------------------------------------------------
BenchType  : BenchEcho
Framework  : nbio_nonblocking
TPS        : 104713
EER        : 280.33
Min        : 56.90us
Avg        : 95.36ms
Max        : 2.29s
TP50       : 62.82ms
TP75       : 65.38ms
TP90       : 89.38ms
TP95       : 409.55ms
TP99       : 637.95ms
Used       : 47.75s
Total      : 5000000
Success    : 5000000
Failed     : 0
Conns      : 1000000
Concurrency: 10000
Payload    : 1024
CPU Min    : 0.00%
CPU Avg    : 373.53%
CPU Max    : 602.33%
MEM Min    : 978.70M
MEM Avg    : 979.88M
MEM Max    : 981.14M
--------------------------------------------------------------
```


## Magics For HTTP and Websocket

### Different IOMod

| IOMod            |                                                                                                                  Remarks                                                                                                                  |
| ---------------- | :---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------: |
| IOModNonBlocking |                                                        There's no difference between this IOMod and the old version with no IOMod. All the connections will be handled by poller.                                                         |
| IOModBlocking    | All the connections will be handled by at least one goroutine, for websocket, we can set Upgrader.BlockingModAsyncWrite=true to handle writing with a separated goroutine and then avoid Head-of-line blocking on broadcasting scenarios. |
| IOModMixed       |                  We set the Engine.MaxBlockingOnline, if the online num is smaller than it, the new connection will be handled by single goroutine as IOModBlocking, else the new connection will be handled by poller.                   |

The `IOModBlocking` aims to improve the performance for low online service, it runs faster than std. 
The `IOModMixed` aims to keep a balance between performance and cpu/mem cost in different scenarios: when there are not too many online connections, it performs better than std, or else it can serve lots of online connections and keep healthy.

### Using Websocket With Std Server

```golang
package main

import (
	"fmt"
	"net/http"

	"github.com/lesismal/nbio/nbhttp/websocket"
)

var (
	upgrader = newUpgrader()
)

func newUpgrader() *websocket.Upgrader {
	u := websocket.NewUpgrader()
	u.OnOpen(func(c *websocket.Conn) {
		// echo
		fmt.Println("OnOpen:", c.RemoteAddr().String())
	})
	u.OnMessage(func(c *websocket.Conn, messageType websocket.MessageType, data []byte) {
		// echo
		fmt.Println("OnMessage:", messageType, string(data))
		c.WriteMessage(messageType, data)
	})
	u.OnClose(func(c *websocket.Conn, err error) {
		fmt.Println("OnClose:", c.RemoteAddr().String(), err)
	})
	return u
}

func onWebsocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		panic(err)
	}
	fmt.Println("Upgraded:", conn.RemoteAddr().String())
}

func main() {
	mux := &http.ServeMux{}
	mux.HandleFunc("/ws", onWebsocket)
	server := http.Server{
		Addr:    "localhost:8080",
		Handler: mux,
	}
	fmt.Println("server exit:", server.ListenAndServe())
}
```


## Credits
- [xtaci/gaio](https://github.com/xtaci/gaio)
- [gorilla/websocket](https://github.com/gorilla/websocket)
- [crossbario/autobahn](https://github.com/crossbario)


## Contributors
Thanks Everyone:
- [acgreek](https://github.com/acgreek)
- [acsecureworks](https://github.com/acsecureworks)
- [arunsathiya](https://github.com/arunsathiya)
- [byene0923](https://github.com/byene0923)
- [guonaihong](https://github.com/guonaihong)
- [isletnet](https://github.com/isletnet)
- [liwnn](https://github.com/liwnn)
- [manjun21](https://github.com/manjun21)
- [maxkidd](https://github.com/maxkidd)
- [om26er](https://github.com/om26er)
- [rfyiamcool](https://github.com/rfyiamcool)
- [sunny352](https://github.com/sunny352)
- [sunvim](https://github.com/sunvim)
- [wuqinqiang](https://github.com/wuqinqiang)
- [wziww](https://github.com/wziww)
- [yicixin](https://github.com/yicixin)
- [youzhixiaomutou](https://github.com/youzhixiaomutou)
- [zbh255](https://github.com/zbh255)
- [IceflowRE](https://github.com/IceflowRE)
- [Jourmey](https://github.com/Jourmey)
- [YanKawaYu](https://github.com/YanKawaYu)


## Star History

[![Star History Chart](https://api.star-history.com/svg?repos=lesismal/nbio&type=Date)](https://star-history.com/#lesismal/nbio&Date)
