// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbhttp

import (
	"net/http"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/lesismal/llib/std/crypto/tls"
	"github.com/lesismal/nbio"
	"github.com/lesismal/nbio/loging"
	"github.com/lesismal/nbio/mempool"
	"github.com/lesismal/nbio/taskpool"
)

var (
	// DefaultMaxLoad .
	DefaultMaxLoad = 1024 * 500

	// DefaultHTTPReadLimit .
	DefaultHTTPReadLimit = 1024 * 1024 * 64

	// DefaultMinBufferSize .
	DefaultMinBufferSize = 1024 * 2

	// DefaultHTTPWriteBufferSize .
	DefaultHTTPWriteBufferSize = 1024 * 2

	// DefaultMessageHandlerPoolSize .
	// DefaultMessageHandlerPoolSize = runtime.NumCPU() * 256

	// DefaultMessageHandlerTaskIdleTime .
	DefaultMessageHandlerTaskIdleTime = time.Second * 60

	// DefaultKeepaliveTime .
	DefaultKeepaliveTime = time.Second * 120

	// DefaultTLSHandshakeTimeout .
	DefaultTLSHandshakeTimeout = time.Second * 10
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

	// NPoller represents poller goroutine num, it's set to runtime.NumCPU() by default.
	NPoller int

	// NListener represents poller goroutine num, it's set to runtime.NumCPU() by default.
	NListener int

	// NParser represents parser goroutine num, it's set to NPoller by default.
	NParser int

	// ReadLimit represents the max size for parser reading, it's set to 64M by default.
	ReadLimit int

	// ReadBufferSize represents buffer size for reading, it's set to 2k by default.
	ReadBufferSize int

	// MinBufferSize represents buffer size for http request parsing and response encoding, it's set to 2k by default.
	MinBufferSize int

	// MaxWriteBufferSize represents max write buffer size for Conn, it's set to 1m by default.
	// if the connection's Send-Q is full and the data cached by nbio is
	// more than MaxWriteBufferSize, the connection would be closed by nbio.
	MaxWriteBufferSize int

	// LockThread represents poller's goroutine to lock thread or not, it's set to false by default.
	LockThread bool

	// MessageHandlerPoolSize represents max http server's task pool goroutine num, it's set to runtime.NumCPU() * 256 by default.
	MessageHandlerPoolSize int

	// MessageHandlerTaskIdleTime represents idle time for task pool's goroutine, it's set to 60s by default.
	MessageHandlerTaskIdleTime time.Duration

	// KeepaliveTime represents Conn's ReadDeadline when waiting for a new request, it's set to 120s by default.
	KeepaliveTime time.Duration

	// EnableSendfile .
	EnableSendfile bool
}

// Server .
type Server struct {
	*nbio.Gopher

	MaxLoad  int
	_onOpen  func(c *nbio.Conn)
	_onClose func(c *nbio.Conn, err error)
	_onStop  func()

	ParserExecutor         func(index int, f func())
	MessageHandlerExecutor func(f func())

	mux   sync.Mutex
	conns map[*nbio.Conn]struct{}

	Malloc  func(size int) []byte
	Realloc func(buf []byte, size int) []byte
	Free    func(buf []byte) error
}

// OnOpen registers callback for new connection
func (s *Server) OnOpen(h func(c *nbio.Conn)) {
	if h == nil {
		panic("invalid nil handler")
	}
	s._onOpen = h
}

// OnClose registers callback for disconnected
func (s *Server) OnClose(h func(c *nbio.Conn, err error)) {
	if h == nil {
		panic("invalid nil handler")
	}
	s._onClose = h
}

// OnStop registers callback before Gopher is stopped.
func (s *Server) OnStop(h func()) {
	if h == nil {
		panic("invalid nil handler")
	}
	s._onStop = h
}

func (s *Server) Online() int {
	return len(s.conns)
}

