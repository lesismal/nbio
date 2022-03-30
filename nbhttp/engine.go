// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbhttp

import (
	"context"
	"errors"
	"math/rand"
	"net"
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

	// DefaultMaxWebsocketFramePayloadSize .
	DefaultMaxWebsocketFramePayloadSize = 1024 * 32

	// DefaultMessageHandlerPoolSize .
	// DefaultMessageHandlerPoolSize = runtime.NumCPU() * 256.

	// DefaultMessageHandlerTaskIdleTime .
	DefaultMessageHandlerTaskIdleTime = time.Second * 60

	// DefaultKeepaliveTime .
	DefaultKeepaliveTime = time.Second * 120

	// DefaultTLSHandshakeTimeout .
	DefaultTLSHandshakeTimeout = time.Second * 10
)

const defaultNetwork = "tcp"

// ConfAddr .
type ConfAddr struct {
	Network   string
	Addr      string
	TLSConfig *tls.Config
}

// Config .
type Config struct {
	// Name describes your gopher name for logging, it's set to "NB" by default.
	Name string

	// Network is the global listening protocol, used with Addrs toghter.
	// tcp* supported only by now, there's no plan for other protocol such as udp,
	// because it's too easy to write udp server/client.
	Network string

	// TLSConfig is the global tls config for all tls addrs.
	TLSConfig *tls.Config

	// Addrs is the non-tls listening addr list for an Engine.
	// if it is empty, no listener created, then the Engine is used for client by default.
	Addrs []string

	// AddrsTLS is the tls listening addr list for an Engine.
	// Engine will create listeners by AddrsTLS if it's not empty.
	AddrsTLS []string

	// AddrConfigs is the non-tls listening addr details list for an Engine.
	AddrConfigs []ConfAddr

	// AddrConfigsTLS is the tls listening addr details list for an Engine.
	AddrConfigsTLS []ConfAddr

	// MaxLoad represents the max online num, it's set to 10k by default.
	MaxLoad int

	// NPoller represents poller goroutine num, it's set to runtime.NumCPU() by default.
	NPoller int

	// NParser represents parser goroutine num, it's set to NPoller by default.
	NParser int

	// ReadLimit represents the max size for parser reading, it's set to 64M by default.
	ReadLimit int

	// ReadBufferSize represents buffer size for reading, it's set to 32k by default.
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

	// DisableSendfile .
	DisableSendfile bool

	// ReleaseWebsocketPayload automatically release data buffer after function each call to websocket OnMessage and OnDataFrame
	ReleaseWebsocketPayload bool

	// MaxConnReadTimesPerEventLoop represents max read times in one poller loop for one fd
	MaxConnReadTimesPerEventLoop int

	Handler http.Handler

	ServerExecutor func(f func())

	ClientExecutor func(f func())

	TLSAllocator tls.Allocator

	BodyAllocator mempool.Allocator

	Context context.Context
	Cancel  func()

	SupportServerOnly bool
}

// Engine .
type Engine struct {
	*nbio.Engine
	*Config

	MaxLoad                      int
	MaxWebsocketFramePayloadSize int
	ReleaseWebsocketPayload      bool
	CheckUtf8                    func(data []byte) bool

	listeners []net.Listener

	_onOpen  func(c *nbio.Conn)
	_onClose func(c *nbio.Conn, err error)
	_onStop  func()

	mux   sync.Mutex
	conns map[*nbio.Conn]struct{}

	tlsBuffers   [][]byte
	getTLSBuffer func(c *nbio.Conn) []byte

	emptyRequest *http.Request
	BaseCtx      context.Context
	Cancel       func()

	ExecuteClient func(f func())
}

// OnOpen registers callback for new connection.
func (e *Engine) OnOpen(h func(c *nbio.Conn)) {
	e._onOpen = h
}

// OnClose registers callback for disconnected.
func (e *Engine) OnClose(h func(c *nbio.Conn, err error)) {
	e._onClose = h
}

// OnStop registers callback before Engine is stopped.
func (e *Engine) OnStop(h func()) {
	e._onStop = h
}

