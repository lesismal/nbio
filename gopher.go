// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbio

import (
	"container/heap"
	"net"
	"runtime/debug"
	"sync"
	"time"

	"github.com/lesismal/nbio/loging"
)

const (
	// DefaultReadBufferSize .
	DefaultReadBufferSize = 1024 * 2

	// DefaultMaxWriteBufferSize .
	DefaultMaxWriteBufferSize = 1024 * 1024

	// DefaultMinConnCacheSize .
	DefaultMinConnCacheSize = 1024 * 2
)

var (
	// MaxOpenFiles .
	MaxOpenFiles = 1024 * 1024
)

// Config Of Gopher
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

	// NPoller represents poller goroutine num, it's set to runtime.NumCPU() by default.
	NPoller int

	// NListener represents poller goroutine num, it's set to runtime.NumCPU() by default.
	NListener int

	// Backlog represents backlog arg for syscall.Listen
	Backlog int

	// ReadBufferSize represents buffer size for reading, it's set to 16k by default.
	ReadBufferSize int

	// MinConnCacheSize represents application layer's Conn write cache buffer size when the kernel sendQ is full
	MinConnCacheSize int

	// MaxWriteBufferSize represents max write buffer size for Conn, it's set to 1m by default.
	// if the connection's Send-Q is full and the data cached by nbio is
	// more than MaxWriteBufferSize, the connection would be closed by nbio.
	MaxWriteBufferSize int

	// LockThread represents poller's goroutine to lock thread or not, it's set to false by default.
	LockThread bool
}

// Gopher is a manager of poller
type Gopher struct {
	sync.WaitGroup
	mux  sync.Mutex
	tmux sync.Mutex

	Name string

	network            string
	addrs              []string
	pollerNum          int
	backlogSize        int
	readBufferSize     int
	maxWriteBufferSize int
	minConnCacheSize   int
	lockThread         bool

	lfds []int

	connsStd  map[*Conn]struct{}
	connsUnix []*Conn

	listeners []*poller
	pollers   []*poller

	onOpen            func(c *Conn)
	onClose           func(c *Conn, err error)
	onData            func(c *Conn, data []byte)
	onReadBufferAlloc func(c *Conn) []byte
	onReadBufferFree  func(c *Conn, buffer []byte)
	onWriteBufferFree func(c *Conn, buffer []byte)
	beforeRead        func(c *Conn)
	afterRead         func(c *Conn)
	beforeWrite       func(c *Conn)
	onStop            func()

	timers  timerHeap
	trigger *time.Timer
	chTimer chan struct{}
}

// Stop pollers
func (g *Gopher) Stop() {
	g.onStop()

	g.trigger.Stop()
	close(g.chTimer)

	for _, l := range g.listeners {
		l.stop()
	}
	for i := 0; i < g.pollerNum; i++ {
		g.pollers[i].stop()
	}
	g.mux.Lock()
	conns := g.connsStd
	g.connsStd = map[*Conn]struct{}{}
	connsUnix := g.connsUnix
	g.mux.Unlock()

	for c := range conns {
		if c != nil {
			c.Close()
		}
	}
	for _, c := range connsUnix {
		if c != nil {
			go c.Close()
		}
	}

	g.Wait()
}

// AddConn adds conn to a poller
func (g *Gopher) AddConn(conn net.Conn) (*Conn, error) {
	c, err := NBConn(conn)
	if err != nil {
		return nil, err
	}
	g.pollers[uint32(c.Hash())%uint32(g.pollerNum)].addConn(c)
	return c, nil
}

// OnOpen registers callback for new connection
func (g *Gopher) OnOpen(h func(c *Conn)) {
	if h == nil {
		panic("invalid nil handler")
	}
	g.onOpen = h
}

// OnClose registers callback for disconnected
func (g *Gopher) OnClose(h func(c *Conn, err error)) {
	if h == nil {
		panic("invalid nil handler")
	}
	g.onClose = h
}

// OnData registers callback for data
func (g *Gopher) OnData(h func(c *Conn, data []byte)) {
	if h == nil {
		panic("invalid nil handler")
	}
	g.onData = h
}

// OnReadBufferAlloc registers callback for memory allocating
func (g *Gopher) OnReadBufferAlloc(h func(c *Conn) []byte) {
	if h == nil {
		panic("invalid nil handler")
	}
	g.onReadBufferAlloc = h
}

