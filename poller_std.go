// +build windows

package nbio

import (
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type poller struct {
	mux sync.Mutex

	g *Gopher

	index int

	currLoad int64

	readBuffer []byte

	pollType   string
	isListener bool
	listener   net.Listener
	shutdown   bool

	chStop chan struct{}
}

func (p *poller) online() int64 {
	return atomic.LoadInt64(&p.currLoad)
}

func (p *poller) increase() {
	atomic.AddInt64(&p.currLoad, 1)
}

func (p *poller) decrease() {
	atomic.AddInt64(&p.currLoad, -1)
}

func (p *poller) accept() error {
	conn, err := p.listener.Accept()
	if err != nil {
		return err
	}

	if !p.acceptable() {
		conn.Close()
		return nil
	}

	c := newConn(conn)
	o := p.g.pollers[c.Hash()%len(p.g.pollers)]
	o.addConn(c)

	return nil
}

func (p *poller) readConn(c *Conn) {
	for {
		buffer := p.g.borrow(c)
		n, err := c.Read(buffer)
		if err == nil {
			p.g.onData(c, buffer[:n])
		} else {
			if c.closeErr == nil {
				c.closeErr = err
			}
			c.Close()
		}
		p.g.payback(c, buffer)
		if err != nil {
			return
		}
	}
}

func (p *poller) acceptable() bool {
	if atomic.AddInt64(&p.g.currLoad, 1) > p.g.maxLoad {
		atomic.AddInt64(&p.g.currLoad, -1)
		return false
	}

	return true
}

func (p *poller) addConn(c *Conn) error {
	c.g = p.g
	p.g.mux.Lock()
	p.g.conns[c] = make([]byte, p.g.readBufferSize)
	p.g.mux.Unlock()
	p.increase()
	p.g.onOpen(c)
	go p.readConn(c)

	return nil
}

func (p *poller) deleteConn(c *Conn) {
	p.g.mux.Lock()
	delete(p.g.conns, c)
	p.g.mux.Unlock()
	p.decrease()
	p.g.decrease()
	p.g.onClose(c, c.closeErr)
}

func (p *poller) stop() {
	log.Printf("poller[%v] stop...", p.index)
	p.shutdown = true
	if p.isListener {
		p.listener.Close()
	}
	close(p.chStop)
}

func (p *poller) start() {
	defer p.g.Done()

	log.Printf("%v[%v] start", p.pollType, p.index)
	defer log.Printf("%v[%v] stopped", p.pollType, p.index)

	if p.isListener {
		var err error
		p.shutdown = false
		for !p.shutdown {
			err = p.accept()
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Temporary() {
					log.Printf("Accept error: %v; retrying...", err)
					time.Sleep(time.Second / 20)
				} else {
					log.Printf("Accept error: %v", err)
					break
				}
			}

		}
	} else {
		<-p.chStop
	}
}

func newPoller(g *Gopher, isListener bool, index int) (*poller, error) {
	p := &poller{
		g:          g,
		index:      index,
		isListener: isListener,
		chStop:     make(chan struct{}),
	}

	if isListener {
		var err error
		var addr = g.addrs[index%len(g.addrs)]
		p.listener, err = net.Listen(g.network, addr)
		if err != nil {
			return nil, err
		}
		p.pollType = "listener"
	} else {
		p.pollType = "poller"
	}

	return p, nil
}
