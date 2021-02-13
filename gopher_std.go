// +build windows

package nbio

import (
	"container/heap"
	"log"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

// Start init and start pollers
func (g *Gopher) Start() error {
	var err error

	g.lfds = []int{}

	g.listeners = make([]*poller, len(g.addrs))
	for i := range g.addrs {
		g.listeners[i], err = newPoller(g, true, int(i))
		if err != nil {
			for j := 0; j < i; j++ {
				g.listeners[j].stop()
			}
			return err
		}
	}

	for i := uint32(0); i < g.pollerNum; i++ {
		g.pollers[i], err = newPoller(g, false, int(i))
		if err != nil {
			for j := 0; j < len(g.addrs); j++ {
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
		go g.pollers[i].start()
	}
	for _, l := range g.listeners {
		g.Add(1)
		go l.start()
	}

	g.Add(1)
	go func() {
		defer g.Done()
		log.Printf("gopher timer start")
		defer log.Printf("gopher timer stopped")
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
									log.Printf("timer exec failed: %v", err)
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
	}()

	if len(g.addrs) == 0 {
		log.Printf("gopher start")
	} else {
		log.Printf("gopher start listen on: [\"%v\"]", strings.Join(g.addrs, `", "`))
	}
	return nil
}

// NewGopher is a factory impl
func NewGopher(conf Config) *Gopher {
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
		conns:              map[*Conn][]byte{},
		connsLinux:         make([]*Conn, conf.MaxLoad+64),
		onOpen:             func(c *Conn) {},
		onClose:            func(c *Conn, err error) {},
		onData:             func(c *Conn, data []byte) {},
		trigger:            time.NewTimer(timeForever),
		chTimer:            make(chan struct{}),
	}

	g.onMemAlloc = func(c *Conn) []byte {
		g.mux.Lock()
		buf, ok := g.conns[c]
		if !ok {
			buf = make([]byte, g.readBufferSize)
			g.conns[c] = buf
		}
		g.mux.Unlock()
		return buf
	}

	return g
}
