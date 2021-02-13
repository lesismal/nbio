package nbio

import (
	"container/heap"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
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
	// Network .
	Network string

	// Addrs .
	Addrs []string

	// MaxLoad .
	MaxLoad uint32

	// NListener .
	NListener uint32

	// NPoller .
	NPoller uint32

	// ReadBufferSize .
	ReadBufferSize uint32

	// MaxWriteBufferSize .
	MaxWriteBufferSize uint32
}

// State of Gopher
type State struct {
	// Online .
	Online int
	// Pollers .
	Pollers []struct{ Online int }
}

// String returns Gopher's State Info
func (state *State) String() string {
	// str := fmt.Sprintf("****************************************\n[%v]:\n", time.Now().Format("2006.01.02 15:04:05"))
	str := fmt.Sprintf("online: %v\n", state.Online)
	for i := 0; i < len(state.Pollers); i++ {
		str += fmt.Sprintf("  poller[%v] online: %v\n", i, state.Pollers[i].Online)
	}
	return str
}

// Gopher is a manager of poller
type Gopher struct {
	sync.WaitGroup
	mux  sync.Mutex
	tmux sync.Mutex

	network            string
	addrs              []string
	listenerNum        uint32
	pollerNum          uint32
	readBufferSize     uint32
	maxWriteBufferSize uint32

	lfds     []int
	currLoad int64
	maxLoad  int64

	conns      map[*Conn][]byte
	connsLinux []*Conn

	listeners []*poller
	pollers   []*poller

	onOpen     func(c *Conn)
	onClose    func(c *Conn, err error)
	onData     func(c *Conn, data []byte)
	onMemAlloc func(c *Conn) []byte
	onMemFree  func(c *Conn, buffer []byte)

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
	conns := g.conns
	g.conns = map[*Conn][]byte{}
	connsLinux := g.connsLinux
	g.connsLinux = make([]*Conn, len(connsLinux))
	g.mux.Unlock()

	for c := range conns {
		if c != nil {
			c.Close()
		}
	}
	for _, c := range connsLinux {
		if c != nil {
			go c.Close()
		}
	}

	g.Wait()
}

// AddConn adds conn to a poller
func (g *Gopher) AddConn(c *Conn) {
	g.increase()
	g.pollers[uint32(c.Hash())%g.pollerNum].addConn(c)
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

// State returns Gopher's state info
func (g *Gopher) State() *State {
	state := &State{
		Online:  int(g.Online()),
		Pollers: make([]struct{ Online int }, len(g.pollers)),
	}

	for i := 0; i < len(g.pollers); i++ {
		state.Pollers[i].Online = int(g.pollers[i].online())
	}

	return state
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

func (g *Gopher) borrow(c *Conn) []byte {
	if g.onMemAlloc != nil {
		return g.onMemAlloc(c)
	}
	return g.pollers[uint32(c.Hash())%g.pollerNum].readBuffer
}

func (g *Gopher) payback(c *Conn, buffer []byte) {
	if g.onMemFree != nil {
		g.onMemFree(c, buffer)
	}
}

func (g *Gopher) increase() {
	atomic.AddInt64(&g.currLoad, 1)
}

func (g *Gopher) decrease() {
	atomic.AddInt64(&g.currLoad, -1)
}