// OnReadBufferFree registers callback for memory release
func (g *Gopher) OnReadBufferFree(h func(c *Conn, b []byte)) {
	if h == nil {
		panic("invalid nil handler")
	}
	g.onReadBufferFree = h
}

// OnWriteBufferRelease registers callback for write buffer memory release
func (g *Gopher) OnWriteBufferRelease(h func(c *Conn, b []byte)) {
	if h == nil {
		panic("invalid nil handler")
	}
	g.onWriteBufferFree = h
}

// BeforeRead registers callback before syscall.Read
// the handler would be called on windows
func (g *Gopher) BeforeRead(h func(c *Conn)) {
	if h == nil {
		panic("invalid nil handler")
	}
	g.beforeRead = h
}

// AfterRead registers callback after syscall.Read
// the handler would be called on *nix
func (g *Gopher) AfterRead(h func(c *Conn)) {
	if h == nil {
		panic("invalid nil handler")
	}
	g.afterRead = h
}

// BeforeWrite registers callback befor syscall.Write and syscall.Writev
// the handler would be called on windows
func (g *Gopher) BeforeWrite(h func(c *Conn)) {
	if h == nil {
		panic("invalid nil handler")
	}
	g.beforeWrite = h
}

// OnStop registers callback before Gopher is stopped.
func (g *Gopher) OnStop(h func()) {
	if h == nil {
		panic("invalid nil handler")
	}
	g.onStop = h
}

// After used as time.After
func (g *Gopher) After(timeout time.Duration) <-chan time.Time {
	c := make(chan time.Time, 1)
	g.afterFunc(timeout, func() {
		c <- time.Now()
	})
	return c
}

// AfterFunc used as time.AfterFunc
func (g *Gopher) AfterFunc(timeout time.Duration, f func()) *Timer {
	ht := g.afterFunc(timeout, f)
	return &Timer{htimer: ht}
}

func (g *Gopher) afterFunc(timeout time.Duration, f func()) *htimer {
	g.tmux.Lock()
	defer g.tmux.Unlock()

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

func (g *Gopher) removeTimer(it *htimer) {
	g.tmux.Lock()
	defer g.tmux.Unlock()

	index := it.index
	if index < 0 || index >= len(g.timers) {
		return
	}

	if g.timers[index] == it {
		heap.Remove(&g.timers, index)
		if len(g.timers) > 0 {
			if index == 0 {
				g.trigger.Reset(g.timers[0].expire.Sub(time.Now()))
			}
		} else {
			g.trigger.Reset(timeForever)
		}
	}
}

// ResetTimer removes a timer
func (g *Gopher) resetTimer(it *htimer) {
	g.tmux.Lock()
	defer g.tmux.Unlock()

	index := it.index
	if index < 0 || index >= len(g.timers) {
		return
	}

	if g.timers[index] == it {
		heap.Fix(&g.timers, index)
		if index == 0 || it.index == 0 {
			g.trigger.Reset(g.timers[0].expire.Sub(time.Now()))
		}
	}
}

func (g *Gopher) timerLoop() {
	defer g.Done()
	loging.Debug("Gopher[%v] timer start", g.Name)
	defer loging.Debug("Gopher[%v] timer stopped", g.Name)
	for {
		select {
		case <-g.trigger.C:
			for {
				g.tmux.Lock()
				if g.timers.Len() == 0 {
					g.tmux.Unlock()
					break
				}
				now := time.Now()
				it := g.timers[0]
				if now.After(it.expire) {
					heap.Remove(&g.timers, it.index)
					g.tmux.Unlock()
					func() {
						defer func() {
							err := recover()
							if err != nil {
								loging.Error("Gopher[%v] exec timer failed: %v", g.Name, err)
								debug.PrintStack()
							}
						}()
						it.f()
					}()

				} else {
					g.trigger.Reset(it.expire.Sub(now))
					g.tmux.Unlock()
					break
				}
			}
		case <-g.chTimer:
			return
		}
	}
}

// PollerBuffer returns Poller's buffer by Conn, can be used on linux/bsd
func (g *Gopher) PollerBuffer(c *Conn) []byte {
	return g.pollers[uint32(c.Hash())%uint32(g.pollerNum)].ReadBuffer
}

func (g *Gopher) initHandlers() {
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
}

func (g *Gopher) borrow(c *Conn) []byte {
	return g.onReadBufferAlloc(c)
}

func (g *Gopher) payback(c *Conn, buffer []byte) {
	g.onReadBufferFree(c, buffer)
}
