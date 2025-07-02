// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbio

import (
	"context"
	"net"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/lesismal/nbio/logging"
	"github.com/lesismal/nbio/mempool"
	"github.com/lesismal/nbio/taskpool"
	"github.com/lesismal/nbio/timer"
)

const (
	// DefaultReadBufferSize .
	DefaultReadBufferSize = 1024 * 64

	// DefaultMaxWriteBufferSize .
	DefaultMaxWriteBufferSize = 0

	// DefaultMaxConnReadTimesPerEventLoop .
	DefaultMaxConnReadTimesPerEventLoop = 3

	// DefaultUDPReadTimeout .
	DefaultUDPReadTimeout = 120 * time.Second
)

const (
	NETWORK_TCP        = "tcp"
	NETWORK_TCP4       = "tcp4"
	NETWORK_TCP6       = "tcp6"
	NETWORK_UDP        = "udp"
	NETWORK_UDP4       = "udp4"
	NETWORK_UDP6       = "udp6"
	NETWORK_UNIX       = "unix"
	NETWORK_UNIXGRAM   = "unixgram"
	NETWORK_UNIXPACKET = "unixpacket"
)

var (
	// MaxOpenFiles .
	MaxOpenFiles = 1024 * 1024 * 2
)

// Config Of Engine.
type Config struct {
	// Name describes your gopher name for logging, it's set to "NB" by default.
	Name string

	// Network is the listening protocol, used with Addrs together.
	Network string

	// Addrs is the listening addr list for a nbio server.
	// if it is empty, no listener created, then the Engine is used for client by default.
	Addrs []string

	// NPoller represents poller goroutine num.
	NPoller int

	// ReadBufferSize represents buffer size for reading, it's set to 64k by default.
	ReadBufferSize int

	// MaxWriteBufferSize represents max write buffer size for Conn, 0 by default, represents no limit for writeBuffer
	// if MaxWriteBufferSize is set greater than to 0, and the connection's Send-Q is full and the data cached by nbio is
	// more than MaxWriteBufferSize, the connection would be closed by nbio.
	MaxWriteBufferSize int

	// MaxConnReadTimesPerEventLoop represents max read times in one poller loop for one fd
	MaxConnReadTimesPerEventLoop int

	// LockListener represents whether to lock thread for listener's goroutine, false by default.
	LockListener bool

	// LockPoller represents whether to lock thread for poller's goroutine, false by default.
	LockPoller bool

	// EpollMod sets the epoll mod, EPOLLLT by default.
	EpollMod uint32

	// EPOLLONESHOT sets EPOLLONESHOT, 0 by default.
	EPOLLONESHOT uint32

	// UDPReadTimeout sets the timeout for udp sessions.
	UDPReadTimeout time.Duration

	// Listen is used to create listener for Engine.
	// Users can set this func to customize listener, such as reuseport.
	Listen func(network, addr string) (net.Listener, error)

	// ListenUDP is used to create udp listener for Engine.
	ListenUDP func(network string, laddr *net.UDPAddr) (*net.UDPConn, error)

	// AsyncReadInPoller represents how the reading events and reading are handled
	// by epoll goroutine:
	// true : epoll goroutine handles the reading events only, another goroutine
	//        pool will handle the reading.
	// false: epoll goroutine handles both the reading events and the reading.
	AsyncReadInPoller bool
	// IOExecute is used to handle the aysnc reading, users can customize it.
	IOExecute func(f func(*[]byte))

	// BodyAllocator sets the buffer allocator for write cache.
	BodyAllocator mempool.Allocator
}

// Gopher keeps old type to compatible with new name Engine.
type Gopher = Engine

//go:norace
func NewGopher(conf Config) *Gopher {
	return NewEngine(conf)
}

// Engine is a manager of poller.
type Engine struct {
	Config
	*timer.Timer
	sync.WaitGroup

	Execute func(f func())
	mux     sync.Mutex

	isOneshot bool

	wgConn sync.WaitGroup

	// store std connections, for Windows only.
	connsStd map[*Conn]struct{}

	// store *nix connections.
	connsUnix []*Conn

	// listeners.
	listeners []*poller
	pollers   []*poller

	// onAcceptError is called when accept error.
	onAcceptError func(err error)

	// onUDPListen for udp listener created.
	onUDPListen func(c *Conn)
	// callback for new connection connected.
	onOpen func(c *Conn)
	// callback for connection closed.
	onClose func(c *Conn, err error)
	// callback for reading event.
	onRead func(c *Conn)
	// callback for coming data.
	onDataPtr func(c *Conn, pdata *[]byte)
	// callback for writing data size caculation.
	onWrittenSize func(c *Conn, b []byte, n int)
	// callback for allocationg the reading buffer.
	onReadBufferAlloc func(c *Conn) *[]byte
	// callback for freeing the reading buffer.
	onReadBufferFree func(c *Conn, pbuf *[]byte)

	// depreacated.
	// beforeRead  func(c *Conn)
	// afterRead   func(c *Conn)
	// beforeWrite func(c *Conn)

	// callback for Engine stop.
	onStop func()

	ioTaskPool *taskpool.IOTaskPool
}

