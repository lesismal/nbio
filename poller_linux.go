// +build linux

package nbio

import (
	"io"
	"log"
	"syscall"
	"time"
)

const (
	_EPOLLET        = 0x80000000
	_EPOLLEXCLUSIVE = (1 << 28)
)

func (p *poller) start() {
	if p.g != nil {
		defer p.g.Done()
	}

	log.Printf("poller[%v] start", p.idx)
	defer log.Printf("poller[%v] stopped", p.idx)

	p.shutdown = false

	events := make([]syscall.EpollEvent, 128)

	if p.twRead != nil {
		p.twRead.start()
	}
	if p.twWrite != nil {
		p.twWrite.start()
	}

	timeout := int(p.g.pollInterval.Milliseconds())
	for !p.shutdown {
		n, err := syscall.EpollWait(p.pfd, events, timeout)
		if err != nil && err != syscall.EINTR {
			return
		}

		for i := 0; i < n; i++ {
			ev := &events[i]
			if int(ev.Fd) == p.lfd {
				err = p.accept()
				if err != nil && err != syscall.EAGAIN {
					return
				}
			} else {
				p.readWrite(ev)
			}
		}
		now := time.Now()
		if p.twRead != nil {
			timeout = p.twRead.check(now)
		}
		if p.twWrite != nil {
			timeout = p.twWrite.check(now)
		}
	}
}

func (p *poller) addRead(fd int) error {
	return syscall.EpollCtl(p.pfd, syscall.EPOLL_CTL_ADD, fd, &syscall.EpollEvent{Fd: int32(fd), Events: syscall.EPOLLRDHUP | syscall.EPOLLIN | _EPOLLET})
}

func (p *poller) addWrite(fd int) error {
	return syscall.EpollCtl(p.pfd, syscall.EPOLL_CTL_MOD, fd, &syscall.EpollEvent{Fd: int32(fd), Events: syscall.EPOLLRDHUP | syscall.EPOLLIN | syscall.EPOLLOUT | _EPOLLET})
}

func (p *poller) readWrite(ev *syscall.EpollEvent) {
	fd := int(ev.Fd)
	if c, ok := p.getConn(fd); ok {
		if ev.Events&(syscall.EPOLLERR|syscall.EPOLLHUP|syscall.EPOLLRDHUP) != 0 {
			c.closeWithError(io.EOF)
			return
		}

		if ev.Events&syscall.EPOLLIN != 0 {
			for {
				buffer := p.g.borrow(c)
				n, err := c.Read(buffer[:])
				if err == nil && n > 0 {
					w := p.g.workers[uint32(fd)%uint32(len(p.g.workers))]
					w.pushEvent(event{c: c, t: _EVENT_DATA, b: buffer[:n]})
				} else {
					p.g.payback(c, buffer)
					if err == syscall.EINTR {
						continue
					}
					if err == syscall.EAGAIN {
						break
					}
					if err != nil {
						c.closeWithError(err)
						return
					}
					break
				}
			}
		}

		if ev.Events&syscall.EPOLLOUT != 0 {
			c.flush()
		}
	}
}

func newPoller(g *Gopher, idx int) (*poller, error) {
	if g == nil {
		panic("invalid gopher")
	}

	fd, err := syscall.EpollCreate1(0)
	if err != nil {
		return nil, err
	}

	if g.lfd > 0 {
		if err := syscall.EpollCtl(fd, syscall.EPOLL_CTL_ADD, g.lfd, &syscall.EpollEvent{Fd: int32(g.lfd), Events: syscall.EPOLLIN | _EPOLLEXCLUSIVE}); err != nil {
			syscallClose(fd)
			return nil, err
		}
	}

	p := &poller{
		g:   g,
		idx: idx,
		lfd: g.lfd,
		pfd: fd,

		conns: map[int]*Conn{},
	}

	if p.g.maxTimeout > 0 {
		p.twRead = newTimerWheel(int(g.maxTimeout/g.pollInterval+1), g.pollInterval, errReadTimeout)
		p.twWrite = newTimerWheel(int(g.maxTimeout/g.pollInterval+1), g.pollInterval, errWriteTimeout)
	}

	return p, nil
}