// NewServer .
func NewServer(conf Config, handler http.Handler, messageHandlerExecutor func(f func())) *Server {
	if conf.MaxLoad <= 0 {
		conf.MaxLoad = DefaultMaxLoad
	}
	if conf.NPoller <= 0 {
		conf.NPoller = runtime.NumCPU()
	}
	if conf.NParser <= 0 {
		conf.NParser = conf.NPoller
	}
	if conf.ReadLimit <= 0 {
		conf.ReadLimit = DefaultHTTPReadLimit
	}
	if conf.MinBufferSize <= 0 {
		conf.MinBufferSize = DefaultMinBufferSize
	}
	if conf.KeepaliveTime <= 0 {
		conf.KeepaliveTime = DefaultKeepaliveTime
	}
	if conf.ReadBufferSize <= 0 {
		conf.ReadBufferSize = nbio.DefaultReadBufferSize
	}

	var messageHandlerExecutePool *taskpool.MixedPool
	parserExecutor := func(index int, f func()) {
		defer func() {
			if err := recover(); err != nil {
				const size = 64 << 10
				buf := make([]byte, size)
				buf = buf[:runtime.Stack(buf, false)]
				loging.Error("execute parser failed: %v\n%v\n", err, *(*string)(unsafe.Pointer(&buf)))
			}
		}()
		f()
	}

	if messageHandlerExecutor == nil {
		if conf.MessageHandlerPoolSize <= 0 {
			conf.MessageHandlerPoolSize = conf.NPoller * 256
		}
		if conf.MessageHandlerTaskIdleTime <= 0 {
			conf.MessageHandlerTaskIdleTime = DefaultMessageHandlerTaskIdleTime
		}
		nativeSize := conf.MaxLoad / 8
		if nativeSize <= 0 {
			nativeSize = 1024
		}
		bufferSize := conf.MaxLoad - nativeSize + 1
		if bufferSize <= 0 {
			bufferSize = 1024
		}
		messageHandlerExecutePool = taskpool.NewMixedPool(nativeSize, conf.NPoller, bufferSize)
		messageHandlerExecutor = messageHandlerExecutePool.Go
	}

	gopherConf := nbio.Config{
		Name:               conf.Name,
		Network:            conf.Network,
		Addrs:              conf.Addrs,
		NPoller:            conf.NPoller,
		NListener:          conf.NListener,
		ReadBufferSize:     conf.ReadBufferSize,
		MaxWriteBufferSize: conf.MaxWriteBufferSize,
		LockThread:         conf.LockThread,
	}
	g := nbio.NewGopher(gopherConf)

	svr := &Server{
		Gopher:                 g,
		_onOpen:                func(c *nbio.Conn) {},
		_onClose:               func(c *nbio.Conn, err error) {},
		_onStop:                func() {},
		MaxLoad:                conf.MaxLoad,
		ParserExecutor:         parserExecutor,
		MessageHandlerExecutor: messageHandlerExecutor,
		conns:                  map[*nbio.Conn]struct{}{},

		Malloc:  mempool.Malloc,
		Realloc: mempool.Realloc,
		Free:    mempool.Free,
	}

	g.OnOpen(func(c *nbio.Conn) {
		svr.mux.Lock()
		if len(svr.conns) >= svr.MaxLoad {
			svr.mux.Unlock()
			c.Close()
			return
		}
		svr.conns[c] = struct{}{}
		svr.mux.Unlock()
		svr._onOpen(c)
		processor := NewServerProcessor(c, handler, messageHandlerExecutor, conf.MinBufferSize, conf.KeepaliveTime, conf.EnableSendfile)
		parser := NewParser(processor, false, conf.ReadLimit, conf.MinBufferSize)
		parser.Server = svr
		processor.(*ServerProcessor).parser = parser
		c.SetSession(parser)
		c.SetReadDeadline(time.Now().Add(conf.KeepaliveTime))
	})
	g.OnClose(func(c *nbio.Conn, err error) {
		parser := c.Session().(*Parser)
		if parser == nil {
			loging.Error("nil parser")
		}
		parser.onClose(err)
		svr._onClose(c, err)
		svr.mux.Lock()
		delete(svr.conns, c)
		svr.mux.Unlock()
	})
	g.OnData(func(c *nbio.Conn, data []byte) {
		newData := svr.Malloc(len(data))
		copy(newData, data)
		data = newData
		parser := c.Session().(*Parser)
		if parser == nil {
			loging.Error("nil parser")
			return
		}
		svr.ParserExecutor(c.Hash(), func() {
			err := parser.Read(data)
			if err != nil {
				loging.Debug("parser.Read failed: %v", err)
				c.CloseWithError(err)
			}
		})
		// c.SetReadDeadline(time.Now().Add(conf.KeepaliveTime))
	})

	// g.OnReadBufferAlloc(func(c *nbio.Conn) []byte {
	// 	return mempool.Malloc(int(conf.ReadBufferSize))
	// })
	// g.OnReadBufferFree(func(c *nbio.Conn, buffer []byte) {})
	g.OnWriteBufferRelease(func(c *nbio.Conn, buffer []byte) {
		mempool.Free(buffer)
	})

	g.OnStop(func() {
		svr._onStop()
		svr.MessageHandlerExecutor = func(f func()) {}
		svr.ParserExecutor = func(index int, f func()) {}
		if messageHandlerExecutePool != nil {
			messageHandlerExecutePool.Stop()
		}
	})
	return svr
}