// SetETAsyncRead .
//
//go:norace
func (e *Engine) SetETAsyncRead() {
	if e.NPoller <= 0 {
		e.NPoller = 1
	}
	e.EpollMod = EPOLLET
	e.AsyncReadInPoller = true
}

// SetLTSyncRead .
//
//go:norace
func (e *Engine) SetLTSyncRead() {
	if e.NPoller <= 0 {
		e.NPoller = runtime.NumCPU()
	}
	e.EpollMod = EPOLLLT
	e.AsyncReadInPoller = false
}

// Stop closes listeners/pollers/conns/timer.
//
//go:norace
func (g *Engine) Stop() {
	for _, l := range g.listeners {
		l.stop()
	}

	g.mux.Lock()
	conns := g.connsStd
	g.connsStd = map[*Conn]struct{}{}
	connsUnix := g.connsUnix
	g.mux.Unlock()

	g.wgConn.Done()
	for c := range conns {
		if c != nil {
			cc := c
			g.Async(func() {
				_ = cc.Close()
			})
		}
	}
	for _, c := range connsUnix {
		if c != nil {
			cc := c
			g.Async(func() {
				_ = cc.Close()
			})
		}
	}

	g.wgConn.Wait()

	g.onStop()

	g.Timer.Stop()

	if g.ioTaskPool != nil {
		g.ioTaskPool.Stop()
	}

	for i := 0; i < g.NPoller; i++ {
		g.pollers[i].stop()
	}

	g.Wait()
	logging.Info("NBIO[%v] stop", g.Name)
}

