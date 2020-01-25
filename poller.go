package nbio

import (
	"log"
	"sync"
	"syscall"
)

type empty struct{}

type poller struct {
	mux sync.Mutex

	g *Gopher

	idx int // index
	lfd int // listen fd
	pfd int // epoll fd

	twRead  *timerWheel
	twWrite *timerWheel

	conns map[int]*Conn

	shutdown bool
}

func (p *poller) accept() error {
	fd, saddr, err := syscall.Accept(p.lfd)
	if err != nil {
		return err
	}

	if !p.g.acceptable() {
		syscallClose(fd)
		return nil
	}

	err = syscall.SetNonblock(fd, true)
	if err != nil {
		syscallClose(fd)
		return nil
	}

	c := NewConn(p.g, int(fd), sockaddrToAddr(saddr))
	o := p.g.pollers[int(fd)%len(p.g.pollers)]
	o.addConn(c)

	return nil
}

func (p *poller) addConn(c *Conn) error {
	err := p.addRead(c.fd)
	if err == nil {
		p.mux.Lock()
		p.conns[c.fd] = c
		p.mux.Unlock()

		p.g.workers[uint32(c.Hash())%p.g.workerNum].pushEvent(event{c: c, t: _EVENT_OPEN})
	}

	return err
}

func (p *poller) getConn(fd int) (*Conn, bool) {
	p.mux.Lock()
	c, ok := p.conns[fd]
	p.mux.Unlock()
	return c, ok
}

func (p *poller) deleteConn(c *Conn) {
	p.mux.Lock()
	delete(p.conns, c.fd)
	p.mux.Unlock()
}

func (p *poller) stop() {
	log.Printf("poller[%v] stop...", p.idx)
	p.shutdown = true
	syscallClose(p.pfd)
	p.mux.Lock()
	tmp := p.conns
	p.conns = map[int]*Conn{}
	p.mux.Unlock()

	for _, c := range tmp {
		c.Close()
	}
}
