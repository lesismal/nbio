// +build darwin netbsd freebsd openbsd dragonfly

package nbio

import (
	"io"
	"log"
	"sync/atomic"
	"syscall"
)

type poller struct {
	g *Gopher

	epfd  int
	evtfd int

	index int

	currLoad int64

	shutdown bool

	isListener bool

	readBuffer []byte

	pollType string
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
	if fd >= len(p.g.connsLinux) {
		p.g.mux.Lock()
		p.g.connsLinux = append(p.g.connsLinux, make([]*Conn, fd-len(p.g.connsLinux)+1024)...)
		p.g.mux.Unlock()
	}
	if atomic.AddInt64(&p.g.currLoad, 1) > p.g.maxLoad {
		atomic.AddInt64(&p.g.currLoad, -1)
		return false
	}

	return true
}

func (p *poller) addConn(c *Conn) error {
	c.g = p.g

	p.g.onOpen(c)

	fd := c.fd
	err := p.addRead(fd)
	if err == nil {
		p.g.connsLinux[fd] = c
		p.increase()
	}

	return err
}

func (p *poller) getConn(fd int) *Conn {
	return p.g.connsLinux[fd]
}

func (p *poller) deleteConn(c *Conn) {
	p.g.connsLinux[c.fd] = nil
	p.decrease()
	p.g.decrease()
	p.g.onClose(c, c.closeErr)
}

func (p *poller) trigger() {
	syscall.Kevent(p.fd, []syscall.Kevent_t{{
		Ident:  0,
		Filter: syscall.EVFILT_USER,
		Fflags: syscall.NOTE_TRIGGER,
	}}, nil, nil)
}

func (p *poller) addRead(fd int) error {
	_, err := syscall.Kevent(p.fd, []syscall.Kevent_t{
		{Ident: uint64(fd), Flags: syscall.EV_ADD, Filter: syscall.EVFILT_READ},
	}, nil, nil)
	return err
}

// no need
// func (p *poller) addWrite(fd int) error {
// 	_, err := syscall  .Kevent(p.fd, []syscall  .Kevent_t{
// 		{Ident: uint64(fd), Flags: syscall  .EV_ADD, Filter: syscall  .EVFILT_WRITE},
// 	}, nil, nil)
// 	return os.NewSyscallError("kevent add", err)
// }

func (p *poller) modWrite(fd int) error {
	_, err := syscall.Kevent(p.fd, []syscall.Kevent_t{
		{Ident: uint64(fd), Flags: syscall.EV_ADD, Filter: syscall.EVFILT_WRITE},
	}, nil, nil)
	return err
}

func (p *poller) deleteWrite(fd int) error {
	_, err := syscall.Kevent(p.fd, []syscall.Kevent_t{
		{Ident: uint64(fd), Flags: syscall.EV_DELETE, Filter: syscall.EVFILT_WRITE},
	}, nil, nil)
	return err
}

func (p *poller) readWrite(ev *syscall.Kevent_t) {
	fd := int(ev.Fd)
	c := p.getConn(fd)
	if c != nil {
		// EVFilterSock = -0xd
		if ev.Events&(-0xd) != 0 {
			c.closeWithError(io.EOF)
			return
		}

		if ev.Events&syscall.EVFILT_READ != 0 {
			buffer := p.g.borrow(c)
			n, err := c.Read(buffer)
			if err == nil {
				p.g.onData(c, buffer[:n])
			} else {
				if err != nil && err != syscall.EINTR && err != syscall.EAGAIN {
					c.closeWithError(err)
					return
				}
			}
			p.g.payback(c, buffer)
		}

		if ev.Events&syscall.EVFILT_WRITE != 0 {
			c.flush()
		}
	}
}

func (p *poller) stop() {
	log.Printf("poller[%v] stop...", p.index)
	p.shutdown = true
	p.trigger()
}

func (p *poller) start() {
	defer p.g.Done()

	log.Printf("%v[%v] start", p.pollType, p.index)
	defer log.Printf("%v[%v] stopped", p.pollType, p.index)
	defer func() {
		syscall.Close(p.epfd)
		syscall.Close(p.evtfd)
	}()
	p.shutdown = false

	// twout := 0
	fd := 0
	msec := -1 //int(interval.Milliseconds())
	events := make([]syscall.Kevent_t, 1024)
	changes := []syscall.Kevent_t{}
	if p.isListener {
		for !p.shutdown {
			n, err := syscall.Kevent(p.fd, changes, events, nil)
			if err != nil && err != syscall.EINTR {
				return
			}

			if n <= 0 {
				msec = -1
				// runtime.Gosched()
				continue
			}
			msec = 20

			for i := 0; i < n; i++ {
				fd = int(events[i].Fd)
				switch fd {
				case p.evtfd:
				default:
					err = p.accept(fd)
					if err != nil && err != syscall.EAGAIN {
						return
					}
				}
			}
		}
	} else {
		for !p.shutdown {
			n, err := syscall.Kevent(p.fd, changes, events, nil)
			if err != nil && err != syscall.EINTR {
				return
			}

			if n <= 0 {
				msec = -1
				// runtime.Gosched()
				continue
			}
			msec = 20

			for i := 0; i < n; i++ {
				fd = int(events[i].Fd)
				switch fd {
				case p.evtfd:
				default:
					p.readWrite(&events[i])
				}
			}
		}
	}
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
		syscall.Close(int(r0))
		return nil, err
	}

	if isListener {
		if len(g.lfds) > 0 {
			for _, lfd := range g.lfds {
				_, err := syscall.Kevent(fd, []syscall.Kevent_t{
					{Ident: uint64(lfd), Flags: syscall.EV_ADD, Filter: syscall.EVFILT_READ},
				}, nil, nil)
				if err != nil {
					return err
				}
			}
		} else {
			panic("invalid listener num")
		}
	}

	p := &poller{
		g:          g,
		epfd:       fd,
		evtfd:      int(r0),
		index:      index,
		isListener: isListener,
	}

	if isListener {
		p.pollType = "listener"
	} else {
		p.pollType = "poller"
	}

	return p, nil
}
