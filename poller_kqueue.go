// +build darwin netbsd freebsd openbsd dragonfly

package nbio

import (
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/lesismal/nbio/log"
)

type poller struct {
	mux sync.Mutex

	g *Gopher

	kfd   int
	evtfd int

	index int

	currLoad int64

	shutdown bool

	isListener bool

	ReadBuffer []byte

	pollType string

	eventList []syscall.Kevent_t
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

func (p *poller) accept(lfd int) error {
	fd, saddr, err := syscall.Accept(lfd)
	if err != nil {
		return err
	}

	if !p.acceptable(fd) {
		syscall.Close(fd)
		return nil
	}

	err = syscall.SetNonblock(fd, true)
	if err != nil {
		syscall.Close(fd)
		return nil
	}

	laddr, err := syscall.Getsockname(fd)
	if err != nil {
		syscall.Close(fd)
		return nil
	}

	c := newConn(int(fd), sockaddrToAddr(laddr), sockaddrToAddr(saddr))
	o := p.g.pollers[int(fd)%len(p.g.pollers)]
	o.addConn(c)

	return nil
}

func (p *poller) acceptable(fd int) bool {
	if fd < 0 {
		return false
	}
	if fd >= len(p.g.connsUnix) {
		p.g.mux.Lock()
		p.g.connsUnix = append(p.g.connsUnix, make([]*Conn, fd-len(p.g.connsUnix)+1024)...)
		p.g.mux.Unlock()
	}
	if atomic.AddInt64(&p.g.currLoad, 1) > p.g.maxLoad {
		atomic.AddInt64(&p.g.currLoad, -1)
		return false
	}

	return true
}

func (p *poller) addConn(c *Conn) {
	c.g = p.g

	p.g.onOpen(c)

	fd := c.fd
	p.addRead(fd)
	p.g.connsUnix[fd] = c
	p.increase()
}

func (p *poller) getConn(fd int) *Conn {
	return p.g.connsUnix[fd]
}

func (p *poller) deleteConn(c *Conn) {
	p.g.connsUnix[c.fd] = nil
	p.decrease()
	p.g.decrease()
	p.g.onClose(c, c.closeErr)
}

func (p *poller) trigger() {
	syscall.Kevent(p.kfd, []syscall.Kevent_t{{Ident: 0, Filter: syscall.EVFILT_USER, Fflags: syscall.NOTE_TRIGGER}}, nil, nil)
}

func (p *poller) addRead(fd int) {
	p.mux.Lock()
	p.eventList = append(p.eventList, syscall.Kevent_t{Ident: uint64(fd), Flags: syscall.EV_ADD, Filter: syscall.EVFILT_READ})
	p.mux.Unlock()
	p.trigger()
}

func (p *poller) modWrite(fd int) {
	p.mux.Lock()
	p.eventList = append(p.eventList, syscall.Kevent_t{Ident: uint64(fd), Flags: syscall.EV_ADD, Filter: syscall.EVFILT_WRITE})
	p.mux.Unlock()
	p.trigger()
}

func (p *poller) deleteWrite(fd int) {
	p.mux.Lock()
	p.eventList = append(p.eventList, syscall.Kevent_t{Ident: uint64(fd), Flags: syscall.EV_DELETE, Filter: syscall.EVFILT_WRITE})
	p.mux.Unlock()
	p.trigger()
}

func (p *poller) readWrite(ev *syscall.Kevent_t) {
	fd := int(ev.Ident)
	c := p.getConn(fd)
	if c != nil {
		if ev.Filter&syscall.EVFILT_READ != 0 {
			buffer := p.g.borrow(c)
			b, err := p.g.onRead(c, buffer)
			if err == nil {
				p.g.onData(c, b)
			} else {
				if err != nil && err != syscall.EINTR && err != syscall.EAGAIN {
					c.closeWithError(err)
					return
				}
			}
			p.g.payback(c, buffer)
		}

		if ev.Filter&syscall.EVFILT_WRITE != 0 {
			c.flush()
		}
	}
}

func (p *poller) start() {
	if p.g.lockThread {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
	}
	defer p.g.Done()

	log.Debug("Poller[%v_%v_%v] start", p.g.Name, p.pollType, p.index)
	defer log.Debug("Poller[%v_%v_%v] stopped", p.g.Name, p.pollType, p.index)
	defer syscall.Close(p.kfd)

	if p.isListener {
		p.acceptorLoop()
	} else {
		p.readWriteLoop()
	}
}

func (p *poller) acceptorLoop() {
	var events = make([]syscall.Kevent_t, 1024)
	var changes []syscall.Kevent_t

	p.shutdown = false
	for !p.shutdown {
		n, err := syscall.Kevent(p.kfd, changes, events, nil)
		if err != nil && err != syscall.EINTR {
			return
		}

		fd := 0
		for i := 0; i < n; i++ {
			fd = int(events[i].Ident)
			switch fd {
			case p.evtfd:
			default:
				err = p.accept(fd)
				if err != nil {
					if err == syscall.EAGAIN {
						log.Error("Poller[%v_%v_%v] Accept failed: EAGAIN, retrying...", p.g.Name, p.pollType, p.index)
						time.Sleep(time.Second / 20)
					} else {
						log.Error("Poller[%v_%v_%v] Accept failed: %v, exit...", p.g.Name, p.pollType, p.index, err)
						break
					}
				}
			}
		}
	}
}

func (p *poller) readWriteLoop() {
	var events = make([]syscall.Kevent_t, 1024)
	var changes []syscall.Kevent_t

	p.shutdown = false
	for !p.shutdown {
		p.mux.Lock()
		changes = p.eventList
		p.eventList = nil
		p.mux.Unlock()
		n, err := syscall.Kevent(p.kfd, changes, events, nil)
		if err != nil && err != syscall.EINTR {
			return
		}

		for i := 0; i < n; i++ {
			switch int(events[i].Ident) {
			case p.evtfd:
			default:
				p.readWrite(&events[i])
			}
		}
	}
}

func (p *poller) stop() {
	log.Debug("Poller[%v_%v_%v] stop...", p.g.Name, p.pollType, p.index)
	p.shutdown = true
	p.trigger()
}

func newPoller(g *Gopher, isListener bool, index int) (*poller, error) {
	fd, err := syscall.Kqueue()
	if err != nil {
		return nil, err
	}

	_, err = syscall.Kevent(fd, []syscall.Kevent_t{{
		Ident:  0,
		Filter: syscall.EVFILT_USER,
		Flags:  syscall.EV_ADD | syscall.EV_CLEAR,
	}}, nil, nil)

	if err != nil {
		syscall.Close(fd)
		return nil, err
	}

	if isListener {
		if len(g.lfds) > 0 {
			for _, lfd := range g.lfds {
				_, err := syscall.Kevent(fd, []syscall.Kevent_t{
					{Ident: uint64(lfd), Flags: syscall.EV_ADD, Filter: syscall.EVFILT_READ},
				}, nil, nil)
				if err != nil {
					return nil, err
				}
			}
		} else {
			panic("invalid listener num")
		}
	}

	p := &poller{
		g:          g,
		kfd:        fd,
		index:      index,
		isListener: isListener,
	}

	if isListener {
		p.pollType = "LISTENER"
	} else {
		p.pollType = "POLLER"
	}

	return p, nil
}