// NewServerTLS .
func NewServerTLS(conf Config, handler http.Handler, messageHandlerExecutor func(f func()), tlsConfig *tls.Config) *Server {
	if conf.MaxLoad <= 0 {
		conf.MaxLoad = DefaultMaxLoad
	}
	if conf.NPoller <= 0 {
		conf.NPoller = runtime.NumCPU()
	}
	if conf.NParser <= 0 {
		conf.NParser = conf.NPoller
	}
	if conf.ReadLimit <= 0 {
		conf.ReadLimit = DefaultHTTPReadLimit
	}
	if conf.MinBufferSize <= 0 {
		conf.MinBufferSize = DefaultMinBufferSize
	}
	if conf.KeepaliveTime <= 0 {
		conf.KeepaliveTime = DefaultKeepaliveTime
	}
	if conf.ReadBufferSize <= 0 {
		conf.ReadBufferSize = nbio.DefaultReadBufferSize
	}
	conf.EnableSendfile = false

	var messageHandlerExecutePool *taskpool.MixedPool
	parserExecutor := func(index int, f func()) {
		defer func() {
			if err := recover(); err != nil {
				const size = 64 << 10
				buf := make([]byte, size)
				buf = buf[:runtime.Stack(buf, false)]
				loging.Error("execute parser failed: %v\n%v\n", err, *(*string)(unsafe.Pointer(&buf)))
			}
		}()
		f()
	}
	if messageHandlerExecutor == nil {
		if conf.MessageHandlerPoolSize <= 0 {
			conf.MessageHandlerPoolSize = conf.NPoller * 256
		}
		if conf.MessageHandlerTaskIdleTime <= 0 {
			conf.MessageHandlerTaskIdleTime = DefaultMessageHandlerTaskIdleTime
		}
		nativeSize := conf.MaxLoad / 8
		if nativeSize <= 0 {
			nativeSize = 1024
		}
		bufferSize := conf.MaxLoad - nativeSize + 1
		if bufferSize <= 0 {
			bufferSize = 1024
		}
		messageHandlerExecutePool = taskpool.NewMixedPool(nativeSize, conf.NPoller, bufferSize)
		messageHandlerExecutor = messageHandlerExecutePool.Go
	}

	// setup prefer protos: http2.0, other protos to be added
	preferenceProtos := map[string]struct{}{
		// "h2": {},
	}
	for _, v := range tlsConfig.NextProtos {
		if _, ok := preferenceProtos[v]; ok {
			delete(preferenceProtos, v)
		}
	}
	for proto := range preferenceProtos {
		tlsConfig.NextProtos = append(tlsConfig.NextProtos, proto)
	}

	gopherConf := nbio.Config{
		Name:               conf.Name,
		Network:            conf.Network,
		Addrs:              conf.Addrs,
		NPoller:            conf.NPoller,
		NListener:          conf.NListener,
		ReadBufferSize:     conf.ReadBufferSize,
		MaxWriteBufferSize: conf.MaxWriteBufferSize,
		LockThread:         conf.LockThread,
	}
	g := nbio.NewGopher(gopherConf)

	nativeAllocator := &mempool.NativeAllocator{}
	svr := &Server{
		Gopher:                 g,
		_onOpen:                func(c *nbio.Conn) {},
		_onClose:               func(c *nbio.Conn, err error) {},
		_onStop:                func() {},
		MaxLoad:                conf.MaxLoad,
		ParserExecutor:         parserExecutor,
		MessageHandlerExecutor: messageHandlerExecutor,
		conns:                  map[*nbio.Conn]struct{}{},

		Malloc:  nativeAllocator.Malloc,
		Realloc: nativeAllocator.Realloc,
		Free:    nativeAllocator.Free,
		// Malloc:  mempool.Malloc,
		// Realloc: mempool.Realloc,
		// Free:    mempool.Free,
	}

	isClient := false

	g.OnOpen(func(c *nbio.Conn) {
		svr.mux.Lock()
		if len(svr.conns) >= svr.MaxLoad {
			svr.mux.Unlock()
			c.Close()
			return
		}
		svr.conns[c] = struct{}{}
		svr.mux.Unlock()
		svr._onOpen(c)
		tlsConn := tls.NewConn(c, tlsConfig, isClient, true, conf.ReadBufferSize)
		processor := NewServerProcessor(tlsConn, handler, messageHandlerExecutor, conf.MinBufferSize, conf.KeepaliveTime, conf.EnableSendfile)
		parser := NewParser(processor, false, conf.ReadLimit, conf.MinBufferSize)
		parser.Server = svr
		parser.TLSBuffer = make([]byte, conf.ReadBufferSize)
		processor.(*ServerProcessor).parser = parser
		c.SetSession(parser)
		c.SetReadDeadline(time.Now().Add(conf.KeepaliveTime))
	})
	g.OnClose(func(c *nbio.Conn, err error) {
		parser := c.Session().(*Parser)
		if parser == nil {
			loging.Error("nil parser")
			return
		}
		parser.onClose(err)
		svr._onClose(c, err)
		svr.mux.Lock()
		delete(svr.conns, c)
		svr.mux.Unlock()
	})
	g.OnData(func(c *nbio.Conn, data []byte) {
		parser := c.Session().(*Parser)
		if parser == nil {
			loging.Error("nil parser")
			c.Close()
			return
		}
		if tlsConn, ok := parser.Processor.Conn().(*tls.Conn); ok {
			tlsConn.Append(data)
			svr.ParserExecutor(c.Hash(), func() {
				for {
					buffer := parser.TLSBuffer
					n, err := tlsConn.Read(buffer)
					if err != nil {
						c.CloseWithError(err)
						return
					}
					if n > 0 {
						err := parser.Read(buffer[:n])
						if err != nil {
							loging.Debug("parser.Read failed: %v", err)
							c.CloseWithError(err)
							return
						}
					}
					if n < len(buffer) {
						return
					}
				}
			})
			// c.SetReadDeadline(time.Now().Add(conf.KeepaliveTime))
		}
	})
	// g.OnReadBufferAlloc(func(c *nbio.Conn) []byte {
	// 	return mempool.Malloc(int(conf.ReadBufferSize))
	// })
	// g.OnReadBufferFree(func(c *nbio.Conn, buffer []byte) {})
	// g.OnWriteBufferRelease(func(c *nbio.Conn, buffer []byte) {
	// 	mempool.Free(buffer)
	// })

	g.OnStop(func() {
		svr._onStop()
		svr.MessageHandlerExecutor = func(f func()) {}
		svr.ParserExecutor = func(index int, f func()) {}
		if messageHandlerExecutePool != nil {
			messageHandlerExecutePool.Stop()
		}
	})
	return svr
}
