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
	"github.com/lesismal/nbio/timer"
)

const (
	// DefaultReadBufferSize .
	DefaultReadBufferSize = 1024 * 64

	// DefaultMaxWriteBufferSize .
	DefaultMaxWriteBufferSize = 1024 * 1024

	// DefaultMaxConnReadTimesPerEventLoop .
	DefaultMaxConnReadTimesPerEventLoop = 3

	// DefaultUDPReadTimeout .
	DefaultUDPReadTimeout = 120 * time.Second
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
	// tcp* supported only by now, there's no plan for other protocol such as udp,
	// because it's too easy to write udp server/client.
	Network string

	// Addrs is the listening addr list for a nbio server.
	// if it is empty, no listener created, then the Engine is used for client by default.
	Addrs []string

	// NPoller represents poller goroutine num, it's set to runtime.NumCPU() by default.
	NPoller int

	// ReadBufferSize represents buffer size for reading, it's set to 64k by default.
	ReadBufferSize int

	// MaxWriteBufferSize represents max write buffer size for Conn, it's set to 1m by default.
	// if the connection's Send-Q is full and the data cached by nbio is
	// more than MaxWriteBufferSize, the connection would be closed by nbio.
	MaxWriteBufferSize int

	// MaxConnReadTimesPerEventLoop represents max read times in one poller loop for one fd
	MaxConnReadTimesPerEventLoop int

	// LockListener represents listener's goroutine to lock thread or not, it's set to false by default.
	LockListener bool

	// LockPoller represents poller's goroutine to lock thread or not, it's set to false by default.
	LockPoller bool

	// EpollMod sets the epoll mod, EPOLLLT by default.
	EpollMod uint32

	// EPOLLONESHOT .
	EPOLLONESHOT uint32

	// UDPReadTimeout sets the timeout for udp sessions.
	UDPReadTimeout time.Duration

	// TimerExecute sets the executor for timer callbacks.
	TimerExecute func(f func())

	// Listen is used to create listener for Engine.
	Listen func(network, addr string) (net.Listener, error)

	// ListenUDP is used to create udp listener for Engine.
	ListenUDP func(network string, laddr *net.UDPAddr) (*net.UDPConn, error)
}

// Gopher keeps old type to compatible with new name Engine.
type Gopher = Engine

func NewGopher(conf Config) *Gopher {
	return NewEngine(conf)
}

// Engine is a manager of poller.
type Engine struct {
	*timer.Timer
	sync.WaitGroup

	Name string

	Execute      func(f func())
	TimerExecute func(f func())

	mux sync.Mutex

	wgConn sync.WaitGroup

	network   string
	addrs     []string
	listen    func(network, addr string) (net.Listener, error)
	listenUDP func(network string, laddr *net.UDPAddr) (*net.UDPConn, error)

	pollerNum                    int
	readBufferSize               int
	maxWriteBufferSize           int
	maxConnReadTimesPerEventLoop int
	udpReadTimeout               time.Duration
	epollMod                     uint32
	epollOneshot                 uint32
	lockListener                 bool
	lockPoller                   bool

	connsStd  map[*Conn]struct{}
	connsUnix []*Conn

	listeners []*poller
	pollers   []*poller

	onOpen            func(c *Conn)
	onClose           func(c *Conn, err error)
	onRead            func(c *Conn)
	onData            func(c *Conn, data []byte)
	onWrittenSize     func(c *Conn, b []byte, n int)
	onReadBufferAlloc func(c *Conn) []byte
	onReadBufferFree  func(c *Conn, buffer []byte)
	// onWriteBufferFree func(c *Conn, buffer []byte)
	beforeRead  func(c *Conn)
	afterRead   func(c *Conn)
	beforeWrite func(c *Conn)
	onStop      func()
}

// Stop closes listeners/pollers/conns/timer.
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
				cc.Close()
			})
		}
	}
	for _, c := range connsUnix {
		if c != nil {
			cc := c
			g.Async(func() {
				cc.Close()
			})
		}
	}

	g.wgConn.Wait()
	time.Sleep(time.Second / 5)

	g.onStop()

	g.Timer.Stop()

	for i := 0; i < g.pollerNum; i++ {
		g.pollers[i].stop()
	}

	g.Wait()
	logging.Info("NBIO[%v] stop", g.Name)
}

