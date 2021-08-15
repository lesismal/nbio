// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbhttp

import (
	"context"
	"math/rand"
	"net/http"
	"runtime"
	"sync"
	"time"
	"unicode/utf8"
	"unsafe"

	"github.com/lesismal/llib/std/crypto/tls"
	"github.com/lesismal/nbio"
	"github.com/lesismal/nbio/logging"
	"github.com/lesismal/nbio/mempool"
	"github.com/lesismal/nbio/taskpool"
)

var (
	// DefaultMaxLoad .
	DefaultMaxLoad = 1024 * 1024

	// DefaultHTTPReadLimit .
	DefaultHTTPReadLimit = 1024 * 1024 * 64

	// DefaultMinBufferSize .
	DefaultMinBufferSize = 1024 * 2

	// DefaultHTTPWriteBufferSize .
	DefaultHTTPWriteBufferSize = 1024 * 2

	// DefaultMaxWebsocketFramePayloadSize
	DefaultMaxWebsocketFramePayloadSize = 1024 * 32

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

	// MaxWriteBufferSize represents max write buffer size for Conn, it's set to 1m by default.
	// if the connection's Send-Q is full and the data cached by nbio is
	// more than MaxWriteBufferSize, the connection would be closed by nbio.
	MaxWriteBufferSize int

	// MaxWebsocketFramePayloadSize represents max payload size of websocket frame.
	MaxWebsocketFramePayloadSize int

	// MessageHandlerPoolSize represents max http server's task pool goroutine num, it's set to runtime.NumCPU() * 256 by default.
	MessageHandlerPoolSize int

	// MessageHandlerTaskIdleTime represents idle time for task pool's goroutine, it's set to 60s by default.
	// MessageHandlerTaskIdleTime time.Duration

	// KeepaliveTime represents Conn's ReadDeadline when waiting for a new request, it's set to 120s by default.
	KeepaliveTime time.Duration

	// LockListener represents listener's goroutine to lock thread or not, it's set to false by default.
	LockListener bool

	// LockPoller represents poller's goroutine to lock thread or not, it's set to false by default.
	LockPoller bool

	// EnableSendfile .
	EnableSendfile bool

	// ReleaseWebsocketPayload .
	ReleaseWebsocketPayload bool

	// MaxReadTimesPerEventLoop represents max read times in one poller loop for one fd
	MaxReadTimesPerEventLoop int
}

