// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build darwin || netbsd || freebsd || openbsd || dragonfly
// +build darwin netbsd freebsd openbsd dragonfly

package nbio

import (
	"errors"
	"fmt"
	"io"
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

	// EPOLLONESHOT .
	EPOLLONESHOT = 0
)

const (
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

//go:norace
func (p *poller) addConn(c *Conn) error {
	fd := c.fd
	if fd >= len(p.g.connsUnix) {
		err := fmt.Errorf("too many open files, fd[%d] >= MaxOpenFiles[%d]",
			fd,
			len(p.g.connsUnix))
		_ = c.closeWithError(err)
		return err
	}
	c.p = p
	if c.typ != ConnTypeUDPServer {
		p.g.onOpen(c)
	} else {
		p.g.onUDPListen(c)
	}
	p.g.connsUnix[fd] = c
	p.addRead(fd)
	return nil
}

//go:norace
func (p *poller) addDialer(c *Conn) error {
	fd := c.fd
	if fd >= len(p.g.connsUnix) {
		err := fmt.Errorf("too many open files, fd[%d] >= MaxOpenFiles[%d]",
			fd,
			len(p.g.connsUnix),
		)
		_ = c.closeWithError(err)
		return err
	}
	c.p = p
	p.g.connsUnix[fd] = c
	c.isWAdded = true
	p.addReadWrite(fd)
	return nil
}

//go:norace
func (p *poller) getConn(fd int) *Conn {
	return p.g.connsUnix[fd]
}

//go:norace
func (p *poller) deleteConn(c *Conn) {
	if c == nil {
		return
	}
	fd := c.fd

	if c.typ != ConnTypeUDPClientFromRead {
		if c == p.g.connsUnix[fd] {
			p.g.connsUnix[fd] = nil
		}
		// p.deleteEvent(fd)
	}

	if c.typ != ConnTypeUDPServer {
		p.g.onClose(c, c.closeErr)
	}
}

//go:norace
func (p *poller) trigger() error {
	_, err := syscall.Kevent(p.kfd, []syscall.Kevent_t{{Ident: 0, Filter: syscall.EVFILT_USER, Fflags: syscall.NOTE_TRIGGER}}, nil, nil)
	return err
}

//go:norace
func (p *poller) addRead(fd int) {
	p.mux.Lock()
	p.eventList = append(p.eventList, syscall.Kevent_t{Ident: uint64(fd), Flags: syscall.EV_ADD, Filter: syscall.EVFILT_READ})
	// p.eventList = append(p.eventList, syscall.Kevent_t{Ident: uint64(fd), Flags: syscall.EV_ADD, Filter: syscall.EVFILT_WRITE})
	p.mux.Unlock()
	p.trigger()
}

//go:norace
func (p *poller) resetRead(fd int) error {
	p.mux.Lock()
	p.eventList = append(p.eventList, syscall.Kevent_t{Ident: uint64(fd), Flags: syscall.EV_DELETE, Filter: syscall.EVFILT_WRITE})
	p.mux.Unlock()
	return p.trigger()
}

//go:norace
func (p *poller) modWrite(fd int) error {
	p.mux.Lock()
	p.eventList = append(p.eventList, syscall.Kevent_t{Ident: uint64(fd), Flags: syscall.EV_ADD, Filter: syscall.EVFILT_WRITE})
	p.mux.Unlock()
	return p.trigger()
}

//go:norace
func (p *poller) addReadWrite(fd int) {
	p.mux.Lock()
	p.eventList = append(p.eventList, syscall.Kevent_t{Ident: uint64(fd), Flags: syscall.EV_ADD, Filter: syscall.EVFILT_READ})
	p.eventList = append(p.eventList, syscall.Kevent_t{Ident: uint64(fd), Flags: syscall.EV_ADD, Filter: syscall.EVFILT_WRITE})
	p.mux.Unlock()
	p.trigger()
}

// func (p *poller) deleteEvent(fd int) {
// 	p.mux.Lock()
// 	p.eventList = append(p.eventList,
// 		syscall.Kevent_t{Ident: uint64(fd), Flags: syscall.EV_DELETE, Filter: syscall.EVFILT_READ},
// 		syscall.Kevent_t{Ident: uint64(fd), Flags: syscall.EV_DELETE, Filter: syscall.EVFILT_WRITE})
// 	p.mux.Unlock()
// 	p.trigger()
// }

//go:norace
func (p *poller) readWrite(ev *syscall.Kevent_t) {
	if ev.Flags&syscall.EV_DELETE > 0 {
		return
	}
	fd := int(ev.Ident)
	c := p.getConn(fd)
	if c != nil {
		if ev.Filter == syscall.EVFILT_READ {
			if p.g.onRead == nil {
				for {
					pbuf := p.g.borrow(c)
					bufLen := len(*pbuf)
					rc, n, err := c.ReadAndGetConn(pbuf)
					if n > 0 {
						*pbuf = (*pbuf)[:n]
						p.g.onDataPtr(rc, pbuf)
					}
					p.g.payback(c, pbuf)
					if errors.Is(err, syscall.EINTR) {
						continue
					}
					if errors.Is(err, syscall.EAGAIN) {
						return
					}
					if (err != nil || n == 0) && ev.Flags&syscall.EV_DELETE == 0 {
						if err == nil {
							err = io.EOF
						}
						_ = c.closeWithError(err)
					}
					if n < bufLen {
						break
					}
				}
			} else {
				p.g.onRead(c)
			}

			if ev.Flags&syscall.EV_EOF != 0 {
				if c.onConnected == nil {
					_ = c.flush()
				} else {
					c.onConnected(c, nil)
					c.onConnected = nil
					c.resetRead()
				}
			}
		}

		if ev.Filter == syscall.EVFILT_WRITE {
			if c.onConnected == nil {
				_ = c.flush()
			} else {
				c.resetRead()
				c.onConnected(c, nil)
				c.onConnected = nil
			}
		}
	}
}

//go:norace
func (p *poller) start() {
	if p.g.LockPoller {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
	}
	defer p.g.Done()

	logging.Debug("NBIO[%v][%v_%v] start", p.g.Name, p.pollType, p.index)
	defer logging.Debug("NBIO[%v][%v_%v] stopped", p.g.Name, p.pollType, p.index)

	if p.isListener {
		p.acceptorLoop()
	} else {
		defer func() { _ = syscall.Close(p.kfd) }()
		p.readWriteLoop()
	}
}

//go:norace
func (p *poller) acceptorLoop() {
	if p.g.LockListener {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
	}

	p.shutdown = false
	for !p.shutdown {
		conn, err := p.listener.Accept()
		if err == nil {
			var c *Conn
			c, err = NBConn(conn)
			if err != nil {
				_ = conn.Close()
				continue
			}
			_ = p.g.pollers[c.Hash()%len(p.g.pollers)].addConn(c)
		} else {
			var ne net.Error
			if ok := errors.As(err, &ne); ok && ne.Timeout() {
				logging.Error("NBIO[%v][%v_%v] Accept failed: timeout error, retrying...", p.g.Name, p.pollType, p.index)
				time.Sleep(time.Second / 20)
			} else {
				if !p.shutdown {
					logging.Error("NBIO[%v][%v_%v] Accept failed: %v, exit...", p.g.Name, p.pollType, p.index, err)
				}
				if p.g.onAcceptError != nil {
					p.g.onAcceptError(err)
				}
			}
		}
	}
}

//go:norace
func (p *poller) readWriteLoop() {
	if p.g.LockPoller {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
	}

	events := make([]syscall.Kevent_t, 1024)
	var changes []syscall.Kevent_t

	p.shutdown = false
	for !p.shutdown {
		p.mux.Lock()
		changes = p.eventList
		p.eventList = nil
		p.mux.Unlock()
		n, err := syscall.Kevent(p.kfd, changes, events, nil)
		if err != nil && !errors.Is(err, syscall.EINTR) && !errors.Is(err, syscall.EBADF) && !errors.Is(err, syscall.ENOENT) && !errors.Is(err, syscall.EINVAL) {
			logging.Error("NBIO[%v][%v_%v] Kevent failed: %v, exit...", p.g.Name, p.pollType, p.index, err)
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

//go:norace
func (p *poller) stop() {
	logging.Debug("NBIO[%v][%v_%v] stop...", p.g.Name, p.pollType, p.index)
	p.shutdown = true
	if p.listener != nil {
		_ = p.listener.Close()
		if p.unixSockAddr != "" {
			_ = os.Remove(p.unixSockAddr)
		}
	}
	p.trigger()
}

//go:norace
func newPoller(g *Engine, isListener bool, index int) (*poller, error) {
	if isListener {
		if len(g.Addrs) == 0 {
			panic("invalid listener num")
		}

		addr := g.Addrs[index%len(g.Addrs)]
		ln, err := g.Listen(g.Network, addr)
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
		if g.Network == "unix" {
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
		_ = syscall.Close(fd)
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

//go:norace
func (c *Conn) ResetPollerEvent() {
}
