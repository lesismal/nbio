package nbio

import (
	"log"
	"sync"
	"sync/atomic"
	"syscall"
)

type empty struct{}

type poller struct {
	mux sync.Mutex

	g *Gopher

	idx int // index
	pfd int // epoll fd

	currLoad int64

	buffer []byte // read buffer

	pollType string
	listener bool // is listener
	shutdown bool
}

// Online return poller's total online
func (p *poller) Online() int64 {
	return atomic.LoadInt64(&p.currLoad)
}

func (p *poller) increase() {
	atomic.AddInt64(&p.currLoad, 1)
}

func (p *poller) decrease() {
	atomic.AddInt64(&p.currLoad, -1)
}

func (p *poller) accept(lfd int) error {
	fd, saddr, err := syscall.Accept(lfd)
	if err != nil {
		return err
	}

	if !p.g.acceptable(int(fd)) {
		syscallClose(fd)
		return nil
	}

	err = syscall.SetNonblock(fd, true)
	if err != nil {
		syscallClose(fd)
		return nil
	}

	laddr, err := syscall.Getsockname(fd)
	if err != nil {
		syscallClose(fd)
		return nil
	}

	c := NewConn(int(fd), sockaddrToAddr(laddr), sockaddrToAddr(saddr))
	o := p.g.pollers[int(fd)%len(p.g.pollers)]
	o.addConn(c)

	return nil
}

func (p *poller) addConn(c *Conn) error {
	err := p.setRead(c.fd)
	if err == nil {
		c.g = p.g
		p.g.conns[c.fd] = c
		p.increase()
		if p.g.onOpen != nil {
			p.g.onOpen(c)
		}
	}

	return err
}

func (p *poller) getConn(fd int) (*Conn, bool) {
	c := p.g.conns[fd]
	return c, c != nil
}

func (p *poller) deleteConn(c *Conn) {
	p.g.conns[c.fd] = nil
	p.decrease()
	p.g.decrease()
	if p.g.onClose != nil {
		p.g.onClose(c, c.closeErr)
	}
}

func (p *poller) stop() {
	log.Printf("poller[%v] stop...", p.idx)
	p.shutdown = true
	syscallClose(p.pfd)
}
