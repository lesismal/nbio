// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbhttp

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"runtime"
	"sync"
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

	// DefaultBlockingReadBufferSize sets to 4k.
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

	// NPoller represents poller goroutine num.
	NPoller int

	// ReadLimit represents the max size for parser reading, it's set to 64M by default.
	ReadLimit int

	// MaxHTTPBodySize represents the max size of HTTP body for parser reading.
	MaxHTTPBodySize int

	// ReadBufferSize represents buffer size for reading, it's set to 64k by default.
	ReadBufferSize int

	// MaxWriteBufferSize represents max write buffer size for Conn, 0 by default, represents no limit for writeBuffer
	// if MaxWriteBufferSize is set greater than to 0, and the connection's Send-Q is full and the data cached by nbio is
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

	// OnAcceptError is called when accept error.
	OnAcceptError func(err error)

	// Handler sets HTTP handler for Engine.
	Handler http.Handler

	// `OnRequest` sets HTTP handler which will be called before `Handler.ServeHTTP`.
	//
	// A `Request` is pushed into a task queue and waits to be executed by the goroutine pool by default, which means the `Request`
	// may not be executed at once and may wait for long to be executed: if the client-side supports `pipeline` and the previous
	// `Requests` are handled for long. In some scenarios, we need to know when the `Request` is received, then we can control and
	// customize whether we should drop the task or record the real processing time for the `Request`. That is what this func should
	// be used for.
	OnRequest http.HandlerFunc

	// ServerExecutor sets the executor for data reading callbacks.
	ServerExecutor func(f func())

	// ClientExecutor sets the executor for client callbacks.
	ClientExecutor func(f func())

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

	// Deprecated.
	// WebsocketCompressor .
	WebsocketCompressor func(w io.WriteCloser, level int) io.WriteCloser

	// Deprecated.
	// WebsocketDecompressor .
	WebsocketDecompressor func(r io.Reader) io.ReadCloser

	// AsyncReadInPoller represents how the reading events and reading are handled
	// by epoll goroutine:
	// true : epoll goroutine handles the reading events only, another goroutine
	//        pool will handles the reading.
	// false: epoll goroutine handles both the reading events and the reading.
	//        false is by defalt.
	AsyncReadInPoller bool
	// IOExecute is used to handle the aysnc reading, users can customize it.
	IOExecute func(f func(*[]byte))
}

// Engine .
type Engine struct {
	*nbio.Engine
	Config

	CheckUtf8 func(data []byte) bool

	shutdown bool

	listenerMux *lmux.ListenerMux
	listeners   []net.Listener

	_onAcceptError func(err error)
	_onOpen        func(c net.Conn)
	_onClose       func(c net.Conn, err error)
	_onStop        func()

	mux         sync.Mutex
	conns       map[connValue]struct{}
	dialerConns map[connValue]struct{}

	// tlsBuffers [][]byte
	// getTLSBuffer func(c *nbio.Conn) []byte

	emptyRequest *http.Request
	BaseCtx      context.Context
	Cancel       func()

	SyncCall      func(f func())
	ExecuteClient func(f func())

	// isOneshot bool
}

// OnAcceptError is called when accept error.
//
//go:norace
func (e *Engine) OnAcceptError(h func(err error)) {
	e._onAcceptError = h
	e.Engine.OnAcceptError(h)
}

// OnOpen registers callback for new connection.
//
//go:norace
func (e *Engine) OnOpen(h func(c net.Conn)) {
	e._onOpen = h
}

// OnClose registers callback for disconnected.
//
//go:norace
func (e *Engine) OnClose(h func(c net.Conn, err error)) {
	e._onClose = h
}

// OnStop registers callback before Engine is stopped.
//
//go:norace
func (e *Engine) OnStop(h func()) {
	e._onStop = h
}

// Online .
//
//go:norace
func (e *Engine) Online() int {
	return len(e.conns)
}

// DialerOnline .
//
//go:norace
func (e *Engine) DialerOnline() int {
	return len(e.dialerConns)
}

//go:norace
func (e *Engine) closeAllConns() {
	e.mux.Lock()
	defer e.mux.Unlock()
	for key := range e.conns {
		if c, err := array2Conn(key); err == nil {
			_ = c.Close()
		}
	}
	for key := range e.dialerConns {
		if c, err := array2Conn(key); err == nil {
			_ = c.Close()
		}
	}
}

