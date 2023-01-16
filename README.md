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
  - [Installation](#installation)
  - [Quick Start](#quick-start)
  - [API Examples](#api-examples)
    - [New Engine For Server-Side](#new-gopher-for-server-side)
    - [New Engine For Client-Side](#new-gopher-for-client-side)
    - [Start Engine](#start-gopher)
    - [Custom Other Config For Engine](#custom-other-config-for-gopher)
    - [SetDeadline/SetReadDeadline/SetWriteDeadline](#setdeadlinesetreaddeadlinesetwritedeadline)
    - [Bind User Session With Conn](#bind-user-session-with-conn)
    - [Writev / Batch Write](#writev--batch-write)
    - [Handle New Connection](#handle-new-connection)
    - [Handle Disconnected](#handle-disconnected)
    - [Handle Data](#handle-data)
    - [Handle Memory Allocation/Free For Reading](#handle-memory-allocationfree-for-reading)
    - [Handle Conn Before Read](#handle-conn-before-read)
    - [Handle Conn After Read](#handle-conn-after-read)
    - [Handle Conn Before Write](#handle-conn-before-write)
  - [Echo Examples](#echo-examples)
  - [TLS Examples](#tls-examples)
  - [HTTP Examples](#http-examples)
  - [HTTPS Examples](#https-examples)
  - [Websocket Examples](#websocket-examples)
  - [Websocket TLS Examples](#websocket-tls-examples)
  - [Websocket 1M Connections Examples](#websocket-1m-connections-examples)
  - [Use With Other STD Based Frameworkds](#use-with-other-std-based-frameworkds)
  - [More Examples](#more-examples)
  

## Features
### Cross Platform
- [x] Linux: Epoll, both ET/LT(as default) supported
- [x] BSD(MacOS): Kqueue
- [x] Windows: Based on std net, for debugging only

### Protocols Supported
- [x] TCP/UDP supported
- [x] TLS supported
- [x] HTTP/HTTPS 1.x supported
- [x] Websocket supported, [Passes the Autobahn Test Suite](https://lesismal.github.io/nbio/websocket/autobahn), `OnOpen/OnMessage/OnClose` order guaranteed

### Interface
- [x] Implements a non-blocking net.Conn(except windows)
- [x] Concurrent Write/Close supported(both nbio.Conn and nbio/nbhttp/websocket.Conn)
- [x] Writev supported


## Installation

1. Get and install nbio

```sh
$ go get -u github.com/lesismal/nbio
```

2. Import in your code:

```go
import "github.com/lesismal/nbio"
```


## Quick Start
 
- start a server

```go
import "github.com/lesismal/nbio"

g := nbio.NewEngine(nbio.Config{
    Network: "tcp",
    Addrs:   []string{"localhost:8888"},
})

// echo
g.OnData(func(c *nbio.Conn, data []byte) {
    c.Write(append([]byte{}, data...))
})

err := g.Start()
if err != nil {
    panic(err)
}
// ...
```

- start a client

```go
import "github.com/lesismal/nbio"

g := nbio.NewEngine(nbio.Config{})

g.OnData(func(c *nbio.Conn, data []byte) {
    // ...
})

err := g.Start()
if err != nil {
    fmt.Printf("Start failed: %v\n", err)
}
defer g.Stop()

c, err := nbio.Dial("tcp", addr)
if err != nil {
    fmt.Printf("Dial failed: %v\n", err)
}
g.AddConn(c)

buf := make([]byte, 1024)
c.Write(buf)
// ...
```

## API Examples

### New Engine For Server-Side
```golang
g := nbio.NewEngine(nbio.Config{
    Network: "tcp",
    Addrs:   []string{"localhost:8888"},
})
``` 

### New Engine For Client-Side
```golang
g := nbio.NewEngine(nbio.Config{})
``` 

### Start Engine
```golang
err := g.Start()
if err != nil {
    fmt.Printf("Start failed: %v\n", err)
}
defer g.Stop()
```

### Custom Other Config For Engine
```golang
conf := nbio.Config struct {
    // Name describes your gopher name for logging, it's set to "NB" by default
    Name: "NB",

    // MaxLoad represents the max online num, it's set to 10k by default
    MaxLoad: 1024 * 10, 

    // NPoller represents poller goroutine num, it's set to runtime.NumCPU() by default
    NPoller: runtime.NumCPU(),

    // ReadBufferSize represents buffer size for reading, it's set to 16k by default
    ReadBufferSize: 1024 * 16,

    // MaxWriteBufferSize represents max write buffer size for Conn, it's set to 1m by default.
    // if the connection's Send-Q is full and the data cached by nbio is 
    // more than MaxWriteBufferSize, the connection would be closed by nbio.
    MaxWriteBufferSize uint32

    // LockListener represents listener's goroutine to lock thread or not, it's set to false by default.
	LockListener bool

    // LockPoller represents poller's goroutine to lock thread or not.
    LockPoller bool
}
```

### SetDeadline/SetReadDeadline/SetWriteDeadline
```golang
var c *nbio.Conn = ...
c.SetDeadline(time.Now().Add(time.Second * 10))
c.SetReadDeadline(time.Now().Add(time.Second * 10))
c.SetWriteDeadline(time.Now().Add(time.Second * 10))
```

### Bind User Session With Conn
```golang
var c *nbio.Conn = ...
var session *YourSessionType = ... 
c.SetSession(session)
```

```golang
var c *nbio.Conn = ...
session := c.Session().(*YourSessionType)
```

### Writev / Batch Write
```golang
var c *nbio.Conn = ...
var data [][]byte = ...
c.Writev(data)
```

### Handle New Connection
```golang
g.OnOpen(func(c *Conn) {
    // ...
    c.SetReadDeadline(time.Now().Add(time.Second*30))
})
```

### Handle Disconnected
```golang
g.OnClose(func(c *Conn) {
    // clear sessions from user layer
})
```

### Handle Data
```golang
g.OnData(func(c *Conn, data []byte) {
    // decode data
    // ...
})
```

### Handle Memory Allocation/Free For Reading
```golang
import "sync"

var memPool = sync.Pool{
    New: func() interface{} {
        return make([]byte, yourSize)
    },
}

g.OnReadBufferAlloc(func(c *Conn) []byte {
    return memPool.Get().([]byte)
})
g.OnReadBufferFree(func(c *Conn, b []byte) {
    memPool.Put(b)
})
```

### Handle Conn Before Read
```golang
// BeforeRead registers callback before syscall.Read
// the handler would be called only on windows
g.OnData(func(c *Conn, data []byte) {
    c.SetReadDeadline(time.Now().Add(time.Second*30))
})
```

### Handle Conn After Read
```golang
// AfterRead registers callback after syscall.Read
// the handler would be called only on *nix
g.BeforeRead(func(c *Conn) {
    c.SetReadDeadline(time.Now().Add(time.Second*30))
})
```

### Handle Conn Before Write
```golang
g.OnData(func(c *Conn, data []byte) {
    c.SetWriteDeadline(time.Now().Add(time.Second*5))
})
```

## Echo Examples

- [echo-server](https://github.com/lesismal/nbio_examples/blob/master/echo/server/server.go)
- [echo-client](https://github.com/lesismal/nbio_examples/blob/master/echo/client/client.go)

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

## Websocket 1M Connections Examples

- [websocket-1m-connections-server](https://github.com/lesismal/nbio_examples/tree/master/websocket_1m/server/server.go)
- [websocket-1m-connections-client](https://github.com/lesismal/nbio_examples/tree/master/websocket_1m/client/client.go)

## Use With Other STD Based Frameworkds

- [echo-http-and-websocket-server](https://github.com/lesismal/nbio_examples/blob/master/http_with_other_frameworks/echo_server/echo_server.go)
- [gin-http-and-websocket-server](https://github.com/lesismal/nbio_examples/blob/master/http_with_other_frameworks/gin_server/gin_server.go)
- [go-chi-http-and-websocket-server](https://github.com/lesismal/nbio_examples/blob/master/http_with_other_frameworks/go-chi_server/go-chi_server.go)

## UDP Examples

- [udp-server](https://github.com/lesismal/nbio-examples/blob/master/udp/server/server.go)
- [udp-client](https://github.com/lesismal/nbio-examples/blob/master/udp/client/client.go)

## More Examples

- [nbio-examples](https://github.com/lesismal/nbio-examples)
