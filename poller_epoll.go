// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// +build linux

package nbio

import (
	"io"
	"net"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"github.com/lesismal/nbio/loging"
)

const (
	epoollEventsRead      = syscall.EPOLLPRI | syscall.EPOLLIN
	epoollEventsWrite     = syscall.EPOLLOUT
	epoollEventsReadWrite = syscall.EPOLLPRI | syscall.EPOLLIN | syscall.EPOLLOUT
	epoollEventsError     = syscall.EPOLLERR | syscall.EPOLLHUP | syscall.EPOLLRDHUP
)

type poller struct {
	g *Gopher

	epfd  int
	evtfd int

	index int

	shutdown bool

	isListener bool

	ReadBuffer []byte

	pollType string

	listener net.Listener
}

func (p *poller) accept(lfd int) error {
	fd, saddr, err := syscall.Accept(lfd)
	if err != nil {
		return err
	}

	err = syscall.SetNonblock(fd, true)
	if err != nil {
		syscall.Close(fd)
		return err
	}

	laddr, err := syscall.Getsockname(fd)
	if err != nil {
		syscall.Close(fd)
		return err
	}

	c := newConn(int(fd), sockaddrToAddr(laddr), sockaddrToAddr(saddr))
	o := p.g.pollers[int(fd)%len(p.g.pollers)]
	o.addConn(c)

	return err
}

func (p *poller) addConn(c *Conn) {
	c.g = p.g
	p.g.onOpen(c)
	fd := c.fd
	err := p.addRead(fd)
	if err != nil {
		c.closeWithError(err)
		loging.Error("[%v] add read event failed: %v", c.fd, err)
		return
	}
	p.g.connsUnix[fd] = c
}

func (p *poller) getConn(fd int) *Conn {
	return p.g.connsUnix[fd]
}

func (p *poller) deleteConn(c *Conn) {
	if c == nil {
		return
	}
	fd := c.fd
	if c == p.g.connsUnix[fd] {
		p.g.connsUnix[fd] = nil
		p.deleteEvent(fd)
	}
	p.g.onClose(c, c.closeErr)
}

func (p *poller) start() {
	defer p.g.Done()

	loging.Debug("Poller[%v_%v_%v] start", p.g.Name, p.pollType, p.index)
	defer loging.Debug("Poller[%v_%v_%v] stopped", p.g.Name, p.pollType, p.index)
	defer func() {
		syscall.Close(p.epfd)
		syscall.Close(p.evtfd)
	}()

	if p.isListener {
		p.acceptorLoop()
	} else {
		p.readWriteLoop()
	}
}

func (p *poller) acceptorLoop() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	fd := 0
	msec := -1
	events := make([]syscall.EpollEvent, 1024)

	p.shutdown = false

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
			default:
				err = p.accept(fd)
				if err != nil {
					if err == syscall.EAGAIN {
						loging.Error("Poller[%v_%v_%v] Accept failed: EAGAIN, retrying...", p.g.Name, p.pollType, p.index)
						time.Sleep(time.Second / 20)
					} else {
						loging.Error("Poller[%v_%v_%v] Accept failed: %v, exit...", p.g.Name, p.pollType, p.index, err)
						break
					}
				}
			}
		}
	}
}

func (p *poller) readWriteLoop() {
	if p.g.lockThread {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
	}

	fd := 0
	msec := -1
	events := make([]syscall.EpollEvent, 1024)

	p.shutdown = false

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
			default:
				p.readWrite(&events[i])
			}
		}
	}
}

func (p *poller) stop() {
	loging.Debug("Poller[%v_%v_%v] stop...", p.g.Name, p.pollType, p.index)
	p.shutdown = true
	n := uint64(1)
	syscall.Write(p.evtfd, (*(*[8]byte)(unsafe.Pointer(&n)))[:])
}

func (p *poller) addRead(fd int) error {
	return syscall.EpollCtl(p.epfd, syscall.EPOLL_CTL_ADD, fd, &syscall.EpollEvent{Fd: int32(fd), Events: epoollEventsRead})
}

// func (p *poller) addWrite(fd int) error {
// 	return syscall.EpollCtl(p.epfd, syscall.EPOLL_CTL_ADD, fd, &syscall.EpollEvent{Fd: int32(fd), Events: syscall.EPOLLOUT})
// }

func (p *poller) modWrite(fd int) error {
	return syscall.EpollCtl(p.epfd, syscall.EPOLL_CTL_MOD, fd, &syscall.EpollEvent{Fd: int32(fd), Events: epoollEventsReadWrite})
}

func (p *poller) deleteEvent(fd int) error {
	return syscall.EpollCtl(p.epfd, syscall.EPOLL_CTL_DEL, fd, &syscall.EpollEvent{Fd: int32(fd)})
}

func (p *poller) readWrite(ev *syscall.EpollEvent) {
	fd := int(ev.Fd)
	c := p.getConn(fd)
	if c != nil {
		if ev.Events&epoollEventsError != 0 {
			c.closeWithError(io.EOF)
			return
		}

		if ev.Events&epoollEventsWrite != 0 {
			c.flush()
		}

		if ev.Events&epoollEventsRead != 0 {
			for i := 0; i < 3; i++ {
				buffer := p.g.borrow(c)
				n, err := c.Read(buffer)
				if n > 0 {
					p.g.onData(c, buffer[:n])
				}
				p.g.payback(c, buffer)
				if err == syscall.EINTR {
					continue
				}
				if err == syscall.EAGAIN {
					return
				}
				if err != nil || n == 0 {
					c.closeWithError(err)
				}
				return
			}
		}
	} else {
		syscall.Close(fd)
		p.deleteEvent(fd)
	}
}

func newPoller(g *Gopher, isListener bool, index int) (*poller, error) {
	fd, err := syscall.EpollCreate1(0)
	if err != nil {
		return nil, err
	}

	r0, _, e0 := syscall.Syscall(syscall.SYS_EVENTFD2, 0, syscall.O_NONBLOCK, 0)
	if e0 != 0 {
		syscall.Close(fd)
		return nil, err
	}

	err = syscall.EpollCtl(fd, syscall.EPOLL_CTL_ADD, int(r0),
		&syscall.EpollEvent{Fd: int32(r0),
			Events: syscall.EPOLLIN,
		},
	)
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
					syscall.Close(int(r0))
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
		p.pollType = "LISTENER"
	} else {
		p.pollType = "POLLER"
	}

	return p, nil
}
