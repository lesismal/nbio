// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbhttp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
	"unsafe"

	"github.com/lesismal/llib/std/crypto/tls"
	"github.com/lesismal/nbio"
	"github.com/lesismal/nbio/lmux"
	"github.com/lesismal/nbio/logging"
	"github.com/lesismal/nbio/mempool"
	"github.com/lesismal/nbio/taskpool"
)

const (
	// IOModNonBlocking represents that the server serve all the connections by nbio poller goroutines to handle io events.
	IOModNonBlocking = 0
	// IOModBlocking represents that the server serve each connection with one goroutine at least to handle reading.
	IOModBlocking = 1
	// IOModMixed represents that the server creates listener mux to handle different connections, 1 listener will be dispatch to two ChanListener:
	// If ChanListener A's online is less than its max online num, the new connection will be dispatch to this listener A and served by single goroutine;
	// Else the new connection will be dispatch to ChanListener B and served by nbio poller.
	IOModMixed = 2

	// DefaultIOMod represents the default IO Mod used by nbhttp.Engine.
	DefaultIOMod = IOModNonBlocking
	// DefaultMaxBlockingOnline represents the default num of connections that will be dispatched to ChanListner A.
	DefaultMaxBlockingOnline = 10000
)

const (
	// DefaultMaxLoad .
	DefaultMaxLoad = 1024 * 1024

	// DefaultHTTPReadLimit .
	DefaultHTTPReadLimit = 1024 * 1024 * 64

	// DefaultMaxWebsocketFramePayloadSize .
	DefaultMaxWebsocketFramePayloadSize = 1024 * 32

	// DefaultKeepaliveTime .
	DefaultKeepaliveTime = time.Second * 120

	// DefaultBlockingReadBufferSize sets to 4k(<= goroutine stack size).
	DefaultBlockingReadBufferSize = 1024 * 4
)

const defaultNetwork = "tcp"

// ConfAddr .
type ConfAddr struct {
	Network   string
	Addr      string
	NListener int
	TLSConfig *tls.Config
	pAddr     *string
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

	// Listen is used to create listener for Engine.
	Listen func(network, addr string) (net.Listener, error)

	// ListenUDP is used to create udp listener for Engine.
	ListenUDP func(network string, laddr *net.UDPAddr) (*net.UDPConn, error)

	// MaxLoad represents the max online num, it's set to 10k by default.
	MaxLoad int

	// NListener represents listner goroutine num for each ConfAddr, it's set to 1 by default.
	NListener int

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

	// WriteTimeout represents Conn's write time out when response to a HTTP request.
	WriteTimeout time.Duration

	// KeepaliveTime represents Conn's ReadDeadline when waiting for a new request, it's set to 120s by default.
	KeepaliveTime time.Duration

	// LockListener represents listener's goroutine to lock thread or not, it's set to false by default.
	LockListener bool

	// LockPoller represents poller's goroutine to lock thread or not, it's set to false by default.
	LockPoller bool

	// DisableSendfile .
	DisableSendfile bool

	// ReleaseWebsocketPayload automatically release data buffer after function each call to websocket OnMessage or OnDataFrame.
	ReleaseWebsocketPayload bool

	// RetainHTTPBody represents whether to automatically release HTTP body's buffer after calling HTTP handler.
	RetainHTTPBody bool

	// MaxConnReadTimesPerEventLoop represents max read times in one poller loop for one fd.
	MaxConnReadTimesPerEventLoop int

	// Handler sets HTTP handler for Engine.
	Handler http.Handler

	// ServerExecutor sets the executor for data reading callbacks.
	ServerExecutor func(f func())

	// ClientExecutor sets the executor for client callbacks.
	ClientExecutor func(f func())

	// TimerExecutor sets the executor for timer callbacks.
	TimerExecutor func(f func())

	// TLSAllocator sets the buffer allocator for TLS.
	TLSAllocator tls.Allocator

	// BodyAllocator sets the buffer allocator for HTTP.
	BodyAllocator mempool.Allocator

	// Context sets common context for Engine.
	Context context.Context

	// Cancel sets the cancel func for common context.
	Cancel func()

	// SupportServerOnly .
	SupportServerOnly bool

	// IOMod represents io mod, it is set to IOModNonBlocking by default.
	IOMod int
	// MaxBlockingOnline represents max blocking conn's online num.
	MaxBlockingOnline int
	// BlockingReadBufferSize represents read buffer size of blocking mod.
	BlockingReadBufferSize int

	// EpollMod .
	EpollMod uint32
	// EPOLLONESHOT .
	EPOLLONESHOT uint32

	// ReadBufferPool .
	ReadBufferPool mempool.Allocator

	// WebsocketCompressor .
	WebsocketCompressor func() interface {
		Compress([]byte) []byte
		Close()
	}
	// WebsocketDecompressor .
	WebsocketDecompressor func() interface {
		Decompress([]byte) ([]byte, error)
		Close()
	}
}

