package nbio

import (
	"fmt"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	// default max load of 1 gopher
	_DEFAULT_MAX_LOAD uint32 = 65535

	// default event queue cap of 1 worker
	_DEFAULT_QUEUE_SIZE uint32 = 1024

	// default read buffer size of 1 gopher
	_DEFAULT_BUFFER_SIZE uint32 = 1024

	// default read buffer number of 1 gopher
	_DEFAULT_BUFFER_NUM uint32 = 1024

	// default max write buffer size of 1 Conn
	_DEFAULT_MAX_WRITE_BUFFER uint32 = 1024 * 64

	// default interval of poller tick and timer wheel
	_DEFAULT_POLL_INTERVAL = time.Millisecond * 200

	// default max timeout ms of conn.Read and conn.Write
	_DEFAULT_MAX_TIMEOUT = time.Second * 120
)

// Config Of Gopher
type Config struct {
	// tcp network
	Network string

	// tcp address
	Address string

	// max load
	MaxLoad uint32

	// poller num
	NPoller uint32

	// worker num
	NWorker uint32

	// worker event queue cap
	QueueSize uint32

	// is without memory control
	WithoutMemControl bool

	// read buffer num
	BufferNum uint32

	// read buffer size
	BufferSize uint32

	// interval of poller tick and timer wheel
	PollInterval time.Duration

	// max timeout ms of conn.Read and conn.Write
	MaxTimeout time.Duration

	// max write buffer size
	MaxWriteBuffer uint32
}

// State of Gopher
type State struct {
	Online  int
	Pollers []struct{ Online int }
	Workers []struct{ Online int }
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
	str += fmt.Sprintf("buffer size: %v, available: %v / %v\n", state.Buffer.EachSize, state.Buffer.Available, state.Buffer.Capacity)
	for i := 0; i < len(state.Pollers); i++ {
		str += fmt.Sprintf("  poller[%v] online: %v\n", i, state.Pollers[i].Online)
	}
	for i := 0; i < len(state.Workers); i++ {
		str += fmt.Sprintf("  worker[%v] online: %v\n", i, (state.Workers[i].Online))
	}
	return str
}

// Gopher is a manager of poller and worker
type Gopher struct {
	sync.WaitGroup

	lfd            int
	network        string
	address        string
	pollerNum      uint32
	workerNum      uint32
	queueSize      uint32
	memControl     bool
	bufferNum      uint32
	bufferSize     uint32
	pollInterval   time.Duration
	maxTimeout     time.Duration
	maxWriteBuffer uint32

	localAddr net.Addr

	chDebug   chan empty
	chBuffers chan []byte

	pollers []*poller
	workers []*worker

	currLoad int64
	maxLoad  int64

	onOpen  func(c *Conn)
	onClose func(c *Conn, err error)
	onData  func(c *Conn, data []byte)

	onRead     func(c *Conn, b []byte) (int, error)
	onMemAlloc func(c *Conn) []byte
	onMemFree  func(c *Conn, b []byte)
}

// Start init and start pollers and workers
func (g *Gopher) Start() error {
	var err error

	if g.network != "" && g.address != "" {
		fd, addr, err := listen(g.network, g.address)
		if err != nil {
			return err
		}

		syscall.SetNonblock(fd, true)

		g.lfd = fd
		g.localAddr = addr
	}

	for i := uint32(0); i < g.pollerNum; i++ {
		g.pollers[i], err = newPoller(g, int(i))
		if err != nil {
			for j := 0; j < int(i); j++ {
				g.pollers[j].stop()
			}
			syscallClose(g.lfd)
			return err
		}
	}

	for i := uint32(0); i < g.workerNum; i++ {
		g.workers[i] = newWorker(g, i, g.queueSize)
		g.Add(1)
		go g.workers[i].start()
	}

	for i := uint32(0); i < g.pollerNum; i++ {
		g.Add(1)
		go g.pollers[i].start()
	}

	return nil
}

// Stop pollers and workers
func (g *Gopher) Stop() {
	for i := uint32(0); i < g.pollerNum; i++ {
		g.pollers[i].stop()
	}

	for i := uint32(0); i < g.workerNum; i++ {
		g.workers[i].stop()
	}
}

