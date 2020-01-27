# NBIO - NON BLOCKING IO

[![GoDoc][1]][2] [![MIT licensed][3]][4] [![Build Status][5]][6] [![Go Report Card][7]][8] [![Coverage Statusd][9]][10]

[1]: https://godoc.org/github.com/lesismal/nbio?status.svg
[2]: https://godoc.org/github.com/lesismal/nbio
[3]: https://img.shields.io/badge/license-MIT-blue.svg
[4]: LICENSE
[5]: https://travis-ci.org/lesismal/nbio.svg?branch=master
[6]: https://travis-ci.org/lesismal/nbio
[7]: https://goreportcard.com/badge/github.com/lesismal/nbio
[8]: https://goreportcard.com/report/github.com/lesismal/nbio
[9]: https://codecov.io/gh/lesismal/nbio/branch/master/graph/badge.svg
[10]: https://codecov.io/gh/lesismal/nbio

## Examples

- [echo-server](https://github.com/lesismal/nbio/blob/master/examples/echo/server.go)

```golang
package main

import (
	"fmt"
	"github.com/lesismal/nbio"
	"time"
)

func onOpen(c *nbio.Conn) {
	c.SetReadDeadline(time.Now().Add(time.Second * 10))
	fmt.Println("onOpen:", c.RemoteAddr().String(), time.Now().Format("15:04:05.000"))
}

func onClose(c *nbio.Conn, err error) {
	fmt.Println("onClose:", c.RemoteAddr().String(), time.Now().Format("15:04:05.000"), err)
}

func onData(c *nbio.Conn, data []byte) {
	c.SetReadDeadline(time.Now().Add(time.Second * 10))
	c.SetWriteDeadline(time.Now().Add(time.Second * 3))
	c.Write(append([]byte{}, data...))
}

func main() {
	g, err := nbio.NewGopher(nbio.Config{
		Network:      "tcp",
		Address:      ":8888",
		NPoller:      2,
		NWorker:      4,
		QueueSize:    1024,
		BufferSize:   1024 * 8,
		BufferNum:    1024 * 2,
		PollInterval: time.Millisecond * 200,
		MaxTimeout:   time.Second * 10,
	})
	if err != nil {
		fmt.Printf("nbio.New failed: %v\n", err)
		return
	}

	g.OnOpen(onOpen)
	g.OnClose(onClose)
	g.OnData(onData)

	err = g.Start()
	if err != nil {
		fmt.Printf("nbio.Start failed: %v\n", err)
		return
	}

	go func() {
		for {
			time.Sleep(time.Second * 5)
			fmt.Println(g.State().String())
		}
	}()

	g.Wait()
}
```