// Engine .
type Engine struct {
	*nbio.Engine
	*Config

	CheckUtf8 func(data []byte) bool

	shutdown bool

	listenerMux *lmux.ListenerMux
	listeners   []net.Listener

	_onOpen  func(c net.Conn)
	_onClose func(c net.Conn, err error)
	_onStop  func()

	mux   sync.Mutex
	conns map[string]struct{}

	// tlsBuffers [][]byte
	// getTLSBuffer func(c *nbio.Conn) []byte

	emptyRequest *http.Request
	BaseCtx      context.Context
	Cancel       func()

	ExecuteClient func(f func())

	isOneshot bool
}

// OnOpen registers callback for new connection.
func (e *Engine) OnOpen(h func(c net.Conn)) {
	e._onOpen = h
}

// OnClose registers callback for disconnected.
func (e *Engine) OnClose(h func(c net.Conn, err error)) {
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

func (e *Engine) closeAllConns() {
	e.mux.Lock()
	defer e.mux.Unlock()
	for s := range e.conns {
		if c, err := string2Conn(s); err == nil {
			c.Close()
		}
	}
}

func (e *Engine) listen(ln net.Listener, tlsConfig *tls.Config, addConn func(net.Conn, *tls.Config, func()), decrease func()) {
	e.WaitGroup.Add(1)
	go func() {
		defer func() {
			// ln.Close()
			e.WaitGroup.Done()
		}()
		for !e.shutdown {
			conn, err := ln.Accept()
			if err == nil && !e.shutdown {
				addConn(conn, tlsConfig, decrease)
			} else {
				var ne net.Error
				if ok := errors.As(err, &ne); ok && ne.Timeout() {
					logging.Error("Accept failed: temporary error, retrying...")
					time.Sleep(time.Second / 20)
				} else {
					if !e.shutdown {
						logging.Error("Accept failed: %v, exit...", err)
					}
					break
				}
			}
		}
	}()
}

func (e *Engine) startListeners() error {
	if e.IOMod == IOModMixed {
		e.listenerMux = lmux.New(e.MaxBlockingOnline)
	}

	for i := range e.AddrConfigsTLS {
		conf := &e.AddrConfigsTLS[i]
		if conf.Addr != "" {
			network := conf.Network
			if network == "" {
				network = e.Network
			}
			if network == "" {
				network = defaultNetwork
			}
			for j := 0; j < conf.NListener; j++ {
				ln, err := e.Listen(network, conf.Addr)
				if err != nil {
					for _, l := range e.listeners {
						l.Close()
					}
					return err
				}
				conf.Addr = ln.Addr().String()
				if conf.pAddr != nil {
					*conf.pAddr = conf.Addr
				}
				logging.Info("Serve HTTPS On: [%v@%v]", conf.Network, conf.Addr)

				tlsConfig := conf.TLSConfig
				if tlsConfig == nil {
					tlsConfig = e.TLSConfig
					if tlsConfig == nil {
						tlsConfig = &tls.Config{}
					}
				}

				switch e.IOMod {
				case IOModMixed:
					lnA, lnB := e.listenerMux.Mux(ln)
					e.listen(lnA, tlsConfig, e.AddConnTLSBlocking, lnA.Decrease)
					e.listen(lnB, tlsConfig, e.AddConnTLSNonBlocking, func() {})
				case IOModBlocking:
					e.listeners = append(e.listeners, ln)
					e.listen(ln, tlsConfig, e.AddConnTLSBlocking, func() {})
				case IOModNonBlocking:
					e.listeners = append(e.listeners, ln)
					e.listen(ln, tlsConfig, e.AddConnTLSNonBlocking, func() {})
				}
			}
		}
	}

	for i := range e.AddrConfigs {
		conf := &e.AddrConfigs[i]
		if conf.Addr != "" {
			network := conf.Network
			if network == "" {
				network = e.Network
			}
			if network == "" {
				network = defaultNetwork
			}
			for j := 0; j < conf.NListener; j++ {
				ln, err := e.Listen(network, conf.Addr)
				if err != nil {
					for _, l := range e.listeners {
						l.Close()
					}
					return err
				}
				conf.Addr = ln.Addr().String()
				if conf.pAddr != nil {
					*conf.pAddr = conf.Addr
				}

				logging.Info("Serve HTTP On: [%v@%v]", conf.Network, conf.Addr)

				switch e.IOMod {
				case IOModMixed:
					lnA, lnB := e.listenerMux.Mux(ln)
					e.listen(lnA, nil, e.AddConnNonTLSBlocking, lnA.Decrease)
					e.listen(lnB, nil, e.AddConnNonTLSNonBlocking, func() {})
				case IOModBlocking:
					e.listeners = append(e.listeners, ln)
					e.listen(ln, nil, e.AddConnNonTLSBlocking, func() {})
				case IOModNonBlocking:
					e.listeners = append(e.listeners, ln)
					e.listen(ln, nil, e.AddConnNonTLSNonBlocking, func() {})
				}
			}
		}
	}

	e.listenerMux.Start()

	return nil
}

func (e *Engine) stopListeners() {
	if e.IOMod == IOModMixed && e.listenerMux != nil {
		e.listenerMux.Stop()
	}
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
	e.shutdown = true

	if e.Cancel != nil {
		e.Cancel()
	}

	e.stopListeners()
	e.Engine.Stop()
}

// Shutdown .
func (e *Engine) Shutdown(ctx context.Context) error {
	e.shutdown = true
	e.stopListeners()

	if e.Cancel != nil {
		e.Cancel()
	}

	defer e.closeAllConns()
	ticker := time.NewTicker(time.Millisecond * 200)
	defer ticker.Stop()
	for {
		e.closeAllConns()
		select {
		case <-ctx.Done():
			logging.Info("NBIO[%v] shutdown timeout", e.Engine.Name)
			return ctx.Err()
		case <-ticker.C:
			if len(e.conns) == 0 {
				goto Exit
			}
		}
	}

Exit:
	err := e.Engine.Shutdown(ctx)
	logging.Info("NBIO[%v] shutdown", e.Engine.Name)
	return err
}

// InitTLSBuffers .
// func (e *Engine) InitTLSBuffers() {
// 	if e.tlsBuffers != nil {
// 		return
// 	}
// 	e.tlsBuffers = make([][]byte, e.NParser)
// 	for i := 0; i < e.NParser; i++ {
// 		e.tlsBuffers[i] = make([]byte, e.ReadBufferSize)
// 	}

// 	e.getTLSBuffer = func(c *nbio.Conn) []byte {
// 		return e.tlsBuffers[uint64(c.Hash())%uint64(e.NParser)]
// 	}

// 	if runtime.GOOS == "windows" {
// 		bufferMux := sync.Mutex{}
// 		buffers := map[*nbio.Conn][]byte{}
// 		e.getTLSBuffer = func(c *nbio.Conn) []byte {
// 			bufferMux.Lock()
// 			defer bufferMux.Unlock()
// 			buf, ok := buffers[c]
// 			if !ok {
// 				buf = make([]byte, 4096)
// 				buffers[c] = buf
// 			}
// 			return buf
// 		}
// 	}
// }

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

		readed := data
		buffer := data
		for {
			_, nread, err := tlsConn.AppendAndRead(readed, buffer)
			readed = nil
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

// AddConnTLSNonBlocking .
func (engine *Engine) AddTransferredConn(nbc *nbio.Conn) error {
	key, err := conn2String(nbc)
	if err != nil {
		return err
	}

	engine.mux.Lock()
	if len(engine.conns) >= engine.MaxLoad {
		engine.mux.Unlock()
		nbc.Close()
		return ErrServiceOverload
	}
	engine.conns[key] = struct{}{}
	engine.mux.Unlock()
	engine._onOpen(nbc)
	engine.AddConn(nbc)
	return nil
}

// AddConnNonTLSNonBlocking .
func (engine *Engine) AddConnNonTLSNonBlocking(c net.Conn, tlsConfig *tls.Config, decrease func()) {
	nbc, err := nbio.NBConn(c)
	if err != nil {
		c.Close()
		return
	}
	if nbc.Session() != nil {
		return
	}

	key, err := conn2String(nbc)
	if err != nil {
		nbc.Close()
		return
	}

	engine.mux.Lock()
	if len(engine.conns) >= engine.MaxLoad {
		engine.mux.Unlock()
		nbc.Close()
		return
	}
	engine.conns[key] = struct{}{}
	engine.mux.Unlock()
	engine._onOpen(nbc)
	processor := NewServerProcessor(nbc, engine.Handler, engine.KeepaliveTime, !engine.DisableSendfile)
	parser := NewParser(processor, false, engine.ReadLimit, nbc.Execute)
	if engine.isOneshot {
		parser.Execute = SyncExecutor
	}
	parser.Engine = engine
	processor.(*ServerProcessor).parser = parser
	nbc.SetSession(parser)
	nbc.OnData(engine.DataHandler)
	engine.AddConn(nbc)
	nbc.SetReadDeadline(time.Now().Add(engine.KeepaliveTime))
}

// AddConnNonTLSBlocking .
func (engine *Engine) AddConnNonTLSBlocking(conn net.Conn, tlsConfig *tls.Config, decrease func()) {
	engine.mux.Lock()
	if len(engine.conns) >= engine.MaxLoad {
		engine.mux.Unlock()
		conn.Close()
		decrease()
		return
	}
	switch vt := conn.(type) {
	case *net.TCPConn, *net.UnixConn:
		key, err := conn2String(vt)
		if err != nil {
			engine.mux.Unlock()
			conn.Close()
			decrease()
			return
		}
		engine.conns[key] = struct{}{}
	default:
		engine.mux.Unlock()
		conn.Close()
		decrease()
		return
	}
	engine.mux.Unlock()
	engine._onOpen(conn)
	processor := NewServerProcessor(conn, engine.Handler, engine.KeepaliveTime, !engine.DisableSendfile)
	parser := NewParser(processor, false, engine.ReadLimit, SyncExecutor)
	parser.Engine = engine
	processor.(*ServerProcessor).parser = parser
	conn.SetReadDeadline(time.Now().Add(engine.KeepaliveTime))
	go engine.readConnBlocking(conn, parser, decrease)
}

// AddConnTLSNonBlocking .
func (engine *Engine) AddConnTLSNonBlocking(conn net.Conn, tlsConfig *tls.Config, decrease func()) {
	nbc, err := nbio.NBConn(conn)
	if err != nil {
		conn.Close()
		return
	}
	if nbc.Session() != nil {
		return
	}
	key, err := conn2String(nbc)
	if err == nil {
		nbc.Close()
		return
	}

	engine.mux.Lock()
	if len(engine.conns) >= engine.MaxLoad {
		engine.mux.Unlock()
		nbc.Close()
		return
	}

	engine.conns[key] = struct{}{}
	engine.mux.Unlock()
	engine._onOpen(nbc)

	isClient := false
	isNonBlock := true
	tlsConn := tls.NewConn(nbc, tlsConfig, isClient, isNonBlock, engine.TLSAllocator)
	processor := NewServerProcessor(tlsConn, engine.Handler, engine.KeepaliveTime, !engine.DisableSendfile)
	parser := NewParser(processor, false, engine.ReadLimit, nbc.Execute)
	if engine.isOneshot {
		parser.Execute = SyncExecutor
	}
	parser.Conn = tlsConn
	parser.Engine = engine
	processor.(*ServerProcessor).parser = parser
	nbc.SetSession(parser)

	nbc.OnData(engine.TLSDataHandler)
	engine.AddConn(nbc)
	nbc.SetReadDeadline(time.Now().Add(engine.KeepaliveTime))
}

// AddConnTLSBlocking .
func (engine *Engine) AddConnTLSBlocking(conn net.Conn, tlsConfig *tls.Config, decrease func()) {
	engine.mux.Lock()
	if len(engine.conns) >= engine.MaxLoad {
		engine.mux.Unlock()
		conn.Close()
		decrease()
		return
	}

	switch vt := conn.(type) {
	case *net.TCPConn, *net.UnixConn:
		key, err := conn2String(vt)
		if err != nil {
			engine.mux.Unlock()
			conn.Close()
			decrease()
			return
		}
		engine.conns[key] = struct{}{}
	default:
		engine.mux.Unlock()
		conn.Close()
		decrease()
		return
	}
	engine.mux.Unlock()
	engine._onOpen(conn)

	isClient := false
	isNonBlock := true
	tlsConn := tls.NewConn(conn, tlsConfig, isClient, isNonBlock, engine.TLSAllocator)
	processor := NewServerProcessor(tlsConn, engine.Handler, engine.KeepaliveTime, !engine.DisableSendfile)
	parser := NewParser(processor, false, engine.ReadLimit, SyncExecutor)
	parser.Conn = tlsConn
	parser.Engine = engine
	processor.(*ServerProcessor).parser = parser
	conn.SetReadDeadline(time.Now().Add(engine.KeepaliveTime))
	tlsConn.SetSession(parser)
	go engine.readTLSConnBlocking(conn, tlsConn, parser, decrease)
}

func (engine *Engine) readConnBlocking(conn net.Conn, parser *Parser, decrease func()) {
	var (
		n   int
		err error
		buf = make([]byte, engine.BlockingReadBufferSize)
	)

	defer func() {
		// go func() {
		parser.Close(err)
		engine.mux.Lock()
		switch vt := conn.(type) {
		case *net.TCPConn, *net.UnixConn:
			key, _ := conn2String(vt)
			delete(engine.conns, key)
		}
		engine.mux.Unlock()
		engine._onClose(conn, err)
		decrease()
		// }()
	}()

	for {
		n, err = conn.Read(buf)
		if err != nil {
			return
		}
		parser.Read(buf[:n])
	}
}

func (engine *Engine) readTLSConnBlocking(conn net.Conn, tlsConn *tls.Conn, parser *Parser, decrease func()) {
	var (
		err    error
		nread  int
		buffer = make([]byte, engine.BlockingReadBufferSize)
	)

	defer func() {
		// go func() {
		parser.Close(err)
		tlsConn.Close()
		engine.mux.Lock()
		switch vt := conn.(type) {
		case *net.TCPConn, *net.UnixConn:
			key, _ := conn2String(vt)
			delete(engine.conns, key)
		}
		engine.mux.Unlock()
		engine._onClose(conn, err)
		decrease()
		// }()
	}()

	for {
		nread, err = conn.Read(buffer)
		if err != nil {
			return
		}

		readed := buffer[:nread]
		for {
			_, nread, err = tlsConn.AppendAndRead(readed, buffer)
			readed = nil
			if err != nil {
				return
			}
			if nread > 0 {
				err = parser.Read(buffer[:nread])
				if err != nil {
					logging.Debug("parser.Read failed: %v", err)
					return
				}
			}
			if nread == 0 {
				break
			}
		}
	}
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
	if conf.BlockingReadBufferSize <= 0 {
		conf.BlockingReadBufferSize = DefaultBlockingReadBufferSize
	}
	if conf.Listen == nil {
		conf.Listen = net.Listen
	}
	if conf.ListenUDP == nil {
		conf.ListenUDP = net.ListenUDP
	}
	switch conf.IOMod {
	case IOModNonBlocking, IOModBlocking:
	case IOModMixed:
		if conf.MaxBlockingOnline <= 0 {
			conf.MaxBlockingOnline = DefaultMaxBlockingOnline
		}
	default:
		conf.IOMod = DefaultIOMod
	}

	var handler = conf.Handler
	if handler == nil {
		handler = http.NewServeMux()
	}
	conf.Handler = handler

	var serverExecutor = conf.ServerExecutor
	var messageHandlerExecutePool *taskpool.TaskPool
	if serverExecutor == nil {
		if conf.MessageHandlerPoolSize <= 0 {
			conf.MessageHandlerPoolSize = runtime.NumCPU() * 1024
		}
		nativeSize := conf.MessageHandlerPoolSize - 1
		messageHandlerExecutePool = taskpool.New(nativeSize, 1024*64)
		serverExecutor = messageHandlerExecutePool.Go
	}

	var clientExecutor = conf.ClientExecutor
	var clientExecutePool *taskpool.TaskPool
	var goExecutor = func(f func()) {
		go func() { // avoid deadlock
			defer func() {
				if err := recover(); err != nil {
					const size = 64 << 10
					buf := make([]byte, size)
					buf = buf[:runtime.Stack(buf, false)]
					logging.Error("ClientExecutor call failed: %v\n%v\n", err, *(*string)(unsafe.Pointer(&buf)))
				}
			}()
			f()
		}()
	}
	if clientExecutor == nil {
		if !conf.SupportServerOnly {
			clientExecutePool = taskpool.New(runtime.NumCPU()*1024-1, 1024*64)
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
		TimerExecute:                 conf.TimerExecutor,
		EpollMod:                     conf.EpollMod,
		EPOLLONESHOT:                 conf.EPOLLONESHOT,
	}
	g := nbio.NewEngine(gopherConf)
	g.Execute = serverExecutor

	// init non-tls addr configs
	for i, addr := range conf.Addrs {
		conf.AddrConfigs = append(conf.AddrConfigs, ConfAddr{Network: conf.Network, Addr: addr, pAddr: &conf.Addrs[i], NListener: conf.NListener})
	}
	for i := range conf.AddrConfigs {
		if conf.AddrConfigs[i].NListener <= 0 {
			conf.AddrConfigs[i].NListener = 1
		}
	}

	// init tls addr configs
	for i, addr := range conf.AddrsTLS {
		conf.AddrConfigsTLS = append(conf.AddrConfigsTLS, ConfAddr{Network: conf.Network, Addr: addr, pAddr: &conf.AddrsTLS[i], NListener: conf.NListener})
	}
	for i := range conf.AddrConfigsTLS {
		if conf.AddrConfigsTLS[i].NListener <= 0 {
			conf.AddrConfigsTLS[i].NListener = 1
		}
	}

	engine := &Engine{
		Engine:        g,
		Config:        &conf,
		_onOpen:       func(c net.Conn) {},
		_onClose:      func(c net.Conn, err error) {},
		_onStop:       func() {},
		CheckUtf8:     utf8.Valid,
		conns:         map[string]struct{}{},
		ExecuteClient: clientExecutor,

		emptyRequest: (&http.Request{}).WithContext(baseCtx),
		BaseCtx:      baseCtx,
		Cancel:       cancel,
	}

	// shouldSupportTLS := !conf.SupportServerOnly || len(conf.AddrsTLS) > 0
	// if shouldSupportTLS {
	// 	engine.InitTLSBuffers()
	// }

	// g.OnOpen(engine.ServerOnOpen)
	g.OnClose(func(c *nbio.Conn, err error) {
		c.MustExecute(func() {
			switch vt := c.Session().(type) {
			case *Parser:
				vt.Close(err)
			case interface {
				CloseAndClean(error)
			}:
				vt.CloseAndClean(err)
			default:
			}
			engine._onClose(c, err)
			engine.mux.Lock()
			key, _ := conn2String(c)
			delete(engine.conns, key)
			engine.mux.Unlock()
		})
	})

	g.OnData(func(c *nbio.Conn, data []byte) {
		c.DataHandler(c, data)
	})

	engine.isOneshot = (conf.EpollMod == nbio.EPOLLET && conf.EPOLLONESHOT == nbio.EPOLLONESHOT && runtime.GOOS == "linux")
	if engine.isOneshot {
		readBufferPool := conf.ReadBufferPool
		if readBufferPool == nil {
			pool, ok := ReadBufferPools.Load(conf.ReadBufferSize)
			if ok {
				readBufferPool, ok = pool.(mempool.Allocator)
			}
			if !ok {
				readBufferPool = mempool.New(conf.ReadBufferSize, conf.ReadBufferSize*2)
				ReadBufferPools.Store(conf.ReadBufferSize, readBufferPool)
			}
		}

		g.OnRead(func(c *nbio.Conn) {
			serverExecutor(func() {
				buf := readBufferPool.Malloc(conf.ReadBufferSize)
				defer func() {
					readBufferPool.Free(buf)
					c.ResetPollerEvent()
				}()
				for {
					n, err := c.Read(buf)
					if n > 0 && c.DataHandler != nil {
						c.DataHandler(c, buf[:n])
					}
					if errors.Is(err, syscall.EINTR) {
						continue
					}
					if errors.Is(err, syscall.EAGAIN) {
						break
					}
					if err != nil {
						c.CloseWithError(err)
					}
					if n < len(buf) {
						return
					}
				}
			})
		})
	}

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

var ReadBufferPools = &sync.Map{}

func SyncExecutor(f func()) bool {
	defer func() {
		if err := recover(); err != nil {
			const size = 64 << 10
			buf := make([]byte, size)
			buf = buf[:runtime.Stack(buf, false)]
			logging.Error("ProtocolExecutor call failed: %v\n%v\n", err, *(*string)(unsafe.Pointer(&buf)))
		}
	}()
	f()
	return true
}