// Server .
type Server struct {
	*nbio.Gopher

	MaxLoad                      int
	MaxWebsocketFramePayloadSize int
	ReleaseWebsocketPayload      bool
	CheckUtf8                    func(data []byte) bool

	_onOpen  func(c *nbio.Conn)
	_onClose func(c *nbio.Conn, err error)
	_onStop  func()

	mux   sync.Mutex
	conns map[*nbio.Conn]struct{}
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

func (s *Server) closeIdleConns(chCloseQueue chan *nbio.Conn) {
	s.mux.Lock()
	defer s.mux.Unlock()
	for c := range s.conns {
		sess := c.Session()
		if sess != nil {
			if c.ExecuteLen() == 0 {
				select {
				case chCloseQueue <- c:
				default:
				}
			}
		}
	}
}

func (s *Server) Shutdown(ctx context.Context) error {
	pollIntervalBase := time.Millisecond
	shutdownPollIntervalMax := time.Millisecond * 200
	nextPollInterval := func() time.Duration {
		interval := pollIntervalBase + time.Duration(rand.Intn(int(pollIntervalBase/10)))
		pollIntervalBase *= 2
		if pollIntervalBase > shutdownPollIntervalMax {
			pollIntervalBase = shutdownPollIntervalMax
		}
		return interval
	}

	chCloseQueue := make(chan *nbio.Conn, 1024)
	defer close(chCloseQueue)

	go func() {
		for c := range chCloseQueue {
			c.Close()
		}
	}()

	timer := time.NewTimer(nextPollInterval())
	defer timer.Stop()
	for {
		s.closeIdleConns(chCloseQueue)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			if len(s.conns) == 0 {
				goto Exit
			}
			timer.Reset(nextPollInterval())
		}
	}

Exit:
	s.Stop()
	logging.Info("Gopher[%v] shutdown", s.Name)
	return nil
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
	if conf.KeepaliveTime <= 0 {
		conf.KeepaliveTime = DefaultKeepaliveTime
	}
	if conf.ReadBufferSize <= 0 {
		conf.ReadBufferSize = nbio.DefaultReadBufferSize
	}
	if conf.MaxWebsocketFramePayloadSize <= 0 {
		conf.MaxWebsocketFramePayloadSize = DefaultMaxWebsocketFramePayloadSize
	}

	var messageHandlerExecutePool *taskpool.MixedPool
	if messageHandlerExecutor == nil {
		if conf.MessageHandlerPoolSize <= 0 {
			conf.MessageHandlerPoolSize = runtime.NumCPU() * 1024
		}
		nativeSize := conf.MessageHandlerPoolSize - conf.NPoller
		if nativeSize <= 0 {
			nativeSize = 1024
		}

		messageHandlerExecutePool = taskpool.NewMixedPool(nativeSize, conf.NPoller, 1024)
		messageHandlerExecutor = messageHandlerExecutePool.Go
	}

	gopherConf := nbio.Config{
		Name:                     conf.Name,
		Network:                  conf.Network,
		Addrs:                    conf.Addrs,
		NPoller:                  conf.NPoller,
		NListener:                conf.NListener,
		ReadBufferSize:           conf.ReadBufferSize,
		MaxWriteBufferSize:       conf.MaxWriteBufferSize,
		MaxReadTimesPerEventLoop: conf.MaxReadTimesPerEventLoop,
		LockPoller:               conf.LockPoller,
		LockListener:             conf.LockListener,
	}
	g := nbio.NewGopher(gopherConf)
	g.Execute = messageHandlerExecutor

	svr := &Server{
		Gopher:                       g,
		_onOpen:                      func(c *nbio.Conn) {},
		_onClose:                     func(c *nbio.Conn, err error) {},
		_onStop:                      func() {},
		MaxLoad:                      conf.MaxLoad,
		MaxWebsocketFramePayloadSize: conf.MaxWebsocketFramePayloadSize,
		ReleaseWebsocketPayload:      conf.ReleaseWebsocketPayload,
		CheckUtf8:                    utf8.Valid,
		conns:                        map[*nbio.Conn]struct{}{},
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
		processor := NewServerProcessor(c, handler, conf.KeepaliveTime, conf.EnableSendfile)
		parser := NewParser(processor, false, conf.ReadLimit, c.Execute)
		parser.Server = svr
		processor.(*ServerProcessor).parser = parser
		c.SetSession(parser)
		c.SetReadDeadline(time.Now().Add(conf.KeepaliveTime))
	})
	g.OnClose(func(c *nbio.Conn, err error) {
		c.Execute(func() {
			parser := c.Session().(*Parser)
			if parser == nil {
				logging.Error("nil parser")
			}
			parser.Close(err)
			svr._onClose(c, err)
			svr.mux.Lock()
			delete(svr.conns, c)
			svr.mux.Unlock()
		})
	})
	g.OnData(func(c *nbio.Conn, data []byte) {
		defer func() {
			if err := recover(); err != nil {
				const size = 64 << 10
				buf := make([]byte, size)
				buf = buf[:runtime.Stack(buf, false)]
				logging.Error("execute parser failed: %v\n%v\n", err, *(*string)(unsafe.Pointer(&buf)))
			}
		}()

		parser := c.Session().(*Parser)
		if parser == nil {
			logging.Error("nil parser")
			return
		}

		err := parser.Read(data)
		if err != nil {
			logging.Debug("parser.Read failed: %v", err)
			c.CloseWithError(err)
		}
		// c.SetReadDeadline(time.Now().Add(conf.KeepaliveTime))
	})

	g.OnWriteBufferRelease(func(c *nbio.Conn, buffer []byte) {
		mempool.Free(buffer)
	})

	g.OnStop(func() {
		svr._onStop()
		g.Execute = func(f func()) {}
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
	if conf.KeepaliveTime <= 0 {
		conf.KeepaliveTime = DefaultKeepaliveTime
	}
	if conf.ReadBufferSize <= 0 {
		conf.ReadBufferSize = nbio.DefaultReadBufferSize
	}
	if conf.MaxWebsocketFramePayloadSize <= 0 {
		conf.MaxWebsocketFramePayloadSize = DefaultMaxWebsocketFramePayloadSize
	}
	conf.EnableSendfile = false

	buffers := make([][]byte, conf.NParser)
	for i := 0; i < len(buffers); i++ {
		buffers[i] = make([]byte, conf.ReadBufferSize)
	}
	getBuffer := func(c *nbio.Conn) []byte {
		return buffers[uint64(c.Hash())%uint64(conf.NParser)]
	}
	if runtime.GOOS == "windows" {
		bufferMux := sync.Mutex{}
		buffers := map[*nbio.Conn][]byte{}
		getBuffer = func(c *nbio.Conn) []byte {
			bufferMux.Lock()
			defer bufferMux.Unlock()
			buf, ok := buffers[c]
			if !ok {
				buf = make([]byte, 4096)
				buffers[c] = buf
			}
			return buf
		}
	}

	var messageHandlerExecutePool *taskpool.MixedPool
	if messageHandlerExecutor == nil {
		if conf.MessageHandlerPoolSize <= 0 {
			conf.MessageHandlerPoolSize = runtime.NumCPU() * 1024
		}
		nativeSize := conf.MessageHandlerPoolSize - conf.NPoller
		if nativeSize <= 0 {
			nativeSize = 1024
		}

		messageHandlerExecutePool = taskpool.NewMixedPool(nativeSize, conf.NPoller, 1024)
		messageHandlerExecutor = messageHandlerExecutePool.Go
	}

	// setup prefer protos: http2.0, other protos to be added
	preferenceProtos := map[string]struct{}{
		// "h2": {},
	}
	for _, v := range tlsConfig.NextProtos {
		delete(preferenceProtos, v)
	}
	for proto := range preferenceProtos {
		tlsConfig.NextProtos = append(tlsConfig.NextProtos, proto)
	}

	gopherConf := nbio.Config{
		Name:                     conf.Name,
		Network:                  conf.Network,
		Addrs:                    conf.Addrs,
		NPoller:                  conf.NPoller,
		NListener:                conf.NListener,
		ReadBufferSize:           conf.ReadBufferSize,
		MaxWriteBufferSize:       conf.MaxWriteBufferSize,
		MaxReadTimesPerEventLoop: conf.MaxReadTimesPerEventLoop,
		LockPoller:               conf.LockPoller,
		LockListener:             conf.LockListener,
	}
	g := nbio.NewGopher(gopherConf)
	g.Execute = messageHandlerExecutor

	svr := &Server{
		Gopher:                       g,
		_onOpen:                      func(c *nbio.Conn) {},
		_onClose:                     func(c *nbio.Conn, err error) {},
		_onStop:                      func() {},
		MaxLoad:                      conf.MaxLoad,
		MaxWebsocketFramePayloadSize: conf.MaxWebsocketFramePayloadSize,
		ReleaseWebsocketPayload:      conf.ReleaseWebsocketPayload,
		CheckUtf8:                    utf8.Valid,
		conns:                        map[*nbio.Conn]struct{}{},
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
		tlsConn := tls.NewConn(c, tlsConfig, isClient, true, mempool.DefaultMemPool)
		processor := NewServerProcessor(tlsConn, handler, conf.KeepaliveTime, conf.EnableSendfile)
		parser := NewParser(processor, false, conf.ReadLimit, c.Execute)
		parser.Conn = tlsConn
		parser.Server = svr
		processor.(*ServerProcessor).parser = parser
		c.SetSession(parser)
		c.SetReadDeadline(time.Now().Add(conf.KeepaliveTime))
	})
	g.OnClose(func(c *nbio.Conn, err error) {
		c.Execute(func() {
			parser := c.Session().(*Parser)
			if parser == nil {
				logging.Error("nil parser")
				return
			}
			parser.Conn.Close()
			parser.Close(err)
			svr._onClose(c, err)
			svr.mux.Lock()
			delete(svr.conns, c)
			svr.mux.Unlock()
		})
	})

	g.OnData(func(c *nbio.Conn, data []byte) {
		defer func() {
			if err := recover(); err != nil {
				const size = 64 << 10
				buf := make([]byte, size)
				buf = buf[:runtime.Stack(buf, false)]
				logging.Error("execute parser failed: %v\n%v\n", err, *(*string)(unsafe.Pointer(&buf)))
			}
		}()

		parser := c.Session().(*Parser)
		if parser == nil {
			logging.Error("nil parser")
			c.Close()
			return
		}
		if tlsConn, ok := parser.Processor.Conn().(*tls.Conn); ok {
			buffer := getBuffer(c)
			for {
				_, nread, err := tlsConn.AppendAndRead(data, buffer)
				data = nil
				if err != nil {
					c.CloseWithError(err)
					return
				}
				if nread > 0 {
					err := parser.Read(buffer[:nread])
					if err != nil {
						logging.Debug("parser.Read failed: %v", err)
						c.CloseWithError(err)
						return
					}
				}
				if nread == 0 {
					return
				}
			}
			// c.SetReadDeadline(time.Now().Add(conf.KeepaliveTime))
		}
	})
	g.OnWriteBufferRelease(func(c *nbio.Conn, buffer []byte) {
		mempool.Free(buffer)
	})

	g.OnStop(func() {
		svr._onStop()
		g.Execute = func(f func()) {}
		if messageHandlerExecutePool != nil {
			messageHandlerExecutePool.Stop()
		}
	})
	return svr
}
