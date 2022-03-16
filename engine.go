// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbio

import (
	"container/heap"
	"context"
	"net"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/lesismal/nbio/logging"
)

const (
	// DefaultReadBufferSize .
	DefaultReadBufferSize = 1024 * 32

	// DefaultMaxWriteBufferSize .
	DefaultMaxWriteBufferSize = 1024 * 1024

	// DefaultMaxConnReadTimesPerEventLoop .
	DefaultMaxConnReadTimesPerEventLoop = 3
)

var (
	// MaxOpenFiles .
	MaxOpenFiles = 1024 * 1024 * 2
)

// Config Of Engine.
type Config struct {
	// Name describes your gopher name for logging, it's set to "NB" by default.
	Name string

	// Network is the listening protocol, used with Addrs toghter.
	// tcp* supported only by now, there's no plan for other protocol such as udp,
	// because it's too easy to write udp server/client.
	Network string

	// Addrs is the listening addr list for a nbio server.
	// if it is empty, no listener created, then the Engine is used for client by default.
	Addrs []string

	// NPoller represents poller goroutine num, it's set to runtime.NumCPU() by default.
	NPoller int

	// ReadBufferSize represents buffer size for reading, it's set to 16k by default.
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
}

// Gopher keeps old type to compatible with new name Engine.
type Gopher = Engine

func NewGopther(conf Config) *Gopher {
	return NewEngine(conf)
}

// Engine is a manager of poller.
type Engine struct {
	sync.WaitGroup

	Name string

	Execute func(f func())

	mux sync.Mutex

	wgConn sync.WaitGroup

	network                      string
	addrs                        []string
	pollerNum                    int
	readBufferSize               int
	maxWriteBufferSize           int
	maxConnReadTimesPerEventLoop int
	epollMod                     uint32
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
	onReadBufferAlloc func(c *Conn) []byte
	onReadBufferFree  func(c *Conn, buffer []byte)
	onWriteBufferFree func(c *Conn, buffer []byte)
	beforeRead        func(c *Conn)
	afterRead         func(c *Conn)
	beforeWrite       func(c *Conn)
	onStop            func()

	callings  []func()
	chCalling chan struct{}
	timers    timerHeap
	trigger   *time.Timer
	chTimer   chan struct{}
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
			g.atOnce(func() {
				cc.Close()
			})
		}
	}
	for _, c := range connsUnix {
		if c != nil {
			cc := c
			g.atOnce(func() {
				cc.Close()
			})
		}
	}

	g.wgConn.Wait()
	time.Sleep(time.Second / 5)

	g.onStop()

	g.trigger.Stop()
	close(g.chTimer)

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
	g.pollers[uint32(c.Hash())%uint32(g.pollerNum)].addConn(c)
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
		// g.atOnce(func() {
		defer g.wgConn.Done()
		h(c, err)
		// })
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
func (g *Engine) OnWriteBufferRelease(h func(c *Conn, b []byte)) {
	if h == nil {
		panic("invalid nil handler")
	}
	g.onWriteBufferFree = h
}

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

// After used as time.After.
func (g *Engine) After(timeout time.Duration) <-chan time.Time {
	c := make(chan time.Time, 1)
	g.afterFunc(timeout, func() {
		c <- time.Now()
	})
	return c
}

// AfterFunc used as time.AfterFunc.
func (g *Engine) AfterFunc(timeout time.Duration, f func()) *Timer {
	ht := g.afterFunc(timeout, f)
	return &Timer{htimer: ht}
}

func (g *Engine) atOnce(f func()) {
	if f != nil {
		g.mux.Lock()
		g.callings = append(g.callings, f)
		g.mux.Unlock()
		select {
		case g.chCalling <- struct{}{}:
		default:
		}
	}
}

func (g *Engine) afterFunc(timeout time.Duration, f func()) *htimer {
	g.mux.Lock()
	defer g.mux.Unlock()

	now := time.Now()
	it := &htimer{
		index:  len(g.timers),
		expire: now.Add(timeout),
		f:      f,
		parent: g,
	}
	heap.Push(&g.timers, it)
	if g.timers[0] == it {
		g.trigger.Reset(timeout)
	}

	return it
}

func (g *Engine) removeTimer(it *htimer) {
	g.mux.Lock()
	defer g.mux.Unlock()

	index := it.index
	if index < 0 || index >= len(g.timers) {
		return
	}

	if g.timers[index] == it {
		heap.Remove(&g.timers, index)
		if len(g.timers) > 0 {
			if index == 0 {
				g.trigger.Reset(time.Until(g.timers[0].expire))

			}
		} else {
			g.trigger.Reset(timeForever)
		}
	}
}

// ResetTimer removes a timer.
func (g *Engine) resetTimer(it *htimer) {
	g.mux.Lock()
	defer g.mux.Unlock()

	index := it.index
	if index < 0 || index >= len(g.timers) {
		return
	}

	if g.timers[index] == it {
		heap.Fix(&g.timers, index)
		if index == 0 || it.index == 0 {
			g.trigger.Reset(time.Until(g.timers[0].expire))
		}
	}
}

func (g *Engine) timerLoop() {
	defer g.Done()
	logging.Debug("NBIO[%v] timer start", g.Name)
	defer logging.Debug("NBIO[%v] timer stopped", g.Name)
	for {
		select {
		case <-g.chCalling:
			for {
				g.mux.Lock()
				if len(g.callings) == 0 {
					g.callings = nil
					g.mux.Unlock()
					break
				}
				f := g.callings[0]
				g.callings = g.callings[1:]
				g.mux.Unlock()
				func() {
					defer func() {
						err := recover()
						if err != nil {
							const size = 64 << 10
							buf := make([]byte, size)
							buf = buf[:runtime.Stack(buf, false)]
							logging.Error("NBIO[%v] exec call failed: %v\n%v\n", g.Name, err, *(*string)(unsafe.Pointer(&buf)))
						}
					}()
					f()
				}()
			}
		case <-g.trigger.C:
			for {
				g.mux.Lock()
				if g.timers.Len() == 0 {
					g.trigger.Reset(timeForever)
					g.mux.Unlock()
					break
				}
				now := time.Now()
				it := g.timers[0]
				if now.After(it.expire) {
					heap.Remove(&g.timers, it.index)
					g.mux.Unlock()
					func() {
						defer func() {
							err := recover()
							if err != nil {
								const size = 64 << 10
								buf := make([]byte, size)
								buf = buf[:runtime.Stack(buf, false)]
								logging.Error("NBIO[%v] exec timer failed: %v\n%v\n", g.Name, err, *(*string)(unsafe.Pointer(&buf)))
							}
						}()
						it.f()
					}()
				} else {
					g.trigger.Reset(it.expire.Sub(now))
					g.mux.Unlock()
					break
				}
			}
		case <-g.chTimer:
			return
		}
	}
}

// PollerBuffer returns Poller's buffer by Conn, can be used on linux/bsd.
func (g *Engine) PollerBuffer(c *Conn) []byte {
	return g.pollers[uint32(c.Hash())%uint32(g.pollerNum)].ReadBuffer
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
	g.OnWriteBufferRelease(func(c *Conn, buffer []byte) {})
	g.BeforeRead(func(c *Conn) {})
	g.AfterRead(func(c *Conn) {})
	g.BeforeWrite(func(c *Conn) {})
	g.OnStop(func() {})

	if g.Execute == nil {
		g.Execute = func(f func()) {
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
