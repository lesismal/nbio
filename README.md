# NBIO - NON BLOCKING IO / NIUBILITY IO

[![GoDoc][1]][2] [![MIT licensed][3]][4] [![Build Status][5]][6] [![Go Report Card][7]][8] [![Coverage Statusd][9]][10]

[1]: https://godoc.org/github.com/lesismal/nbio?status.svg
[2]: https://godoc.org/github.com/lesismal/nbio
[3]: https://img.shields.io/badge/license-MIT-blue.svg
[4]: LICENSE
[5]: https://img.shields.io/github/workflow/status/lesismal/nbio/build-linux?style=flat-square&logo=github-actions
[6]: https://github.com/lesismal/nbio/actions?query=workflow%3build-linux
[7]: https://goreportcard.com/badge/github.com/lesismal/nbio
[8]: https://goreportcard.com/report/github.com/lesismal/nbio
[9]: https://codecov.io/gh/lesismal/nbio/branch/master/graph/badge.svg
[10]: https://codecov.io/gh/lesismal/nbio


## Contents

- [NBIO - NON BLOCKING IO / NIUBILITY IO](#nbio---non-blocking-io--niubility-io)
  - [Contents](#contents)
  - [Features](#features)
  - [Installation](#installation)
  - [Quick Start](#quick-start)
  - [Echo Examples](#echo-examples)
  - [Performance](#performance)

## Features
- [x] linux: epoll
- [x] macos(bsd): kqueue
- [x] windows: golang std net
- [x] nbio.Conn implements a non-blocking net.Conn(except windows)

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

## Echo Examples

- [echo-server](https://github.com/lesismal/nbio/blob/master/examples/echo/server/server.go)
- [echo-client](https://github.com/lesismal/nbio/blob/master/examples/echo/client/client.go)

## Performance

**refer to this test, or write your own test cases:**

- [bench-server](https://github.com/lesismal/nbio/blob/master/examples/bench/server/server.go)
- [bench-client](https://github.com/lesismal/nbio/blob/master/examples/bench/client/client.go)


