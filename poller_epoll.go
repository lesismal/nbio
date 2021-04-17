// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// +build linux

package nbio

import (
	"io"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/lesismal/nbio/loging"
)

const stopFd int = 1

type poller struct {
	g *Gopher

	epfd  int
	evtfd int

	index int

	currLoad int64

	shutdown bool

	isListener bool

	ReadBuffer []byte

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

func (p *poller) accept(fd int, saddr syscall.Sockaddr) {
	if !p.acceptable(fd) {
		syscall.Close(fd)
		return
	}

	err := syscall.SetNonblock(fd, true)
	if err != nil {
		syscall.Close(fd)
		return
	}

	laddr, err := syscall.Getsockname(fd)
	if err != nil {
		syscall.Close(fd)
		return
	}

	c := newConn(int(fd), sockaddrToAddr(laddr), sockaddrToAddr(saddr))
	o := p.g.pollers[int(fd)%len(p.g.pollers)]
	o.addConn(c)

	return
}

func (p *poller) acceptable(fd int) bool {
	if fd < 0 {
		return false
	}

	if atomic.AddInt64(&p.g.currLoad, 1) > p.g.maxLoad {
		atomic.AddInt64(&p.g.currLoad, -1)
		return false
	}

	if fd >= len(p.g.connsUnix) {
		atomic.AddInt64(&p.g.currLoad, -1)
		return false
	}

	return true
}

func (p *poller) addConn(c *Conn) {
	c.g = p.g

	p.g.onOpen(c)

	fd := c.fd
	p.g.connsUnix[fd] = c
	p.addRead(fd)

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

	type fdEvent struct {
		fd    int
		saddr syscall.Sockaddr
	}

	chEvent := make(chan fdEvent, 1024*8)
	defer close(chEvent)

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		for evt := range chEvent {
			p.accept(evt.fd, evt.saddr)
		}
	}()

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
				// (nfd int, sa Sockaddr, err error)
				fd, saddr, err := syscall.Accept(fd)
				if err == nil {
					chEvent <- fdEvent{fd, saddr}
				} else if err == syscall.EAGAIN {
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
	return syscall.EpollCtl(p.epfd, syscall.EPOLL_CTL_ADD, fd, &syscall.EpollEvent{Fd: int32(fd), Events: syscall.EPOLLIN | syscall.EPOLLPRI})
}

func (p *poller) addWrite(fd int) error {
	return syscall.EpollCtl(p.epfd, syscall.EPOLL_CTL_ADD, fd, &syscall.EpollEvent{Fd: int32(fd), Events: syscall.EPOLLOUT})
}

func (p *poller) modWrite(fd int) error {
	return syscall.EpollCtl(p.epfd, syscall.EPOLL_CTL_MOD, fd, &syscall.EpollEvent{Fd: int32(fd), Events: syscall.EPOLLIN | syscall.EPOLLPRI | syscall.EPOLLOUT})
}

func (p *poller) deleteEvent(fd int) error {
	return syscall.EpollCtl(p.epfd, syscall.EPOLL_CTL_DEL, fd, &syscall.EpollEvent{Fd: int32(fd)})
}

func (p *poller) readWrite(ev *syscall.EpollEvent) {
	fd := int(ev.Fd)
	c := p.getConn(fd)
	if c != nil {
		if ev.Events&(syscall.EPOLLERR|syscall.EPOLLHUP|syscall.EPOLLRDHUP) != 0 {
			c.closeWithError(io.EOF)
			return
		}

		if ev.Events&syscall.EPOLLOUT != 0 {
			c.flush()
		}

		if ev.Events&syscall.EPOLLIN != 0 {
			buffer := p.g.borrow(c)
			b, err := p.g.onRead(c, buffer)
			if err == nil {
				p.g.onData(c, b)
			}
			p.g.payback(c, buffer)
			if err != nil && err != syscall.EINTR && err != syscall.EAGAIN {
				c.closeWithError(err)
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