type Conn struct {
	net.Conn
	Parser    *Parser
	Trasfered bool
}

//go:norace
func (e *Engine) listen(ln net.Listener, tlsConfig *tls.Config, addConn func(*Conn, *tls.Config, func()), decrease func()) {
	e.Add(1)
	go func() {
		defer func() {
			// ln.Close()
			e.Done()
		}()
		for !e.shutdown {
			conn, err := ln.Accept()
			if err == nil && !e.shutdown {
				addConn(&Conn{Conn: conn}, tlsConfig, decrease)
			} else {
				var ne net.Error
				if ok := errors.As(err, &ne); ok && ne.Timeout() {
					logging.Error("Accept failed: timeout error, retrying...")
					time.Sleep(time.Second / 20)
				} else {
					if !e.shutdown {
						logging.Error("Accept failed: %v, exit...", err)
					}
					if e._onAcceptError != nil {
						e._onAcceptError(err)
					}
				}
			}
		}
	}()
}

//go:norace
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
						_ = l.Close()
					}
					return err
				}
				conf.Addr = ln.Addr().String()
				if conf.pAddr != nil {
					*conf.pAddr = conf.Addr
				}
				logging.Info("NBHTTP Engine[%v] Serve HTTPS On: [%v@%v]", e.Engine.Name, conf.Network, conf.Addr)

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
						_ = l.Close()
					}
					return err
				}
				conf.Addr = ln.Addr().String()
				if conf.pAddr != nil {
					*conf.pAddr = conf.Addr
				}

				logging.Info("NBHTTP Engine[%v] Serve HTTP On: [%v@%v]", e.Engine.Name, conf.Network, conf.Addr)

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

//go:norace
func (e *Engine) stopListeners() {
	if e.IOMod == IOModMixed && e.listenerMux != nil {
		e.listenerMux.Stop()
	}
	for _, ln := range e.listeners {
		_ = ln.Close()
	}
}

// SetETAsyncRead .
//
//go:norace
func (e *Engine) SetETAsyncRead() {
	if e.NPoller <= 0 {
		e.NPoller = 1
	}
	e.EpollMod = nbio.EPOLLET
	e.AsyncReadInPoller = true
	e.Engine.SetETAsyncRead()
}

// SetLTSyncRead .
//
//go:norace
func (e *Engine) SetLTSyncRead() {
	if e.NPoller <= 0 {
		e.NPoller = runtime.NumCPU() / 4
		if e.NPoller == 0 {
			e.NPoller = 1
		}
	}
	e.EpollMod = nbio.EPOLLLT
	e.AsyncReadInPoller = false
	e.Engine.SetLTSyncRead()
}

// Start .
//
//go:norace
func (e *Engine) Start() error {
	modNames := map[int]string{
		IOModMixed:       "IOModMixed",
		IOModBlocking:    "IOModBlocking",
		IOModNonBlocking: "IOModNonBlocking",
	}

	err := e.Engine.Start()
	if err != nil {
		return err
	}

	if e.IOMod == IOModMixed {
		logging.Info("NBHTTP Engine[%v] Start with %q, MaxBlockingOnline: %v", e.Engine.Name, modNames[e.IOMod], e.MaxBlockingOnline)
	} else {
		logging.Info("NBHTTP Engine[%v] Start with %q", e.Engine.Name, modNames[e.IOMod])
	}

	err = e.startListeners()
	if err != nil {
		e.Engine.Stop()
		return err
	}
	return err
}

// Stop .
//
//go:norace
func (e *Engine) Stop() {
	e.shutdown = true

	if e.Cancel != nil {
		e.Cancel()
	}

	e.stopListeners()
	e.Engine.Stop()
}

// Shutdown .
//
//go:norace
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
			if len(e.conns)+len(e.dialerConns) == 0 {
				goto Exit
			}
		}
	}

Exit:
	err := e.Engine.Shutdown(ctx)
	logging.Info("NBIO[%v] shutdown", e.Engine.Name)
	return err
}