// Shutdown stops Engine gracefully with context.
//
//go:norace
func (g *Engine) Shutdown(ctx context.Context) error {
	ch := make(chan struct{})
	go func() {
		g.Stop()
		close(ch)
	}()

	select {
	case <-ch:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// AddConn adds conn to a poller.
//
//go:norace
func (g *Engine) AddConn(conn net.Conn) (*Conn, error) {
	c, err := NBConn(conn)
	if err != nil {
		return nil, err
	}

	p := g.pollers[c.Hash()%len(g.pollers)]
	err = p.addConn(c)
	if err != nil {
		return nil, err
	}
	return c, nil
}

//go:norace
func (g *Engine) addDialer(c *Conn) (*Conn, error) {
	p := g.pollers[c.Hash()%len(g.pollers)]
	err := p.addDialer(c)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// OnAcceptError is called when accept error.
//
//go:norace
func (g *Engine) OnAcceptError(h func(err error)) {
	g.onAcceptError = h
}

// OnOpen registers callback for new connection.
//
//go:norace
func (g *Engine) OnUDPListen(h func(c *Conn)) {
	if h == nil {
		panic("invalid handler: nil")
	}
	g.onUDPListen = h
}

// OnOpen registers callback for new connection.
//
//go:norace
func (g *Engine) OnOpen(h func(c *Conn)) {
	if h == nil {
		panic("invalid handler: nil")
	}
	g.onOpen = func(c *Conn) {
		g.wgConn.Add(1)
		h(c)
	}
}

// OnClose registers callback for disconnected.
//
//go:norace
func (g *Engine) OnClose(h func(c *Conn, err error)) {
	if h == nil {
		panic("invalid handler: nil")
	}
	g.onClose = func(c *Conn, err error) {
		g.Async(func() {
			defer g.wgConn.Done()
			h(c, err)
		})
	}
}

// OnRead registers callback for reading event.
//
//go:norace
func (g *Engine) OnRead(h func(c *Conn)) {
	g.onRead = h
}

// OnData registers callback for data.
//
//go:norace
func (g *Engine) OnData(h func(c *Conn, data []byte)) {
	if h == nil {
		panic("invalid handler: nil")
	}
	g.onDataPtr = func(c *Conn, pdata *[]byte) {
		h(c, *pdata)
	}
}

// OnDataPtr registers callback for data ptr.
//
//go:norace
func (g *Engine) OnDataPtr(h func(c *Conn, pdata *[]byte)) {
	if h == nil {
		panic("invalid handler: nil")
	}
	g.onDataPtr = h
}

// OnWrittenSize registers callback for written size.
// If len(b) is bigger than 0, it represents that it's writing a buffer,
// else it's operating by Sendfile.
//
//go:norace
func (g *Engine) OnWrittenSize(h func(c *Conn, b []byte, n int)) {
	if h == nil {
		panic("invalid handler: nil")
	}
	g.onWrittenSize = h
}

// OnReadBufferAlloc registers callback for memory allocating.
//
//go:norace
func (g *Engine) OnReadBufferAlloc(h func(c *Conn) *[]byte) {
	if h == nil {
		panic("invalid handler: nil")
	}
	g.onReadBufferAlloc = h
}

// OnReadBufferFree registers callback for memory release.
//
//go:norace
func (g *Engine) OnReadBufferFree(h func(c *Conn, pbuf *[]byte)) {
	if h == nil {
		panic("invalid handler: nil")
	}
	g.onReadBufferFree = h
}

// Depracated .
// OnWriteBufferRelease registers callback for write buffer memory release.
// func (g *Engine) OnWriteBufferRelease(h func(c *Conn, b []byte)) {
// 	if h == nil {
// 		panic("invalid handler: nil")
// 	}
// 	g.onWriteBufferFree = h
// }

// BeforeRead registers callback before syscall.Read
// the handler would be called on windows.
// func (g *Engine) BeforeRead(h func(c *Conn)) {
// 	if h == nil {
// 		panic("invalid handler: nil")
// 	}
// 	g.beforeRead = h
// }

// Depracated .
// AfterRead registers callback after syscall.Read
// the handler would be called on *nix.
// func (g *Engine) AfterRead(h func(c *Conn)) {
// 	if h == nil {
// 		panic("invalid handler: nil")
// 	}
// 	g.afterRead = h
// }

// Depracated .
// BeforeWrite registers callback befor syscall.Write and syscall.Writev
// the handler would be called on windows.
// func (g *Engine) BeforeWrite(h func(c *Conn)) {
// 	if h == nil {
// 		panic("invalid handler: nil")
// 	}
// 	g.beforeWrite = h
// }

// OnStop registers callback before Engine is stopped.
//
//go:norace
func (g *Engine) OnStop(h func()) {
	if h == nil {
		panic("invalid handler: nil")
	}
	g.onStop = h
}

// PollerBuffer returns Poller's buffer by Conn, can be used on linux/bsd.
//
//go:norace
func (g *Engine) PollerBuffer(c *Conn) []byte {
	return c.p.ReadBuffer
}

// PollerBufferPtr returns Poller's buffer by Conn, can be used on linux/bsd.
//
//go:norace
func (g *Engine) PollerBufferPtr(c *Conn) *[]byte {
	return &c.p.ReadBuffer
}

//go:norace
func (g *Engine) initHandlers() {
	g.wgConn.Add(1)
	g.OnOpen(func(c *Conn) {})
	g.OnClose(func(c *Conn, err error) {})
	// g.OnRead(func(c *Conn, b []byte) ([]byte, error) {
	// 	n, err := c.Read(b)
	// 	if n > 0 {
	// 		return b[:n], err
	// 	}
	// 	return nil, err
	// })
	g.OnData(func(c *Conn, data []byte) {})
	g.OnReadBufferAlloc(g.PollerBufferPtr)
	g.OnReadBufferFree(func(c *Conn, pbuf *[]byte) {})
	// g.OnWriteBufferRelease(func(c *Conn, buffer []byte) {})
	// g.BeforeRead(func(c *Conn) {})
	// g.AfterRead(func(c *Conn) {})
	// g.BeforeWrite(func(c *Conn) {})
	g.OnUDPListen(func(*Conn) {})
	g.OnStop(func() {})

	if g.Execute == nil {
		g.Execute = func(f func()) {
			defer func() {
				if err := recover(); err != nil {
					const size = 64 << 10
					buf := make([]byte, size)
					buf = buf[:runtime.Stack(buf, false)]
					logging.Error("execute failed: %v\n%v\n", err, *(*string)(unsafe.Pointer(&buf)))
				}
			}()
			f()
		}
	}
}

//go:norace
func (g *Engine) borrow(c *Conn) *[]byte {
	return g.onReadBufferAlloc(c)
}

//go:norace
func (g *Engine) payback(c *Conn, pbuf *[]byte) {
	*pbuf = (*pbuf)[:cap(*pbuf)]
	g.onReadBufferFree(c, pbuf)
}