// Shutdown stops Engine gracefully with context.
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
func (g *Engine) AddConn(conn net.Conn) (*Conn, error) {
	c, err := NBConn(conn)
	if err != nil {
		return nil, err
	}

	p := g.pollers[c.Hash()%len(g.pollers)]
	p.addConn(c)
	return c, nil
}

// OnOpen registers callback for new connection.
func (g *Engine) OnOpen(h func(c *Conn)) {
	if h == nil {
		panic("invalid nil handler")
	}
	g.onOpen = func(c *Conn) {
		g.wgConn.Add(1)
		h(c)
	}
}

// OnClose registers callback for disconnected.
func (g *Engine) OnClose(h func(c *Conn, err error)) {
	if h == nil {
		panic("invalid nil handler")
	}
	g.onClose = func(c *Conn, err error) {
		g.Async(func() {
			defer g.wgConn.Done()
			h(c, err)
		})
	}
}

// OnRead registers callback for reading event.
func (g *Engine) OnRead(h func(c *Conn)) {
	g.onRead = h
}

// OnData registers callback for data.
func (g *Engine) OnData(h func(c *Conn, data []byte)) {
	if h == nil {
		panic("invalid nil handler")
	}
	g.onData = h
}

// OnWrittenSize registers callback for written size.
// If len(b) is bigger than 0, it represents that it's writing a buffer,
// else it's operating by Sendfile.
func (g *Engine) OnWrittenSize(h func(c *Conn, b []byte, n int)) {
	if h == nil {
		panic("invalid nil handler")
	}
	g.onWrittenSize = h
}

// OnReadBufferAlloc registers callback for memory allocating.
func (g *Engine) OnReadBufferAlloc(h func(c *Conn) []byte) {
	if h == nil {
		panic("invalid nil handler")
	}
	g.onReadBufferAlloc = h
}

// OnReadBufferFree registers callback for memory release.
func (g *Engine) OnReadBufferFree(h func(c *Conn, b []byte)) {
	if h == nil {
		panic("invalid nil handler")
	}
	g.onReadBufferFree = h
}

// OnWriteBufferRelease registers callback for write buffer memory release.
// func (g *Engine) OnWriteBufferRelease(h func(c *Conn, b []byte)) {
// 	if h == nil {
// 		panic("invalid nil handler")
// 	}
// 	g.onWriteBufferFree = h
// }

// BeforeRead registers callback before syscall.Read
// the handler would be called on windows.
func (g *Engine) BeforeRead(h func(c *Conn)) {
	if h == nil {
		panic("invalid nil handler")
	}
	g.beforeRead = h
}

// AfterRead registers callback after syscall.Read
// the handler would be called on *nix.
func (g *Engine) AfterRead(h func(c *Conn)) {
	if h == nil {
		panic("invalid nil handler")
	}
	g.afterRead = h
}

// BeforeWrite registers callback befor syscall.Write and syscall.Writev
// the handler would be called on windows.
func (g *Engine) BeforeWrite(h func(c *Conn)) {
	if h == nil {
		panic("invalid nil handler")
	}
	g.beforeWrite = h
}

// OnStop registers callback before Engine is stopped.
func (g *Engine) OnStop(h func()) {
	if h == nil {
		panic("invalid nil handler")
	}
	g.onStop = h
}

// PollerBuffer returns Poller's buffer by Conn, can be used on linux/bsd.
func (g *Engine) PollerBuffer(c *Conn) []byte {
	return c.p.ReadBuffer
}

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
	g.OnReadBufferAlloc(g.PollerBuffer)
	g.OnReadBufferFree(func(c *Conn, buffer []byte) {})
	// g.OnWriteBufferRelease(func(c *Conn, buffer []byte) {})
	g.BeforeRead(func(c *Conn) {})
	g.AfterRead(func(c *Conn) {})
	g.BeforeWrite(func(c *Conn) {})
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

func (g *Engine) borrow(c *Conn) []byte {
	return g.onReadBufferAlloc(c)
}

func (g *Engine) payback(c *Conn, buffer []byte) {
	g.onReadBufferFree(c, buffer)
}