// DataHandler .
//
//go:norace
func (e *Engine) DataHandler(c *nbio.Conn, data []byte) {
	defer func() {
		if err := recover(); err != nil {
			const size = 64 << 10
			buf := make([]byte, size)
			buf = buf[:runtime.Stack(buf, false)]
			logging.Error("execute ParserCloser failed: %v\n%v\n", err, *(*string)(unsafe.Pointer(&buf)))
		}
	}()
	readerCloser := c.Session().(ParserCloser)
	if readerCloser == nil {
		logging.Error("nil ParserCloser")
		return
	}
	err := readerCloser.Parse(data)
	if err != nil {
		logging.Debug("ParserCloser.Read failed: %v", err)
		_ = c.CloseWithError(err)
	}
}

// TLSDataHandler .
//
//go:norace
func (e *Engine) TLSDataHandler(c *nbio.Conn, data []byte) {
	defer func() {
		if err := recover(); err != nil {
			const size = 64 << 10
			buf := make([]byte, size)
			buf = buf[:runtime.Stack(buf, false)]
			logging.Error("execute ParserCloser failed: %v\n%v\n", err, *(*string)(unsafe.Pointer(&buf)))
		}
	}()
	parserCloser := c.Session().(ParserCloser)
	if parserCloser == nil {
		logging.Error("nil ParserCloser")
		_ = c.Close()
		return
	}
	nbhttpConn, ok := parserCloser.UnderlayerConn().(*Conn)
	if ok {
		if tlsConn, ok := nbhttpConn.Conn.(*tls.Conn); ok {
			defer tlsConn.ResetOrFreeBuffer()

			readed := data
			buffer := data
			for {
				_, nread, err := tlsConn.AppendAndRead(readed, buffer)
				readed = nil
				if err != nil {
					_ = c.CloseWithError(err)
					return
				}
				if nread > 0 {
					parserCloser = c.Session().(ParserCloser)
					err := parserCloser.Parse(buffer[:nread])
					if err != nil {
						logging.Debug("ParserCloser.Read failed: %v", err)
						_ = c.CloseWithError(err)
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
}

// AddTransferredConn .
//
//go:norace
func (engine *Engine) AddTransferredConn(nbc *nbio.Conn) error {
	key, err := conn2Array(nbc)
	if err != nil {
		_ = nbc.Close()
		logging.Error("AddTransferredConn failed: %v", err)
		return err
	}

	engine.mux.Lock()
	if len(engine.conns) >= engine.MaxLoad {
		engine.mux.Unlock()
		_ = nbc.Close()
		logging.Error("AddTransferredConn failed: overload, already has %v online", engine.MaxLoad)
		return ErrServiceOverload
	}
	engine.conns[key] = struct{}{}
	engine.mux.Unlock()
	_, err = engine.AddConn(nbc)
	if err != nil {
		engine.mux.Lock()
		delete(engine.conns, key)
		engine.mux.Unlock()
		return err
	}
	engine._onOpen(nbc)
	return nil
}

// AddConnNonTLSNonBlocking .
//
//go:norace
func (engine *Engine) AddConnNonTLSNonBlocking(conn *Conn, tlsConfig *tls.Config, decrease func()) {
	nbc, err := nbio.NBConn(conn.Conn)
	if err != nil {
		_ = conn.Close()
		decrease()
		logging.Error("AddConnNonTLSNonBlocking failed: %v", err)
		return
	}
	conn.Conn = nbc
	if nbc.Session() != nil {
		_ = nbc.Close()
		decrease()
		logging.Error("AddConnNonTLSNonBlocking failed, invalid session: %v", nbc.Session())
		return
	}
	key, err := conn2Array(nbc)
	if err != nil {
		_ = nbc.Close()
		decrease()
		logging.Error("AddConnNonTLSNonBlocking failed: %v", err)
		return
	}

	engine.mux.Lock()
	if len(engine.conns) >= engine.MaxLoad {
		engine.mux.Unlock()
		_ = nbc.Close()
		decrease()
		logging.Error("AddConnNonTLSNonBlocking failed: overload, already has %v online", engine.MaxLoad)
		return
	}
	engine.conns[key] = struct{}{}
	engine.mux.Unlock()
	engine._onOpen(conn.Conn)
	processor := NewServerProcessor()
	parser := NewParser(conn, engine, processor, false, nbc.Execute)
	// if engine.isOneshot {
	// 	parser.Execute = SyncExecutor
	// }
	conn.Parser = parser
	nbc.SetSession(parser)
	nbc.OnData(engine.DataHandler)
	_, err = engine.AddConn(nbc)
	if err != nil {
		engine.mux.Lock()
		delete(engine.conns, key)
		engine.mux.Unlock()
		return
	}
	_ = nbc.SetReadDeadline(time.Now().Add(engine.KeepaliveTime))
}

// AddConnNonTLSBlocking .
//
//go:norace
func (engine *Engine) AddConnNonTLSBlocking(conn *Conn, tlsConfig *tls.Config, decrease func()) {
	engine.mux.Lock()
	if len(engine.conns) >= engine.MaxLoad {
		engine.mux.Unlock()
		_ = conn.Close()
		decrease()
		logging.Error("AddConnNonTLSBlocking failed: overload, already has %v online", engine.MaxLoad)
		return
	}
	switch vt := conn.Conn.(type) {
	case *net.TCPConn, *net.UnixConn:
		key, err := conn2Array(vt)
		if err != nil {
			engine.mux.Unlock()
			_ = conn.Close()
			decrease()
			logging.Error("AddConnNonTLSBlocking failed: %v", err)
			return
		}
		engine.conns[key] = struct{}{}
	default:
		engine.mux.Unlock()
		_ = conn.Close()
		decrease()
		logging.Error("AddConnNonTLSBlocking failed: unknown conn type: %v", vt)
		return
	}
	engine.mux.Unlock()
	engine._onOpen(conn)
	processor := NewServerProcessor()
	parser := NewParser(conn, engine, processor, false, SyncExecutor)
	parser.Engine = engine
	conn.Parser = parser
	_ = conn.SetReadDeadline(time.Now().Add(engine.KeepaliveTime))
	go engine.readConnBlocking(conn, parser, decrease)
}

// AddConnTLSNonBlocking .
//
//go:norace
func (engine *Engine) AddConnTLSNonBlocking(conn *Conn, tlsConfig *tls.Config, decrease func()) {
	nbc, err := nbio.NBConn(conn.Conn)
	if err != nil {
		_ = conn.Close()
		decrease()
		logging.Error("AddConnTLSNonBlocking failed: %v", err)
		return
	}
	conn.Conn = nbc
	if nbc.Session() != nil {
		_ = nbc.Close()
		decrease()
		logging.Error("AddConnTLSNonBlocking failed: session should not be nil")
		return
	}
	key, err := conn2Array(nbc)
	if err != nil {
		_ = nbc.Close()
		decrease()
		logging.Error("AddConnTLSNonBlocking failed: %v", err)
		return
	}

	engine.mux.Lock()
	if len(engine.conns) >= engine.MaxLoad {
		engine.mux.Unlock()
		_ = nbc.Close()
		decrease()
		logging.Error("AddConnTLSNonBlocking failed: overload, already has %v online", engine.MaxLoad)
		return
	}

	engine.conns[key] = struct{}{}
	engine.mux.Unlock()
	engine._onOpen(conn.Conn)

	isClient := false
	isNonBlock := true
	tlsConn := tls.NewConn(nbc, tlsConfig, isClient, isNonBlock, engine.TLSAllocator)
	conn = &Conn{Conn: tlsConn}
	processor := NewServerProcessor()
	parser := NewParser(conn, engine, processor, false, nbc.Execute)
	// if engine.isOneshot {
	// 	parser.Execute = SyncExecutor
	// }
	parser.Conn = conn
	parser.Engine = engine
	conn.Parser = parser
	nbc.SetSession(parser)

	nbc.OnData(engine.TLSDataHandler)
	_, err = engine.AddConn(nbc)
	if err != nil {
		engine.mux.Lock()
		delete(engine.conns, key)
		engine.mux.Unlock()
	}
	_ = nbc.SetReadDeadline(time.Now().Add(engine.KeepaliveTime))
}

// AddConnTLSBlocking .
//
//go:norace
func (engine *Engine) AddConnTLSBlocking(conn *Conn, tlsConfig *tls.Config, decrease func()) {
	engine.mux.Lock()
	if len(engine.conns) >= engine.MaxLoad {
		engine.mux.Unlock()
		_ = conn.Close()
		decrease()
		logging.Error("AddConnTLSBlocking failed: overload, already has %v online", engine.MaxLoad)
		return
	}

	underLayerConn := conn.Conn
	switch vt := underLayerConn.(type) {
	case *net.TCPConn, *net.UnixConn:
		key, err := conn2Array(vt)
		if err != nil {
			engine.mux.Unlock()
			_ = conn.Close()
			decrease()
			logging.Error("AddConnTLSBlocking failed: %v", err)
			return
		}
		engine.conns[key] = struct{}{}
	default:
		engine.mux.Unlock()
		_ = conn.Close()
		decrease()
		logging.Error("AddConnTLSBlocking unknown conn type: %v", vt)
		return
	}
	engine.mux.Unlock()
	engine._onOpen(conn)

	isClient := false
	isNonBlock := true
	tlsConn := tls.NewConn(underLayerConn, tlsConfig, isClient, isNonBlock, engine.TLSAllocator)
	conn = &Conn{Conn: tlsConn}
	processor := NewServerProcessor()
	parser := NewParser(conn, engine, processor, false, SyncExecutor)
	conn.Parser = parser
	_ = conn.SetReadDeadline(time.Now().Add(engine.KeepaliveTime))
	tlsConn.SetSession(parser)
	go engine.readTLSConnBlocking(conn, underLayerConn, tlsConn, parser, decrease)
}

//go:norace
func (engine *Engine) readConnBlocking(conn *Conn, parser *Parser, decrease func()) {
	var (
		n   int
		err error
	)

	readBufferPool := engine.ReadBufferPool
	if readBufferPool == nil {
		readBufferPool = getReadBufferPool(engine.BlockingReadBufferSize)
	}

	pbuf := readBufferPool.Malloc(engine.BlockingReadBufferSize)
	var parserCloser ParserCloser = parser
	defer func() {
		readBufferPool.Free(pbuf)
		if !conn.Trasfered {
			parserCloser.CloseAndClean(err)
		}
		engine.mux.Lock()
		switch vt := conn.Conn.(type) {
		case *net.TCPConn, *net.UnixConn:
			key, _ := conn2Array(vt)
			delete(engine.conns, key)
		}
		engine.mux.Unlock()
		engine._onClose(conn, err)
		decrease()
		// }()
	}()

	for {
		n, err = conn.Read(*pbuf)
		if err != nil {
			return
		}
		_ = parserCloser.Parse((*pbuf)[:n])
		if conn.Trasfered {
			parser.onClose = nil
			parser.CloseAndClean(nil)
			return
		}
		if parser != nil && parser.ParserCloser != nil {
			parserCloser = parser.ParserCloser
			parser.onClose = nil
			parser.CloseAndClean(nil)
			parser = nil
		}
	}
}

//go:norace
func (engine *Engine) readTLSConnBlocking(conn *Conn, rconn net.Conn, tlsConn *tls.Conn, parser *Parser, decrease func()) {
	var (
		err   error
		nread int
	)

	readBufferPool := engine.ReadBufferPool
	if readBufferPool == nil {
		readBufferPool = getReadBufferPool(engine.BlockingReadBufferSize)
	}
	pbuf := readBufferPool.Malloc(engine.BlockingReadBufferSize)
	var parserCloser ParserCloser = parser
	defer func() {
		readBufferPool.Free(pbuf)
		if !conn.Trasfered {
			parserCloser.CloseAndClean(err)
			_ = tlsConn.Close()
		}
		engine.mux.Lock()
		switch vt := rconn.(type) {
		case *net.TCPConn, *net.UnixConn:
			key, _ := conn2Array(vt)
			delete(engine.conns, key)
		}
		engine.mux.Unlock()
		engine._onClose(conn, err)
		decrease()
	}()

	for {
		nread, err = rconn.Read(*pbuf)
		if err != nil {
			return
		}

		readed := (*pbuf)[:nread]
		for {
			_, nread, err = tlsConn.AppendAndRead(readed, *pbuf)
			readed = nil
			if err != nil {
				return
			}
			if nread > 0 {
				err = parserCloser.Parse((*pbuf)[:nread])
				if err != nil {
					logging.Debug("parser.Read failed: %v", err)
					return
				}
				if conn.Trasfered {
					parser.onClose = nil
					parser.CloseAndClean(nil)
					return
				}
				if parser != nil && parser.ParserCloser != nil {
					parserCloser = parser.ParserCloser
					parser.onClose = nil
					parser.CloseAndClean(nil)
					parser = nil
				}
			}
			if nread == 0 {
				break
			}
		}
	}
}

// NewEngine .
//
//go:norace
func NewEngine(conf Config) *Engine {
	if conf.Name == "" {
		conf.Name = "NB"
	}
	if conf.MaxLoad <= 0 {
		conf.MaxLoad = DefaultMaxLoad
	}
	if conf.NPoller <= 0 {
		conf.NPoller = runtime.NumCPU() / 4
		if conf.AsyncReadInPoller && conf.EpollMod == nbio.EPOLLET {
			conf.NPoller = 1
		}
		if conf.NPoller == 0 {
			conf.NPoller = 1
		}
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

	var serverCall = func(f func()) { f() }
	var serverExecutor = conf.ServerExecutor
	var messageHandlerExecutePool *taskpool.TaskPool
	if serverExecutor == nil {
		if conf.MessageHandlerPoolSize <= 0 {
			conf.MessageHandlerPoolSize = runtime.NumCPU() * 1024
		}
		nativeSize := conf.MessageHandlerPoolSize - 1
		messageHandlerExecutePool = taskpool.New(nativeSize, 1024*64)
		serverExecutor = messageHandlerExecutePool.Go
		serverCall = messageHandlerExecutePool.Call
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
		EpollMod:                     conf.EpollMod,
		EPOLLONESHOT:                 conf.EPOLLONESHOT,
		AsyncReadInPoller:            conf.AsyncReadInPoller,
		IOExecute:                    conf.IOExecute,
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
		Config:        conf,
		_onOpen:       func(c net.Conn) {},
		_onClose:      func(c net.Conn, err error) {},
		_onStop:       func() {},
		CheckUtf8:     utf8.Valid,
		conns:         map[connValue]struct{}{},
		dialerConns:   map[connValue]struct{}{},
		ExecuteClient: clientExecutor,

		emptyRequest: (&http.Request{}).WithContext(baseCtx),
		BaseCtx:      baseCtx,
		Cancel:       cancel,
	}
	if engine.SyncCall == nil {
		engine.SyncCall = serverCall
	}

	// shouldSupportTLS := !conf.SupportServerOnly || len(conf.AddrsTLS) > 0
	// if shouldSupportTLS {
	// 	engine.InitTLSBuffers()
	// }

	// g.OnOpen(engine.ServerOnOpen)
	g.OnClose(func(c *nbio.Conn, err error) {
		c.MustExecute(func() {
			switch vt := c.Session().(type) {
			case ParserCloser:
				vt.CloseAndClean(err)
			default:
			}
			engine._onClose(c, err)
			engine.mux.Lock()
			key, _ := conn2Array(c)
			delete(engine.conns, key)
			delete(engine.dialerConns, key)
			engine.mux.Unlock()
		})
	})

	g.OnData(func(c *nbio.Conn, data []byte) {
		c.DataHandler()(c, data)
	})

	// engine.isOneshot = (conf.EpollMod == nbio.EPOLLET && conf.EPOLLONESHOT == nbio.EPOLLONESHOT && runtime.GOOS == "linux")
	// if engine.isOneshot {
	// 	readBufferPool := conf.ReadBufferPool
	// 	if readBufferPool == nil {
	// 		readBufferPool = getReadBufferPool(conf.ReadBufferSize)
	// 	}

	// 	g.OnRead(func(c *nbio.Conn) {
	// 		serverExecutor(func() {
	// 			buf := readBufferPool.Malloc(conf.ReadBufferSize)
	// 			defer func() {
	// 				readBufferPool.Free(buf)
	// 				c.ResetPollerEvent()
	// 			}()
	// 			for {
	// 				n, err := c.Read(buf)
	// 				if n > 0 && c.DataHandler != nil {
	// 					c.DataHandler(c, buf[:n])
	// 				}
	// 				if errors.Is(err, syscall.EINTR) {
	// 					continue
	// 				}
	// 				if errors.Is(err, syscall.EAGAIN) {
	// 					break
	// 				}
	// 				if err != nil {
	// 					c.CloseWithError(err)
	// 				}
	// 				if n < len(buf) {
	// 					return
	// 				}
	// 			}
	// 		})
	// 	})
	// }

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

//go:norace
func getReadBufferPool(size int) mempool.Allocator {
	pool, ok := ReadBufferPools.Load(size)
	if ok {
		readBufferPool, ok := pool.(mempool.Allocator)
		if ok {
			return readBufferPool
		}
	}
	readBufferPool := mempool.New(size, size*2)
	ReadBufferPools.Store(size, readBufferPool)
	return readBufferPool
}

//go:norace
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
