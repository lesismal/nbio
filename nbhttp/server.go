// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbhttp

import (
	"net/http"
	"runtime"
	"time"

	"github.com/lesismal/nbio"
	"github.com/lesismal/nbio/loging"
	"github.com/lesismal/nbio/mempool"
	"github.com/lesismal/nbio/taskpool"
)

var (
	// DefaultHTTPMaxReadSize .
	DefaultHTTPMaxReadSize = 1024 * 1024

	// DefaultHTTPReadBufferSize .
	DefaultHTTPReadBufferSize = 1024 * 4

	// DefaultHTTPWriteBufferSize .
	DefaultHTTPWriteBufferSize = 1024 * 4

	// DefaultExecutorTaskPoolSize .
	DefaultExecutorTaskPoolSize = runtime.NumCPU() * 64

	// DefaultExecutorTaskIdleTime .
	DefaultExecutorTaskIdleTime = time.Second * 60
)

// Config .
type Config struct {
	// Name describes your gopher name for logging, it's set to "NB" by default.
	Name string

	// Network is the listening protocol, used with Addrs toghter.
	// tcp* supported only by now, there's no plan for other protocol such as udp,
	// because it's too easy to write udp server/client.
	Network string

	// Addrs is the listening addr list for a nbio server.
	// if it is empty, no listener created, then the Gopher is used for client by default.
	Addrs []string

	// MaxLoad represents the max online num, it's set to 10k by default.
	MaxLoad int

	// NListener represents the listener goroutine num on *nix, it's set to 1 by default.
	NListener int

	// NPoller represents poller goroutine num, it's set to runtime.NumCPU() by default.
	NPoller int

	// NParser represents parser goroutine num, it's set to NPoller by default.
	NParser int

	// ReadBufferSize represents buffer size for reading, it's set to 16k by default.
	ReadBufferSize int

	// MaxWriteBufferSize represents max write buffer size for Conn, it's set to 1m by default.
	// if the connection's Send-Q is full and the data cached by nbio is
	// more than MaxWriteBufferSize, the connection would be closed by nbio.
	MaxWriteBufferSize int

	// LockThread represents poller's goroutine to lock thread or not, it's set to false by default.
	LockThread bool

	// MaxReadSize represents buffer size for max reading, it's set to 1M by default.
	MaxReadSize int

	// MaxReadSize represents max http server's task pool goroutine num, it's set to runtime.NumCPU() * 64 by default.
	TaskPoolSize int

	// TaskIdleTime represents idle time for task pool's goroutine, it's set to 60s by default.
	TaskIdleTime time.Duration
}

// NewServer .
func NewServer(conf Config, handler http.Handler, executor func(f func())) *nbio.Gopher {
	if conf.ReadBufferSize == 0 {
		conf.ReadBufferSize = DefaultHTTPReadBufferSize
	}
	if conf.NPoller <= 0 {
		conf.NPoller = runtime.NumCPU()
	}
	if conf.NParser <= 0 {
		conf.NParser = conf.NPoller * 4
	}

	if executor == nil {
		if conf.TaskPoolSize <= 0 {
			conf.TaskPoolSize = conf.NParser * 128
		}
		if conf.TaskIdleTime <= 0 {
			conf.TaskIdleTime = DefaultExecutorTaskIdleTime
		}
		tp := taskpool.New(conf.TaskPoolSize, conf.TaskIdleTime)
		executor = tp.Go
	}
	if conf.MaxReadSize <= 0 {
		conf.MaxReadSize = DefaultHTTPMaxReadSize
	}
	fixedPool := taskpool.NewFixedPool(conf.NParser, 128)
	gopherConf := nbio.Config{
		Name:               conf.Name,
		Network:            conf.Network,
		Addrs:              conf.Addrs,
		MaxLoad:            conf.MaxLoad,
		NListener:          conf.NListener,
		NPoller:            conf.NPoller,
		ReadBufferSize:     conf.ReadBufferSize,
		MaxWriteBufferSize: conf.MaxWriteBufferSize,
		LockThread:         conf.LockThread,
	}
	g := nbio.NewGopher(gopherConf)

	g.OnOpen(func(c *nbio.Conn) {
		processor := NewServerProcessor(c, handler)
		processor.HandleExecute(executor)
		parser := NewParser(processor, false, conf.MaxReadSize)
		c.SetSession(parser)
		c.SetReadDeadline(time.Now().Add(time.Second * 120))
	})
	g.OnClose(func(c *nbio.Conn, err error) {
		parser := c.Session().(*Parser)
		if parser == nil {
			loging.Error("nil parser")
		}
	})
	g.OnData(func(c *nbio.Conn, data []byte) {
		parser := c.Session().(*Parser)
		if parser == nil {
			loging.Error("nil parser")
			c.Close()
			return
		}
		fixedPool.GoByIndex(c.Hash(), func() {
			err := parser.Read(data)
			if err != nil {
				loging.Error("parser.Read failed: %v", err)
				c.Close()
			}
		})
	})

	g.OnMemAlloc(func(c *nbio.Conn) []byte {
		return mempool.Malloc(int(conf.ReadBufferSize))
	})
	// g.OnMemFree(func(c *nbio.Conn, buffer []byte) {})

	return g
}
