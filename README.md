# NBIO - NON-BLOCKING IO

[![Mentioned in Awesome Go](https://awesome.re/mentioned-badge-flat.svg)](https://github.com/avelino/awesome-go#networking)

[![GoDoc][1]][2] [![MIT licensed][3]][4] [![Build Status][5]][6] [![Go Report Card][7]][8] [![Coverage Statusd][11]][12]

[1]: https://godoc.org/github.com/lesismal/nbio?status.svg
[2]: https://godoc.org/github.com/lesismal/nbio
[3]: https://img.shields.io/badge/license-MIT-blue.svg
[4]: LICENSE
[5]: https://img.shields.io/github/workflow/status/lesismal/nbio/build-linux?style=flat-square&logo=github-actions
[6]: https://github.com/lesismal/nbio/actions?query=workflow%3build-linux
[7]: https://goreportcard.com/badge/github.com/lesismal/nbio
[8]: https://goreportcard.com/report/github.com/lesismal/nbio
[9]: https://img.shields.io/badge/go-%3E%3D1.16-30dff3?style=flat-square&logo=go
[10]: https://github.com/lesismal/nbio
[11]: https://codecov.io/gh/lesismal/nbio/branch/master/graph/badge.svg
[12]: https://codecov.io/gh/lesismal/nbio


## Contents

- [NBIO - NON-BLOCKING IO](#nbio---non-blocking-io)
  - [Contents](#contents)
  - [Features](#features)
  - [Installation](#installation)
  - [Quick Start](#quick-start)
  - [API Examples](#api-examples)
    - [New Gopher For Server-Side](#new-gopher-for-server-side)
    - [New Gopher For Client-Side](#new-gopher-for-client-side)
    - [Start Gopher](#start-gopher)
    - [Custom Other Config For Gopher](#custom-other-config-for-gopher)
    - [SetDeadline/SetReadDeadline/SetWriteDeadline](#setdeadlinesetreaddeadlinesetwritedeadline)
    - [Bind User Session With Conn](#bind-user-session-with-conn)
    - [Writev / Batch Write](#writev--batch-write)
    - [Handle New Connection](#handle-new-connection)
    - [Handle Disconnected](#handle-disconnected)
    - [Handle Data](#handle-data)
    - [Handle Memory Allocation/Free For Reading](#handle-memory-allocationfree-for-reading)
    - [Handle Memory Free For Writing](#handle-memory-free-for-writing)
    - [Handle Conn Before Read](#handle-conn-before-read)
    - [Handle Conn After Read](#handle-conn-after-read)
    - [Handle Conn Before Write](#handle-conn-before-write)
  - [Std Net Examples](#std-net-examples)
  - [Echo Examples](#echo-examples)
  - [TLS Examples](#tls-examples)
  - [HTTP Examples](#http-examples)
  - [HTTPS Examples](#https-examples)
  - [HTTP 1M Connections Examples](#http-1m-connections-examples)
  - [Websocket Examples](#websocket-examples)
  - [Websocket TLS Examples](#websocket-tls-examples)
  - [Websocket 1M Connections Examples](#websocket-1m-connections-examples)
  - [Bench Examples](#bench-examples)
  - [Use With Other STD Based Frameworkds](#use-with-other-std-based-frameworkds)

## Features
- [x] linux: epoll
- [x] macos(bsd): kqueue
- [x] windows: golang std net
- [x] nbio.Conn implements a non-blocking net.Conn(except windows)
- [x] writev supported
- [x] least dependency
- [x] TLS supported
- [x] HTTP/HTTPS 1.x
- [x] Websocket, [Passes the Autobahn Test Suite](https://lesismal.github.io/nbio/websocket)
- [ ] HTTP 2.0

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

g := nbio.NewGopher(nbio.Config{
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

g := nbio.NewGopher(nbio.Config{})

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

### New Gopher For Server-Side
```golang
g := nbio.NewGopher(nbio.Config{
    Network: "tcp",
    Addrs:   []string{"localhost:8888"},
})
``` 

### New Gopher For Client-Side
```golang
g := nbio.NewGopher(nbio.Config{})
``` 

### Start Gopher
```golang
err := g.Start()
if err != nil {
    fmt.Printf("Start failed: %v\n", err)
}
defer g.Stop()
```

### Custom Other Config For Gopher
```golang
conf := nbio.Config struct {
    // Name describes your gopher name for logging, it's set to "NB" by default
    Name: "NB",

    // MaxLoad represents the max online num, it's set to 10k by default
    MaxLoad: 1024 * 10, 

    // NListener represents the listener goroutine num on *nix, it's set to 1 by default
    NListener: 1,

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

### Handle Memory Free For Writing
```golang
g.OnWriteBufferFree(func(c *Conn, b []byte) {
    // ...
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

## Std Net Examples

- [std-net-echo-server](https://github.com/lesismal/nbio/blob/master/examples/netstd/server/server.go)
- [std-net-echo-client](https://github.com/lesismal/nbio/blob/master/examples/netstd/client/client.go)

## Echo Examples

- [echo-server](https://github.com/lesismal/nbio/blob/master/examples/echo/server/server.go)
- [echo-client](https://github.com/lesismal/nbio/blob/master/examples/echo/client/client.go)

## TLS Examples

- [tls-server](https://github.com/lesismal/nbio/blob/master/examples/tls/server/server.go)
- [tls-client](https://github.com/lesismal/nbio/blob/master/examples/tls/client/client.go)

## HTTP Examples

- [http-server](https://github.com/lesismal/nbio/blob/master/examples/http/server/server.go)
- [http-client](https://github.com/lesismal/nbio/blob/master/examples/http/client/client.go)

## HTTPS Examples

- [http-tls_server](https://github.com/lesismal/nbio/blob/master/examples/http/server_tls/server.go)
- visit: https://localhost:28000/echo


## HTTP 1M Connections Examples

- [http-1m-connections-server](https://github.com/lesismal/nbio/blob/master/examples/http_1m/server/server.go)
- [http-1m-connections-client](https://github.com/lesismal/nbio/blob/master/examples/http_1m/client/client.go)

## Websocket Examples

- [websocket-server](https://github.com/lesismal/nbio/blob/master/examples/websocket/server/server.go)
- [websocket-client](https://github.com/lesismal/nbio/blob/master/examples/websocket/client/client.go)

## Websocket TLS Examples

- [websocket-tls-server](https://github.com/lesismal/nbio/blob/master/examples/websocket_tls/server/server.go)
- [websocket-tls-client](https://github.com/lesismal/nbio/blob/master/examples/websocket_tls/client/client.go)

## Websocket 1M Connections Examples

- [websocket-1m-connections-server](https://github.com/lesismal/nbio/blob/master/examples/websocket_1m/server/server.go)
- [websocket-1m-connections-client](https://github.com/lesismal/nbio/blob/master/examples/websocket_1m/client/client.go)

## Bench Examples

**refer to this test, or write your own test cases:**

- [bench-server](https://github.com/lesismal/nbio/blob/master/examples/bench/server/server.go)
- [bench-client](https://github.com/lesismal/nbio/blob/master/examples/bench/client/client.go)

## Use With Other STD Based Frameworkds

- [gin-http-and-websocket-server](https://github.com/lesismal/nbio_with_other_frameworks/blob/master/gin_server/gin_server.go)
- [echo-http-and-websocket-server](https://github.com/lesismal/nbio_with_other_frameworks/blob/master/echo_server/echo_server.go)