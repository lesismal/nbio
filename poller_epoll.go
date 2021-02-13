// +build linux

package nbio

import (
	"io"
	"log"
	"sync/atomic"
	"syscall"
	"unsafe"
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

func (p *poller) stop() {
	log.Printf("poller[%v] stop...", p.index)
	p.shutdown = true
	n := uint64(1)
	syscall.Write(p.evtfd, (*(*[8]byte)(unsafe.Pointer(&n)))[:])
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
	events := make([]syscall.EpollEvent, 1024)
	if p.isListener {
		for !p.shutdown {
			n, err := syscall.EpollWait(p.epfd, events, msec)
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
					p.shutdown = true
					syscall.Read(p.evtfd, make([]byte, 8))
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
			n, err := syscall.EpollWait(p.epfd, events, msec)
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
					p.shutdown = true
					syscall.Read(p.evtfd, make([]byte, 8))
				default:
					p.readWrite(&events[i])
				}
			}
		}
	}
}

func (p *poller) addRead(fd int) error {
	return syscall.EpollCtl(p.epfd, syscall.EPOLL_CTL_ADD, fd, &syscall.EpollEvent{Fd: int32(fd), Events: syscall.EPOLLRDHUP | syscall.EPOLLIN})
}

// no need
// func (p *poller) addWrite(fd int) error {
// 	return syscall.EpollCtl(p.epfd, syscall.EPOLL_CTL_ADD, fd, &syscall.EpollEvent{Fd: int32(fd), Events: syscall.EPOLLRDHUP | syscall.EPOLLOUT})
// }

func (p *poller) modWrite(fd int) error {
	return syscall.EpollCtl(p.epfd, syscall.EPOLL_CTL_MOD, fd, &syscall.EpollEvent{Fd: int32(fd), Events: syscall.EPOLLRDHUP | syscall.EPOLLIN | syscall.EPOLLOUT})
}

func (p *poller) deleteWrite(fd int) error {
	err := syscall.EpollCtl(p.epfd, syscall.EPOLL_CTL_DEL, fd, &syscall.EpollEvent{Fd: int32(fd)})
	if err != nil {
		return err
	}
	return syscall.EpollCtl(p.epfd, syscall.EPOLL_CTL_ADD, fd, &syscall.EpollEvent{Fd: int32(fd), Events: syscall.EPOLLRDHUP | syscall.EPOLLIN})
}

func (p *poller) readWrite(ev *syscall.EpollEvent) {
	fd := int(ev.Fd)
	c := p.getConn(fd)
	if c != nil {
		if ev.Events&(syscall.EPOLLERR|syscall.EPOLLHUP|syscall.EPOLLRDHUP) != 0 {
			c.closeWithError(io.EOF)
			return
		}

		if ev.Events&syscall.EPOLLIN != 0 {
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

		if ev.Events&syscall.EPOLLOUT != 0 {
			c.flush()
		}
	}
}

func newPoller(g *Gopher, isListener bool, index int) (*poller, error) {
	fd, err := syscall.EpollCreate1(0)
	if err != nil {
		return nil, err
	}

	// EFD_NONBLOCK = 0x800
	r0, _, e0 := syscall.Syscall(syscall.SYS_EVENTFD2, 0, 0x800, 0)
	if e0 != 0 {
		syscall.Close(fd)
		return nil, err
	}

	err = syscall.EpollCtl(fd, syscall.EPOLL_CTL_ADD, int(r0), &syscall.EpollEvent{Fd: int32(r0), Events: syscall.EPOLLIN})
	if err != nil {
		syscall.Close(fd)
		syscall.Close(int(r0))
		return nil, err
	}

	if isListener {
		if len(g.lfds) > 0 {
			for _, lfd := range g.lfds {
				// EPOLLEXCLUSIVE := (1 << 28)
				if err := syscall.EpollCtl(fd, syscall.EPOLL_CTL_ADD, lfd, &syscall.EpollEvent{Fd: int32(lfd), Events: syscall.EPOLLIN | (1 << 28)}); err != nil {
					syscall.Close(fd)
					return nil, err
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
