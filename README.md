# NBIO - NON-BLOCKING IO / NIUBILITY IO

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

- [NBIO - NON-BLOCKING IO / NIUBILITY IO](#nbio---non-blocking-io--niubility-io)
  - [Contents](#contents)
  - [Features](#features)
  - [Installation](#installation)
  - [Quick Start](#quick-start)
  - [API Examples](#api-examples)
    - [New Gopher For Server-Side](#new-gopher-for-server-side)
    - [New Gopher For Client-Side](#new-gopher-for-client-side)
    - [Start Gopher](#start-gopher)
    - [Custom Other Config](#custom-other-config)
    - [Handle New Connection](#handle-new-connection)
    - [Handle Disconnected](#handle-disconnected)
    - [Handle Data](#handle-data)
    - [Handle Memory Allocation/Free For Read](#handle-memory-allocationfree-for-read)
    - [Handle Conn Before Read](#handle-conn-before-read)
    - [Handle Conn After Read](#handle-conn-after-read)
    - [Handle Conn Before Write](#handle-conn-before-write)
  - [Echo Examples](#echo-examples)
  - [TLS Examples](#tls-examples)
  - [Bench Examples](#bench-examples)
  - [Dependency](#dependency)

## Features
- [x] linux: epoll
- [x] macos(bsd): kqueue
- [x] windows: golang std net
- [x] nbio.Conn implements a non-blocking net.Conn(except windows)
- [x] tls supported
- [x] writev supported
- [x] least dependency

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

### Custom Other Config
```golang
conf := nbio.Config struct {
    // Name describes your gopher name for logging, it's set to "NB" by default
	Name: "NB",

    // MaxLoad decides the max online num, it's set to 10k by default
	MaxLoad: 1024 * 10, 

	// NListener decides the listener goroutine num on *nix, it's set to 1 by default
	NListener: 1,

	// NPoller decides poller goroutine num, it's set to runtime.NumCPU() by default
	NPoller: runtime.NumCPU(),

	// ReadBufferSize decides buffer size for reading, it's set to 16k by default
	ReadBufferSize: 1024 * 16,

	// MaxWriteBufferSize decides max write buffer size for Conn, it's set to 1m by default.
    // if the connection's Send-Q is full and the data cached by nbio is 
    // more than MaxWriteBufferSize, the connection would be closed by nbio.
	MaxWriteBufferSize uint32

	// LockThread decides poller's goroutine to lock thread or not.
	LockThread bool
}
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
g.OnOpen(func(c *Conn) {
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

### Handle Memory Allocation/Free For Read
```golang
import "sync"

var memPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, yourSize)
	},
}

g.OnMemAlloc(func(c *Conn) []byte {
    return memPool.Get().([]byte)
})
g.OnMemFree(func(c *Conn, b []byte) {
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

- [echo-server](https://github.com/lesismal/nbio/blob/master/examples/echo/server/server.go)
- [echo-client](https://github.com/lesismal/nbio/blob/master/examples/echo/client/client.go)

## TLS Examples

- [tls-server](https://github.com/lesismal/nbio/blob/master/examples/tls/server/server.go)
- [tls-client](https://github.com/lesismal/nbio/blob/master/examples/tls/client/client.go)

## Bench Examples

**refer to this test, or write your own test cases:**

- [bench-server](https://github.com/lesismal/nbio/blob/master/examples/bench/server/server.go)
- [bench-client](https://github.com/lesismal/nbio/blob/master/examples/bench/client/client.go)

## Dependency

**nbio** depend on std lib only.

extension for TLS depends on [lesismal/llib/std/crypto/tls](), which is copied from of go1.6 and rewrited, if tls is used like the [example](#tls-examples), go1.6+ is needed.