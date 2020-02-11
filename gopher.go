package nbio

import (
	"fmt"
	"log"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
)

const (
	// default max load of 1 gopher
	_DEFAULT_MAX_LOAD uint32 = 65535

	// redundancy
	_DEFAULT_REDUNDANCY_FD_NUM uint32 = 128

	// default read buffer size of 1 Conn
	_DEFAULT_BUFFER_SIZE uint32 = 1024 * 64

	// default max write buffer size of 1 Conn
	_DEFAULT_MAX_WRITE_BUFFER uint32 = 1024 * 64
)

// Config Of Gopher
type Config struct {
	// tcp network
	Network string

	// tcp addrs
	Addrs []string

	// max load
	MaxLoad uint32

	// listener num
	NListener uint32

	// poller num
	NPoller uint32

	// is without memory control
	WithoutMemControl bool

	// read buffer size
	BufferSize uint32

	// max write buffer size
	MaxWriteBuffer uint32
}

// State of Gopher
type State struct {
	Online  int
	Pollers []struct{ Online int }
	Buffer  struct {
		EachSize  int
		Available int
		Capacity  int
	}
}

// String return Gopher's State Info
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

	lfds           []int
	network        string
	addrs          []string
	listenerNum    uint32
	pollerNum      uint32
	memControl     bool
	bufferSize     uint32
	maxWriteBuffer uint32

	conns     []*Conn
	listeners []*poller
	pollers   []*poller

	currLoad int64
	maxLoad  int64

	onOpen  func(c *Conn)
	onClose func(c *Conn, err error)
	onData  func(c *Conn, data []byte)

	onRead func(c *Conn, b []byte) (int, error)

	onMemAlloc func(c *Conn) []byte
	onMemFree  func(c *Conn, buffer []byte)
}

// Start init and start pollers
func (g *Gopher) Start() error {
	var err error

	g.lfds = []int{}
	if g.network != "" && len(g.addrs) > 0 {
		for _, addr := range g.addrs {
			fd, err := listen(g.network, addr)
			if err != nil {
				return err
			}

			syscall.SetNonblock(fd, true)

			g.lfds = append(g.lfds, fd)
		}
	}

	for i := uint32(0); i < g.listenerNum; i++ {
		g.listeners[i], err = newPoller(g, true, int(i))
		if err != nil {
			for j := 0; j < int(i); j++ {
				syscallClose(g.lfds[j])
				g.listeners[j].stop()
			}
			return err
		}
		go g.listeners[i].start()
	}

	for i := uint32(0); i < g.pollerNum; i++ {
		g.pollers[i], err = newPoller(g, false, int(i))
		if err != nil {
			for j := 0; j < int(len(g.lfds)); j++ {
				syscallClose(g.lfds[j])
				g.listeners[j].stop()
			}
			for j := 0; j < int(i); j++ {
				g.pollers[j].stop()
			}
			return err
		}
	}

	for i := uint32(0); i < g.pollerNum; i++ {
		g.Add(1)
		g.pollers[i].buffer = make([]byte, g.bufferSize)
		go g.pollers[i].start()
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
	for _, c := range g.conns {
		if c != nil {
			c.Close()
		}
	}
}

// AddConn add conn to a poller
func (g *Gopher) AddConn(c *Conn) {
	g.increase()
	g.pollers[uint32(c.Hash())%g.pollerNum].addConn(c)
}

// Online return Gopher's total online
func (g *Gopher) Online() int64 {
	return atomic.LoadInt64(&g.currLoad)
}

// OnOpen register callback for new connection
func (g *Gopher) OnOpen(h func(c *Conn)) {
	g.onOpen = h
}

// OnClose register callback for disconnected
func (g *Gopher) OnClose(h func(c *Conn, err error)) {
	g.onClose = h
}

// OnData register callback for data
func (g *Gopher) OnData(h func(c *Conn, data []byte)) {
	g.onData = h
}

// OnRead register callback for conn.Read
func (g *Gopher) OnRead(h func(c *Conn, b []byte) (int, error)) {
	g.onRead = h
}

// OnMemAlloc register callback for memory allocating
func (g *Gopher) OnMemAlloc(h func(c *Conn) []byte) {
	g.onMemAlloc = h
}

// OnMemFree register callback for memory release
func (g *Gopher) OnMemFree(h func(c *Conn, b []byte)) {
	g.onMemFree = h
}

// get memory from gopher
func (g *Gopher) borrow(c *Conn) []byte {
	if g.onMemAlloc != nil {
		return g.onMemAlloc(c)
	}
	return g.pollers[uint32(c.fd)%g.pollerNum].buffer
}

// payback memory to gopher
func (g *Gopher) payback(c *Conn, buffer []byte) {
	if g.onMemFree != nil {
		g.onMemFree(c, buffer)
	}
}

func (g *Gopher) acceptable(fd int) bool {
	if fd < 0 || fd >= len(g.conns) {
		return false
	}
	if atomic.AddInt64(&g.currLoad, 1) > g.maxLoad {
		atomic.AddInt64(&g.currLoad, -1)
		return false
	}

	return true
}

func (g *Gopher) increase() {
	atomic.AddInt64(&g.currLoad, 1)
}

func (g *Gopher) decrease() {
	atomic.AddInt64(&g.currLoad, -1)
}

// State return Gopher's state info
func (g *Gopher) State() *State {
	state := &State{
		Online:  int(g.Online()),
		Pollers: make([]struct{ Online int }, len(g.pollers)),
	}

	for i := 0; i < len(g.pollers); i++ {
		state.Pollers[i].Online = int(g.pollers[i].Online())
	}

	return state
}

// NewGopher is a factory impl
func NewGopher(conf Config) (*Gopher, error) {
	cpuNum := uint32(runtime.NumCPU())
	if conf.MaxLoad == 0 {
		conf.MaxLoad = _DEFAULT_MAX_LOAD
	}

	if len(conf.Addrs) > 0 && conf.NListener == 0 {
		conf.NListener = 1
	}
	if conf.NPoller == 0 {
		conf.NPoller = cpuNum
	}
	if conf.BufferSize == 0 {
		conf.BufferSize = _DEFAULT_BUFFER_SIZE
	}
	if !conf.WithoutMemControl {
		if conf.MaxWriteBuffer == 0 {
			conf.MaxWriteBuffer = _DEFAULT_MAX_WRITE_BUFFER
		}
	}

	g := &Gopher{
		network:        conf.Network,
		addrs:          conf.Addrs,
		maxLoad:        int64(conf.MaxLoad),
		listenerNum:    conf.NListener,
		pollerNum:      conf.NPoller,
		memControl:     !conf.WithoutMemControl,
		bufferSize:     conf.BufferSize,
		maxWriteBuffer: conf.MaxWriteBuffer,
		listeners:      make([]*poller, conf.NListener),
		pollers:        make([]*poller, conf.NPoller),
		conns:          make([]*Conn, conf.MaxLoad+_DEFAULT_REDUNDANCY_FD_NUM),
	}

	return g, nil
}
