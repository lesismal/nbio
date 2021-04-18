// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// +build darwin netbsd freebsd openbsd dragonfly

package nbio

import (
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/lesismal/nbio/loging"
)

type poller struct {
	mux sync.Mutex

	g *Gopher

	kfd   int
	evtfd int

	index int

	shutdown bool

	isListener bool

	ReadBuffer []byte

	pollType string

	eventList []syscall.Kevent_t
}

func (p *poller) accept(lfd int) error {
	fd, saddr, err := syscall.Accept(lfd)
	if err != nil {
		return err
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

func (p *poller) addConn(c *Conn) {
	c.g = p.g
	p.g.onOpen(c)
	fd := c.fd
	p.addRead(c.fd)
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

func (p *poller) deleteEvent(fd int) {
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

		if ev.Filter&syscall.EVFILT_WRITE != 0 {
			c.flush()
		}
	} else {
		syscall.Close(fd)
		p.deleteEvent(fd)
	}
}

func (p *poller) start() {
	if p.g.lockThread {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
	}
	defer p.g.Done()

	loging.Debug("Poller[%v_%v_%v] start", p.g.Name, p.pollType, p.index)
	defer loging.Debug("Poller[%v_%v_%v] stopped", p.g.Name, p.pollType, p.index)
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
	loging.Debug("Poller[%v_%v_%v] stop...", p.g.Name, p.pollType, p.index)
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