// Online .
func (e *Engine) Online() int {
	return len(e.conns)
}

func (e *Engine) closeIdleConns(chCloseQueue chan *nbio.Conn) {
	e.mux.Lock()
	defer e.mux.Unlock()
	for c := range e.conns {
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

func (e *Engine) startListeners() error {
	for _, conf := range e.AddrConfigsTLS {
		if conf.Addr != "" {
			network := conf.Network
			if network == "" {
				network = e.Network
			}
			if network == "" {
				network = defaultNetwork
			}
			ln, err := net.Listen(network, conf.Addr)
			if err != nil {
				for _, l := range e.listeners {
					l.Close()
				}
				return err
			}
			e.listeners = append(e.listeners, ln)

			logging.Info("Serve     TLS On: [%v]", conf.Addr)
			e.WaitGroup.Add(1)

			tlsConfig := conf.TLSConfig
			if tlsConfig == nil {
				tlsConfig = e.TLSConfig
				if tlsConfig == nil {
					tlsConfig = &tls.Config{}
				}
			}

			go func() {
				defer func() {
					ln.Close()
					e.WaitGroup.Done()
				}()
				for {
					conn, err := ln.Accept()
					if err == nil {
						e.AddConnTLS(conn, tlsConfig)
					} else {
						var ne net.Error
						if ok := errors.As(err, &ne); ok && ne.Temporary() {
							logging.Error("Accept failed: temporary error, retrying...")
							time.Sleep(time.Second / 20)
						} else {
							logging.Error("Accept failed: %v, exit...", err)
							break
						}
					}
				}
			}()
		}
	}

	for _, conf := range e.AddrConfigs {
		if conf.Addr != "" {
			network := conf.Network
			if network == "" {
				network = e.Network
			}
			if network == "" {
				network = defaultNetwork
			}
			ln, err := net.Listen(network, conf.Addr)
			if err != nil {
				for _, l := range e.listeners {
					l.Close()
				}
				return err
			}
			e.listeners = append(e.listeners, ln)

			logging.Info("Serve  NonTLS On: [%v]", conf.Addr)
			e.WaitGroup.Add(1)
			go func() {
				defer func() {
					ln.Close()
					e.WaitGroup.Done()
				}()
				for {
					conn, err := ln.Accept()
					if err == nil {
						e.AddConnNonTLS(conn)
					} else {
						var ne net.Error
						if ok := errors.As(err, &ne); ok && ne.Temporary() {
							logging.Error("Accept failed: temporary error, retrying...")
							time.Sleep(time.Second / 20)
						} else {
							logging.Error("Accept failed: %v, exit...", err)
							break
						}
					}
				}
			}()
		}
	}

	return nil
}

func (e *Engine) stopListeners() {
	for _, ln := range e.listeners {
		ln.Close()
	}
}

// Start .
func (e *Engine) Start() error {
	err := e.Engine.Start()
	if err != nil {
		return err
	}
	err = e.startListeners()
	if err != nil {
		e.Engine.Stop()
		return err
	}
	return err
}

// Stop .
func (e *Engine) Stop() {
	e.stopListeners()
	e.Engine.Stop()
}

// Shutdown .
func (e *Engine) Shutdown(ctx context.Context) error {
	e.stopListeners()

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

	if e.Cancel != nil {
		e.Cancel()
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
		e.closeIdleConns(chCloseQueue)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			if len(e.conns) == 0 {
				goto Exit
			}
			timer.Reset(nextPollInterval())
		}
	}

Exit:
	err := e.Engine.Shutdown(ctx)
	logging.Info("NBIO[%v] shutdown", e.Engine.Name)
	return err
}

// InitTLSBuffers .
func (e *Engine) InitTLSBuffers() {
	if e.tlsBuffers != nil {
		return
	}
	e.tlsBuffers = make([][]byte, e.NParser)
	for i := 0; i < e.NParser; i++ {
		e.tlsBuffers[i] = make([]byte, e.ReadBufferSize)
	}

	e.getTLSBuffer = func(c *nbio.Conn) []byte {
		return e.tlsBuffers[uint64(c.Hash())%uint64(e.NParser)]
	}

	if runtime.GOOS == "windows" {
		bufferMux := sync.Mutex{}
		buffers := map[*nbio.Conn][]byte{}
		e.getTLSBuffer = func(c *nbio.Conn) []byte {
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
}

// TLSBuffer .
func (e *Engine) TLSBuffer(c *nbio.Conn) []byte {
	return e.getTLSBuffer(c)
}

// DataHandler .
func (e *Engine) DataHandler(c *nbio.Conn, data []byte) {
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
}

// TLSDataHandler .
func (e *Engine) TLSDataHandler(c *nbio.Conn, data []byte) {
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
		defer tlsConn.ResetOrFreeBuffer()

		buffer := e.getTLSBuffer(c)
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
}

// AddConnNonTLS .
func (engine *Engine) AddConnNonTLS(c net.Conn) {
	nbc, err := nbio.NBConn(c)
	if err != nil {
		c.Close()
		return
	}
	if nbc.Session() != nil {
		return
	}
	engine.mux.Lock()
	if len(engine.conns) >= engine.MaxLoad {
		engine.mux.Unlock()
		c.Close()
		return
	}
	engine.conns[nbc] = struct{}{}
	engine.mux.Unlock()
	engine._onOpen(nbc)
	processor := NewServerProcessor(nbc, engine.Handler, engine.KeepaliveTime, !engine.DisableSendfile)
	parser := NewParser(processor, false, engine.ReadLimit, nbc.Execute)
	parser.Engine = engine
	processor.(*ServerProcessor).parser = parser
	nbc.SetSession(parser)
	nbc.OnData(engine.DataHandler)
	engine.AddConn(nbc)
	nbc.SetReadDeadline(time.Now().Add(engine.KeepaliveTime))
}

// AddConnTLS .
func (engine *Engine) AddConnTLS(conn net.Conn, tlsConfig *tls.Config) {
	nbc, err := nbio.NBConn(conn)
	if err != nil {
		conn.Close()
		return
	}
	if nbc.Session() != nil {
		return
	}
	engine.mux.Lock()
	if len(engine.conns) >= engine.MaxLoad {
		engine.mux.Unlock()
		nbc.Close()
		return
	}
	engine.conns[nbc] = struct{}{}
	engine.mux.Unlock()
	engine._onOpen(nbc)

	isClient := false
	isNonBlock := true
	tlsConn := tls.NewConn(nbc, tlsConfig, isClient, isNonBlock, engine.TLSAllocator)
	processor := NewServerProcessor(tlsConn, engine.Handler, engine.KeepaliveTime, !engine.DisableSendfile)
	parser := NewParser(processor, false, engine.ReadLimit, nbc.Execute)
	parser.Conn = tlsConn
	parser.Engine = engine
	processor.(*ServerProcessor).parser = parser
	nbc.SetSession(parser)

	nbc.OnData(engine.TLSDataHandler)
	engine.AddConn(nbc)
	nbc.SetReadDeadline(time.Now().Add(engine.KeepaliveTime))
}

// NewEngine .
func NewEngine(conf Config) *Engine {
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
	if conf.TLSAllocator == nil {
		conf.TLSAllocator = mempool.DefaultMemPool
	}
	if conf.BodyAllocator == nil {
		conf.BodyAllocator = mempool.DefaultMemPool
	}

	var handler = conf.Handler
	if handler == nil {
		handler = http.NewServeMux()
	}
	conf.Handler = handler

	var serverExecutor = conf.ServerExecutor
	var messageHandlerExecutePool *taskpool.MixedPool
	if serverExecutor == nil {
		if conf.MessageHandlerPoolSize <= 0 {
			conf.MessageHandlerPoolSize = runtime.NumCPU() * 1024
		}
		nativeSize := conf.MessageHandlerPoolSize - 1
		messageHandlerExecutePool = taskpool.NewMixedPool(nativeSize, 1, 1024*1024, true)
		serverExecutor = messageHandlerExecutePool.Go
	}

	var clientExecutor = conf.ClientExecutor
	var clientExecutePool *taskpool.MixedPool
	var goExecutor = func(f func()) {
		go func() { // avoid deadlock
			defer func() {
				if err := recover(); err != nil {
					const size = 64 << 10
					buf := make([]byte, size)
					buf = buf[:runtime.Stack(buf, false)]
					logging.Error("clientExecutor call failed: %v\n%v\n", err, *(*string)(unsafe.Pointer(&buf)))
				}
			}()
			f()
		}()
	}
	if clientExecutor == nil {
		if !conf.SupportServerOnly {
			clientExecutePool = taskpool.NewMixedPool(runtime.NumCPU()*1024-1, 1, 1024*1024)
			clientExecutor = clientExecutePool.Go
		} else {
			clientExecutor = goExecutor
		}
	}

	baseCtx, cancel := conf.Context, conf.Cancel
	if baseCtx == nil {
		baseCtx, cancel = context.WithCancel(context.Background())
	}

	gopherConf := nbio.Config{
		Name:                         conf.Name,
		Network:                      conf.Network,
		NPoller:                      conf.NPoller,
		ReadBufferSize:               conf.ReadBufferSize,
		MaxWriteBufferSize:           conf.MaxWriteBufferSize,
		MaxConnReadTimesPerEventLoop: conf.MaxConnReadTimesPerEventLoop,
		LockPoller:                   conf.LockPoller,
		LockListener:                 conf.LockListener,
	}
	g := nbio.NewEngine(gopherConf)
	g.Execute = serverExecutor

	for _, addr := range conf.Addrs {
		conf.AddrConfigs = append(conf.AddrConfigs, ConfAddr{Addr: addr})
	}
	for _, addr := range conf.AddrsTLS {
		conf.AddrConfigsTLS = append(conf.AddrConfigsTLS, ConfAddr{Addr: addr})
	}

	engine := &Engine{
		Engine:                       g,
		Config:                       &conf,
		_onOpen:                      func(c *nbio.Conn) {},
		_onClose:                     func(c *nbio.Conn, err error) {},
		_onStop:                      func() {},
		MaxLoad:                      conf.MaxLoad,
		MaxWebsocketFramePayloadSize: conf.MaxWebsocketFramePayloadSize,
		ReleaseWebsocketPayload:      conf.ReleaseWebsocketPayload,
		CheckUtf8:                    utf8.Valid,
		conns:                        map[*nbio.Conn]struct{}{},
		ExecuteClient:                clientExecutor,

		emptyRequest: (&http.Request{}).WithContext(baseCtx),
		BaseCtx:      baseCtx,
		Cancel:       cancel,
	}

	shouldSupportTLS := !conf.SupportServerOnly || len(conf.AddrsTLS) > 0
	if shouldSupportTLS {
		engine.InitTLSBuffers()
	}

	// g.OnOpen(engine.ServerOnOpen)
	g.OnClose(func(c *nbio.Conn, err error) {
		c.MustExecute(func() {
			parser, ok := c.Session().(*Parser)
			if ok && parser != nil {
				parser.Close(err)
			}
			engine._onClose(c, err)
			engine.mux.Lock()
			delete(engine.conns, c)
			engine.mux.Unlock()
		})
	})
	g.OnData(func(c *nbio.Conn, data []byte) {
		if c.DataHandler != nil {
			c.DataHandler(c, data)
		}
	})
	// g.OnWriteBufferRelease(func(c *nbio.Conn, buffer []byte) {
	// 	mempool.Free(buffer)
	// })
	g.OnStop(func() {
		engine._onStop()
		g.Execute = func(f func()) {}
		if messageHandlerExecutePool != nil {
			messageHandlerExecutePool.Stop()
		}
		engine.ExecuteClient = goExecutor
		if clientExecutePool != nil {
			clientExecutePool.Stop()
		}
	})
	return engine
}
