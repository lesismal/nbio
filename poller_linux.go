// +build linux

package nbio

import (
	"io"
	"log"
	"math"
	"os"
	"syscall"
)

const (
	_EPOLLET        = 0x80000000
	_EPOLLEXCLUSIVE = (1 << 28)

	_TIME_FOREVER = int(math.MaxInt32)
)

func (p *poller) start() {
	defer p.g.Done()

	log.Printf("%v[%v] start", p.pollType, p.idx)
	defer log.Printf("%v[%v] stopped", p.pollType, p.idx)
	p.shutdown = false

	events := make([]syscall.EpollEvent, 128)

	if p.listener {
		for !p.shutdown {
			n, err := syscall.EpollWait(p.pfd, events, _TIME_FOREVER)
			if err != nil && err != syscall.EINTR {
				return
			}

			for i := 0; i < n; i++ {
				err = p.accept(int(events[i].Fd))
				if err != nil && err != syscall.EAGAIN {
					return
				}
			}
		}
	} else {
		for !p.shutdown {
			n, err := syscall.EpollWait(p.pfd, events, _TIME_FOREVER)
			if err != nil && err != syscall.EINTR {
				return
			}

			for i := 0; i < n; i++ {
				p.readWrite(&events[i])
			}
		}
	}
}

func (p *poller) setRead(fd int) error {
	return syscall.EpollCtl(p.pfd, syscall.EPOLL_CTL_ADD, fd, &syscall.EpollEvent{Fd: int32(fd), Events: syscall.EPOLLRDHUP | syscall.EPOLLIN})
}

func (p *poller) setReadWrite(fd int) error {
	return syscall.EpollCtl(p.pfd, syscall.EPOLL_CTL_MOD, fd, &syscall.EpollEvent{Fd: int32(fd), Events: syscall.EPOLLRDHUP | syscall.EPOLLIN | syscall.EPOLLOUT})
}

func (p *poller) readWrite(ev *syscall.EpollEvent) {
	fd := int(ev.Fd)
	if c, ok := p.getConn(fd); ok {
		if ev.Events&(syscall.EPOLLERR|syscall.EPOLLHUP|syscall.EPOLLRDHUP) != 0 {
			c.closeWithError(io.EOF)
			return
		}
		if ev.Events&syscall.EPOLLIN != 0 {
			buffer := p.g.borrow(c)
			n, err := c.Read(buffer)
			if len(buffer) == 0 {
				os.Exit(0)
			}
			if err == nil && n > 0 && p.g.onData != nil {
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

func newPoller(g *Gopher, listener bool, idx int) (*poller, error) {
	if g == nil {
		panic("invalid gopher")
	}

	fd, err := syscall.EpollCreate1(0)
	if err != nil {
		return nil, err
	}

	if listener {
		if len(g.lfds) > 0 {
			for _, lfd := range g.lfds {
				if err := syscall.EpollCtl(fd, syscall.EPOLL_CTL_ADD, lfd, &syscall.EpollEvent{Fd: int32(lfd), Events: syscall.EPOLLIN | _EPOLLEXCLUSIVE}); err != nil {
					syscallClose(fd)
					return nil, err
				}
			}
		} else {
			panic("invalid listener num")
		}
	}

	p := &poller{
		g:        g,
		idx:      idx,
		pfd:      fd,
		listener: listener,
	}

	if listener {
		p.pollType = "listener"
	} else {
		p.pollType = "poller"
	}

	return p, nil
}
