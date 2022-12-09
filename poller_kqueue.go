// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build darwin || netbsd || freebsd || openbsd || dragonfly
// +build darwin netbsd freebsd openbsd dragonfly

package nbio

import (
	"net"
	"os"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/lesismal/nbio/logging"
)

const (
	// EPOLLLT .
	EPOLLLT = 0

	// EPOLLET .
	EPOLLET = 1
)

const (
	// for build
	IPPROTO_TCP   = 0
	TCP_KEEPINTVL = 0
	TCP_KEEPIDLE  = 0
)

type poller struct {
	mux sync.Mutex

	g *Engine

	kfd   int
	evtfd int

	index int

	shutdown bool

	listener     net.Listener
	isListener   bool
	unixSockAddr string

	ReadBuffer []byte

	pollType string

	eventList []syscall.Kevent_t
}

func (p *poller) addConn(c *Conn) {
	c.p = p
	if c.typ != ConnTypeUDPServer {
		p.g.onOpen(c)
	}
	fd := c.fd
	p.g.connsUnix[fd] = c
	p.addRead(c.fd)
}

func (p *poller) getConn(fd int) *Conn {
	return p.g.connsUnix[fd]
}

func (p *poller) deleteConn(c *Conn) {
	if c == nil {
		return
	}
	fd := c.fd

	if c.typ != ConnTypeUDPClientFromRead {
		if c == p.g.connsUnix[fd] {
			p.g.connsUnix[fd] = nil
		}
		p.deleteEvent(fd)
	}

	if c.typ != ConnTypeUDPServer {
		p.g.onClose(c, c.closeErr)
	}
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
	p.eventList = append(p.eventList, syscall.Kevent_t{Ident: uint64(fd), Flags: syscall.EV_DELETE, Filter: syscall.EVFILT_READ})
	p.mux.Unlock()
	p.trigger()
}

func (p *poller) readWrite(ev *syscall.Kevent_t) {
	if ev.Flags&syscall.EV_DELETE > 0 {
		return
	}
	fd := int(ev.Ident)
	c := p.getConn(fd)
	if c != nil {
		if ev.Filter&syscall.EVFILT_READ == syscall.EVFILT_READ {
			if p.g.onRead == nil {
				for {
					buffer := p.g.borrow(c)
					rc, n, err := c.ReadAndGetConn(buffer)
					if n > 0 {
						p.g.onData(rc, buffer[:n])
					}
					p.g.payback(c, buffer)
					if err == syscall.EINTR {
						continue
					}
					if err == syscall.EAGAIN {
						return
					}
					if (err != nil || n == 0) && ev.Flags&syscall.EV_DELETE == 0 {
						c.closeWithError(err)
					}
					if n < len(buffer) {
						break
					}
				}
			} else {
				p.g.onRead(c)
			}
		}

		if ev.Filter&syscall.EVFILT_WRITE == syscall.EVFILT_WRITE {
			c.flush()
		}
	} else {
		syscall.Close(fd)
		// p.deleteEvent(fd)
	}
}

func (p *poller) start() {
	if p.g.lockPoller {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
	}
	defer p.g.Done()

	logging.Debug("NBIO[%v][%v_%v] start", p.g.Name, p.pollType, p.index)
	defer logging.Debug("NBIO[%v][%v_%v] stopped", p.g.Name, p.pollType, p.index)

	if p.isListener {
		p.acceptorLoop()
	} else {
		defer syscall.Close(p.kfd)
		p.readWriteLoop()
	}
}

func (p *poller) acceptorLoop() {
	if p.g.lockListener {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
	}

	p.shutdown = false
	for !p.shutdown {
		conn, err := p.listener.Accept()
		if err == nil {
			c, err := NBConn(conn)
			if err != nil {
				conn.Close()
				continue
			}
			p.g.pollers[c.Hash()%len(p.g.pollers)].addConn(c)
		} else {
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				logging.Error("NBIO[%v][%v_%v] Accept failed: temporary error, retrying...", p.g.Name, p.pollType, p.index)
				time.Sleep(time.Second / 20)
			} else {
				if !p.shutdown {
					logging.Error("NBIO[%v][%v_%v] Accept failed: %v, exit...", p.g.Name, p.pollType, p.index, err)
				}
				break
			}
		}
	}
}

func (p *poller) readWriteLoop() {
	if p.g.lockPoller {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
	}

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
	logging.Debug("NBIO[%v][%v_%v] stop...", p.g.Name, p.pollType, p.index)
	p.shutdown = true
	if p.listener != nil {
		p.listener.Close()
		if p.unixSockAddr != "" {
			os.Remove(p.unixSockAddr)
		}
	}
	p.trigger()
}

func newPoller(g *Engine, isListener bool, index int) (*poller, error) {
	if isListener {
		if len(g.addrs) == 0 {
			panic("invalid listener num")
		}

		addr := g.addrs[index%len(g.addrs)]
		ln, err := g.listen(g.network, addr)
		if err != nil {
			return nil, err
		}

		p := &poller{
			g:          g,
			index:      index,
			listener:   ln,
			isListener: isListener,
			pollType:   "LISTENER",
		}
		if g.network == "unix" {
			p.unixSockAddr = addr
		}

		return p, nil
	}

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

	p := &poller{
		g:          g,
		kfd:        fd,
		index:      index,
		isListener: isListener,
		pollType:   "POLLER",
	}

	return p, nil
}
