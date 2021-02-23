package nbio

import (
	"container/heap"
	"fmt"
	"net"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lesismal/nbio/log"
)

const (
	// DefaultMaxLoad .
	DefaultMaxLoad uint32 = 1024 * 100

	// DefaultReadBufferSize .
	DefaultReadBufferSize uint32 = 1024 * 16

	// DefaultMaxWriteBufferSize .
	DefaultMaxWriteBufferSize uint32 = 1024 * 1024
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

	// MaxLoad decides the max online num, it's set to 10k by default.
	MaxLoad uint32

	// NListener decides the listener goroutine num on *nix, it's set to 1 by default.
	NListener uint32

	// NPoller decides poller goroutine num, it's set to runtime.NumCPU() by default.
	NPoller uint32

	// ReadBufferSize decides buffer size for reading, it's set to 16k by default.
	ReadBufferSize uint32

	// MaxWriteBufferSize decides max write buffer size for Conn, it's set to 1m by default.
	// if the connection's Send-Q is full and the data cached by nbio is
	// more than MaxWriteBufferSize, the connection would be closed by nbio.
	MaxWriteBufferSize uint32

	// LockThread decides poller's goroutine to lock thread or not, it's set to false by default.
	LockThread bool
}

// State of Gopher
type State struct {
	// Name .
	Name string
	// Online .
	Online int
	// Pollers .
	Pollers []struct{ Online int }
}

// String returns Gopher's State Info
func (state *State) String() string {
	// str := fmt.Sprintf("****************************************\n[%v]:\n", time.Now().Format("2006.01.02 15:04:05"))
	str := fmt.Sprintf("Gopher[%v] Total Online: %v\n", state.Name, state.Online)
	for i := 0; i < len(state.Pollers); i++ {
		str += fmt.Sprintf("  Poller[%v] Online: %v\n", i, state.Pollers[i].Online)
	}
	return str
}

// Gopher is a manager of poller
type Gopher struct {
	sync.WaitGroup
	mux  sync.Mutex
	tmux sync.Mutex

	Name string

	network            string
	addrs              []string
	listenerNum        uint32
	pollerNum          uint32
	readBufferSize     uint32
	maxWriteBufferSize uint32
	lockThread         bool

	lfds     []int
	currLoad int64
	maxLoad  int64

	connsStd  map[*Conn]struct{}
	connsUnix []*Conn

	listeners []*poller
	pollers   []*poller

	onOpen      func(c *Conn)
	onClose     func(c *Conn, err error)
	onRead      func(c *Conn, b []byte) ([]byte, error)
	onData      func(c *Conn, data []byte)
	onMemAlloc  func(c *Conn) []byte
	onMemFree   func(c *Conn, buffer []byte)
	beforeRead  func(c *Conn)
	afterRead   func(c *Conn)
	beforeWrite func(c *Conn)

	timers  timerHeap
	trigger *time.Timer
	chTimer chan struct{}
}

// Stop pollers
func (g *Gopher) Stop() {
	g.trigger.Stop()
	close(g.chTimer)

	for i := uint32(0); i < g.listenerNum; i++ {
		g.listeners[i].stop()
	}
	for i := uint32(0); i < g.pollerNum; i++ {
		g.pollers[i].stop()
	}
	g.mux.Lock()
	conns := g.connsStd
	g.connsStd = map[*Conn]struct{}{}
	connsUnix := g.connsUnix
	g.connsUnix = make([]*Conn, len(connsUnix))
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
	c, err := g.Conn(conn)
	if err != nil {
		return nil, err
	}
	g.increase()
	g.pollers[uint32(c.Hash())%g.pollerNum].addConn(c)
	return c, nil
}

// Online returns Gopher's total online
func (g *Gopher) Online() int64 {
	return atomic.LoadInt64(&g.currLoad)
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

// OnRead registers callback for read
func (g *Gopher) OnRead(h func(c *Conn, b []byte) ([]byte, error)) {
	if h == nil {
		panic("invalid nil handler")
	}
	// log.Info("---- set OnRead: %v", h)
	g.onRead = h
}

// OnData registers callback for data
func (g *Gopher) OnData(h func(c *Conn, data []byte)) {
	if h == nil {
		panic("invalid nil handler")
	}
	g.onData = h
}

// OnMemAlloc registers callback for memory allocating
func (g *Gopher) OnMemAlloc(h func(c *Conn) []byte) {
	if h == nil {
		panic("invalid nil handler")
	}
	g.onMemAlloc = h
}

// OnMemFree registers callback for memory release
func (g *Gopher) OnMemFree(h func(c *Conn, b []byte)) {
	if h == nil {
		panic("invalid nil handler")
	}
	g.onMemFree = h
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

// State returns Gopher's state info
func (g *Gopher) State() *State {
	state := &State{
		Name:    g.Name,
		Online:  int(g.Online()),
		Pollers: make([]struct{ Online int }, len(g.pollers)),
	}

	for i := 0; i < len(g.pollers); i++ {
		state.Pollers[i].Online = int(g.pollers[i].online())
	}

	return state
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
	log.Debug("Gopher[%v] timer start", g.Name)
	defer log.Debug("Gopher[%v] timer stopped", g.Name)
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
					heap.Pop(&g.timers)
					g.tmux.Unlock()
					func() {
						defer func() {
							err := recover()
							if err != nil {
								log.Error("Gopher[%v] exec timer failed: %v", g.Name, err)
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
	return g.pollers[uint32(c.Hash())%g.pollerNum].ReadBuffer
}

func (g *Gopher) initHandlers() {
	g.OnOpen(func(c *Conn) {})
	g.OnClose(func(c *Conn, err error) {})
	g.OnRead(func(c *Conn, b []byte) ([]byte, error) {
		n, err := c.Read(b)
		if err != nil {
			return nil, err
		}
		return b[:n], err
	})
	g.OnData(func(c *Conn, data []byte) {})
	g.OnMemAlloc(g.PollerBuffer)
	g.OnMemFree(func(c *Conn, buffer []byte) {})
	g.BeforeRead(func(c *Conn) {})
	g.AfterRead(func(c *Conn) {})
	g.BeforeWrite(func(c *Conn) {})
}

func (g *Gopher) borrow(c *Conn) []byte {
	return g.onMemAlloc(c)
}

func (g *Gopher) payback(c *Conn, buffer []byte) {
	g.onMemFree(c, buffer)
}

func (g *Gopher) increase() {
	atomic.AddInt64(&g.currLoad, 1)
}

func (g *Gopher) decrease() {
	atomic.AddInt64(&g.currLoad, -1)
}