// AddConn add conn to a poller
func (g *Gopher) AddConn(c *Conn) {
	g.pollers[uint32(c.Hash())%g.pollerNum].addConn(c)
}

// Online return total online of a Gohper
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

func (g *Gopher) acceptable() bool {
	if atomic.AddInt64(&g.currLoad, 1) > g.maxLoad {
		atomic.AddInt64(&g.currLoad, -1)
		return false
	}
	return true
}

func (g *Gopher) decrease() {
	atomic.AddInt64(&g.currLoad, -1)
}

// borrow memory from gopher
func (g *Gopher) borrow(c *Conn) []byte {
	if g.memControl {
		return <-g.chBuffers
	} else {
		if g.onMemAlloc != nil {
			return g.onMemAlloc(c)
		}
	}
	return nil
}

// payback memory to gopher
func (g *Gopher) payback(c *Conn, b []byte) {
	if g.memControl {
		g.chBuffers <- b[:g.bufferSize]
	} else {
		if g.onMemFree != nil {
			g.onMemFree(c, b)
		}
	}
}

// State return Gopher's state info
func (g *Gopher) State() *State {
	state := &State{
		Online: int(g.Online()),
		Buffer: struct {
			EachSize  int
			Available int
			Capacity  int
		}{
			EachSize:  int(g.bufferSize),
			Available: len(g.chBuffers),
			Capacity:  cap(g.chBuffers),
		},
		Pollers: make([]struct{ Online int }, len(g.pollers)),
		Workers: make([]struct{ Online int }, len(g.workers)),
	}

	for i := 0; i < len(g.pollers); i++ {
		state.Pollers[i].Online = len(g.pollers[i].conns)
	}
	for i := 0; i < len(g.workers); i++ {
		state.Workers[i].Online = int(g.workers[i].online)
	}

	return state
}

// NewGopher is a factory impl
func NewGopher(conf Config) (*Gopher, error) {
	cpuNum := uint32(runtime.NumCPU())
	if conf.MaxLoad == 0 {
		conf.MaxLoad = _DEFAULT_MAX_LOAD
	}
	if conf.NPoller == 0 {
		conf.NPoller = cpuNum
	}
	if conf.NWorker == 0 {
		conf.NWorker = cpuNum * 2
	}
	if conf.QueueSize == 0 {
		conf.QueueSize = _DEFAULT_QUEUE_SIZE
	}
	if conf.PollInterval.Milliseconds() == 0 {
		conf.PollInterval = _DEFAULT_POLL_INTERVAL
	}
	if conf.MaxTimeout.Milliseconds() == 0 {
		conf.MaxTimeout = _DEFAULT_MAX_TIMEOUT
	}

	if !conf.WithoutMemControl {
		if conf.BufferNum == 0 {
			conf.BufferNum = _DEFAULT_BUFFER_NUM
		}
		if conf.BufferSize == 0 {
			conf.BufferSize = _DEFAULT_BUFFER_SIZE
		}
		if conf.MaxWriteBuffer == 0 {
			conf.MaxWriteBuffer = _DEFAULT_MAX_WRITE_BUFFER
		}
	}

	g := &Gopher{
		network:        conf.Network,
		address:        conf.Address,
		maxLoad:        int64(conf.MaxLoad),
		pollerNum:      conf.NPoller,
		workerNum:      conf.NWorker,
		queueSize:      conf.QueueSize,
		memControl:     !conf.WithoutMemControl,
		bufferNum:      conf.BufferNum,
		bufferSize:     conf.BufferSize,
		pollInterval:   conf.PollInterval,
		maxTimeout:     conf.MaxTimeout,
		maxWriteBuffer: conf.MaxWriteBuffer,
		pollers:        make([]*poller, conf.NPoller),
		workers:        make([]*worker, conf.NWorker),
		chBuffers:      make(chan []byte, conf.BufferNum),
	}

	for i := uint32(0); i < conf.BufferNum; i++ {
		g.chBuffers <- make([]byte, conf.BufferSize)
	}

	return g, nil
}
