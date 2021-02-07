package nbio

import (
	"fmt"
	"log"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
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
	mux sync.Mutex

	network            string
	addrs              []string
	listenerNum        uint32
	pollerNum          uint32
	readBufferSize     uint32
	maxWriteBufferSize uint32

	lfds     []int
	currLoad int64
	maxLoad  int64

	conns      map[*Conn]struct{}
	connsLinux []*Conn

	listeners []*poller
	pollers   []*poller

	onOpen     func(c *Conn)
	onClose    func(c *Conn, err error)
	onData     func(c *Conn, data []byte)
	onRead     func(c *Conn, b []byte) (int, error)
	onMemAlloc func(c *Conn) []byte
	onMemFree  func(c *Conn, buffer []byte)
}

// Start init and start pollers
func (g *Gopher) Start() error {
	var err error

	g.lfds = []int{}

	if runtime.GOOS == "linux" {
		for _, addr := range g.addrs {
			fd, err := listen(g.network, addr, g.maxLoad)
			if err != nil {
				return err
			}

			g.lfds = append(g.lfds, fd)
		}

		for i := uint32(0); i < g.listenerNum; i++ {
			g.listeners[i], err = newPoller(g, true, int(i))
			if err != nil {
				for j := 0; j < int(i); j++ {
					if runtime.GOOS == "linux" {
						syscallClose(g.lfds[j])
					}
					g.listeners[j].stop()
				}
				return err
			}
		}
	} else {
		g.listeners = make([]*poller, len(g.addrs))
		for i := range g.addrs {
			g.listeners[i], err = newPoller(g, true, int(i))
			if err != nil {
				for j := 0; j < i; j++ {
					g.listeners[j].stop()
				}
			}
		}
	}

	for i := uint32(0); i < g.pollerNum; i++ {
		g.pollers[i], err = newPoller(g, false, int(i))
		if err != nil {
			if runtime.GOOS == "linux" {
				for j := 0; j < int(len(g.lfds)); j++ {
					syscallClose(g.lfds[j])
					g.listeners[j].stop()
				}
			} else {
				for j := 0; j < len(g.addrs); j++ {
					g.listeners[j].stop()
				}
			}

			for j := 0; j < int(i); j++ {
				g.pollers[j].stop()
			}
			return err
		}
	}

	for i := uint32(0); i < g.pollerNum; i++ {
		g.Add(1)
		if runtime.GOOS == "linux" {
			g.pollers[i].readBuffer = make([]byte, g.readBufferSize)
		}
		go g.pollers[i].start()
	}
	for _, l := range g.listeners {
		go l.start()
	}

	log.Printf("gopher start listen on: [\"%v\"]", strings.Join(g.addrs, `", "`))

	return nil
}

// Stop pollers
func (g *Gopher) Stop() {
	for i := uint32(0); i < g.listenerNum; i++ {
		g.listeners[i].stop()
	}
	for i := uint32(0); i < g.pollerNum; i++ {
		g.pollers[i].stop()
	}
	for c := range g.conns {
		if c != nil {
			c.Close()
		}
	}
	for _, c := range g.connsLinux {
		if c != nil {
			c.Close()
		}
	}
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

// OnRead registers callback for conn.Read
func (g *Gopher) OnRead(h func(c *Conn, b []byte) (int, error)) {
	if h == nil {
		panic("invalid nil handler")
	}
	g.onRead = h
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

// NewGopher is a factory impl
func NewGopher(conf Config) (*Gopher, error) {
	cpuNum := uint32(runtime.NumCPU())
	if conf.MaxLoad == 0 {
		conf.MaxLoad = DefaultMaxLoad
	}
	if len(conf.Addrs) > 0 && conf.NListener == 0 {
		conf.NListener = 1
	}
	if conf.NPoller == 0 {
		conf.NPoller = cpuNum
	}
	if conf.ReadBufferSize == 0 {
		conf.ReadBufferSize = DefaultReadBufferSize
	}

	g := &Gopher{
		network:            conf.Network,
		addrs:              conf.Addrs,
		maxLoad:            int64(conf.MaxLoad),
		listenerNum:        conf.NListener,
		pollerNum:          conf.NPoller,
		readBufferSize:     conf.ReadBufferSize,
		maxWriteBufferSize: conf.MaxWriteBufferSize,
		listeners:          make([]*poller, conf.NListener),
		pollers:            make([]*poller, conf.NPoller),
		conns:              map[*Conn]struct{}{},
		connsLinux:         make([]*Conn, conf.MaxLoad+1024),
		onOpen:             func(c *Conn) {},
		onClose:            func(c *Conn, err error) {},
		onData:             func(c *Conn, data []byte) {},
	}

	if runtime.GOOS != "linux" {
		g.onMemAlloc = func(c *Conn) []byte {
			if c.readBuffer == nil {
				c.readBuffer = make([]byte, g.readBufferSize)
			}
			return c.readBuffer
		}
	}

	return g, nil
}
