// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build linux
// +build linux

package nbio

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"github.com/lesismal/nbio/logging"
)

const (
	// EPOLLLT .
	EPOLLLT = 0

	// EPOLLET .
	EPOLLET = 0x80000000

	// EPOLLONESHOT .
	EPOLLONESHOT = syscall.EPOLLONESHOT
)

const (
	epollEventsRead  = syscall.EPOLLPRI | syscall.EPOLLIN
	epollEventsWrite = syscall.EPOLLOUT
	epollEventsError = syscall.EPOLLERR | syscall.EPOLLHUP | syscall.EPOLLRDHUP
)

const (
	IPPROTO_TCP   = syscall.IPPROTO_TCP
	TCP_KEEPINTVL = syscall.TCP_KEEPINTVL
	TCP_KEEPIDLE  = syscall.TCP_KEEPIDLE
)

type poller struct {
	g *Engine // parent engine

	epfd  int // epoll fd
	evtfd int // event fd for trigger

	index int // poller index in engine

	pollType string // listener or io poller

	shutdown bool // state

	// whether poller is used for listener.
	isListener bool
	// listener.
	listener net.Listener
	// if poller is used as UnixConn listener,
	// store the addr and remove it when exit.
	unixSockAddr string

	ReadBuffer []byte // default reading buffer
}

// add the connection to poller and handle its io events.
//
//go:norace
func (p *poller) addConn(c *Conn) error {
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
	if c.typ != ConnTypeUDPServer {
		p.g.onOpen(c)
	} else {
		p.g.onUDPListen(c)
	}
	p.g.connsUnix[fd] = c
	err := p.addRead(fd)
	if err != nil {
		p.g.connsUnix[fd] = nil
		_ = c.closeWithError(err)
	}
	return err
}

// add the connection to poller and handle its io events.
//
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
	err := p.addReadWrite(fd)
	if err != nil {
		p.g.connsUnix[fd] = nil
		_ = c.closeWithError(err)
	}
	return err
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
func (p *poller) start() {
	defer p.g.Done()

	logging.Debug("NBIO[%v][%v_%v] start", p.g.Name, p.pollType, p.index)
	defer logging.Debug("NBIO[%v][%v_%v] stopped", p.g.Name, p.pollType, p.index)

	if p.isListener {
		p.acceptorLoop()
	} else {
		defer func() {
			_ = syscall.Close(p.epfd)
			_ = syscall.Close(p.evtfd)
		}()
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
			err = p.g.pollers[c.Hash()%len(p.g.pollers)].addConn(c)
			if err != nil {
				logging.Error("NBIO[%v][%v_%v] addConn [fd: %v] failed: %v",
					p.g.Name,
					p.pollType,
					p.index,
					c.fd,
					err,
				)
			}
		} else {
			var ne net.Error
			if ok := errors.As(err, &ne); ok && ne.Timeout() {
				logging.Error("NBIO[%v][%v_%v] Accept failed: timeout error, retrying...",
					p.g.Name,
					p.pollType,
					p.index,
				)
				time.Sleep(time.Second / 20)
			} else {
				if !p.shutdown {
					logging.Error("NBIO[%v][%v_%v] Accept failed: %v, exit...",
						p.g.Name,
						p.pollType,
						p.index,
						err,
					)
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

	msec := -1
	events := make([]syscall.EpollEvent, 1024)

	if p.g.onRead == nil && p.g.EpollMod == EPOLLET {
		p.g.MaxConnReadTimesPerEventLoop = 1<<31 - 1
	}

	g := p.g
	p.shutdown = false
	isOneshot := g.isOneshot
	asyncReadEnabled := g.AsyncReadInPoller && (g.EpollMod == EPOLLET)
	for !p.shutdown {
		n, err := syscall.EpollWait(p.epfd, events, msec)
		if err != nil && !errors.Is(err, syscall.EINTR) {
			logging.Error("NBIO[%v][%v_%v] EpollWait failed: %v, exit...",
				p.g.Name,
				p.pollType,
				p.index,
				err,
			)
			return
		}

		if n <= 0 {
			continue
		}

		for _, ev := range events[:n] {
			fd := int(ev.Fd)
			switch fd {
			case p.evtfd: // triggered by stop, exit event loop

			default: // for socket connections
				c := p.getConn(fd)
				if c != nil {
					if ev.Events&epollEventsWrite != 0 {
						if c.onConnected == nil {
							_ = c.flush()
						} else {
							c.onConnected(c, nil)
							c.onConnected = nil
							c.resetRead()
						}
					}

					if ev.Events&epollEventsRead != 0 {
						if g.onRead == nil {
							if asyncReadEnabled {
								c.AsyncRead()
							} else {
								for i := 0; i < g.MaxConnReadTimesPerEventLoop; i++ {
									pbuf := g.borrow(c)
									bufLen := len(*pbuf)
									rc, n, err := c.ReadAndGetConn(pbuf)
									if n > 0 {
										*pbuf = (*pbuf)[:n]
										g.onDataPtr(rc, pbuf)
									}
									g.payback(c, pbuf)
									if errors.Is(err, syscall.EINTR) {
										continue
									}
									if errors.Is(err, syscall.EAGAIN) {
										break
									}
									if err != nil {
										_ = c.closeWithError(err)
										break
									}
									if n < bufLen {
										break
									}
								}
								if isOneshot {
									c.ResetPollerEvent()
								}
							}
						} else {
							g.onRead(c)
						}
					}

					if ev.Events&epollEventsError != 0 {
						_ = c.closeWithError(io.EOF)
						continue
					}
				}
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
	} else {
		n := uint64(1)
		_, _ = syscall.Write(p.evtfd, (*(*[8]byte)(unsafe.Pointer(&n)))[:])
	}
}

//go:norace
func (p *poller) addRead(fd int) error {
	return p.setRead(syscall.EPOLL_CTL_ADD, fd)
}

//go:norace
func (p *poller) resetRead(fd int) error {
	return p.setRead(syscall.EPOLL_CTL_MOD, fd)
}

//go:norace
func (p *poller) setRead(op int, fd int) error {
	switch p.g.EpollMod {
	case EPOLLET:
		events := syscall.EPOLLERR |
			syscall.EPOLLHUP |
			syscall.EPOLLRDHUP |
			syscall.EPOLLPRI |
			syscall.EPOLLIN |
			EPOLLET |
			p.g.EPOLLONESHOT
		if p.g.EPOLLONESHOT != EPOLLONESHOT {
			if op == syscall.EPOLL_CTL_ADD {
				return syscall.EpollCtl(p.epfd, op, fd, &syscall.EpollEvent{
					Fd:     int32(fd),
					Events: events | syscall.EPOLLOUT,
				})
			}
			return nil
		}
		return syscall.EpollCtl(p.epfd, op, fd, &syscall.EpollEvent{
			Fd:     int32(fd),
			Events: events,
		})
	default:
		return syscall.EpollCtl(
			p.epfd,
			op,
			fd,
			&syscall.EpollEvent{
				Fd: int32(fd),
				Events: syscall.EPOLLERR |
					syscall.EPOLLHUP |
					syscall.EPOLLRDHUP |
					syscall.EPOLLPRI |
					syscall.EPOLLIN,
			},
		)
	}
}

//go:norace
func (p *poller) modWrite(fd int) error {
	return p.setReadWrite(syscall.EPOLL_CTL_MOD, fd)
}

//go:norace
func (p *poller) addReadWrite(fd int) error {
	return p.setReadWrite(syscall.EPOLL_CTL_ADD, fd)
}

//go:norace
func (p *poller) setReadWrite(op int, fd int) error {
	switch p.g.EpollMod {
	case EPOLLET:
		events := syscall.EPOLLERR |
			syscall.EPOLLHUP |
			syscall.EPOLLRDHUP |
			syscall.EPOLLPRI |
			syscall.EPOLLIN |
			syscall.EPOLLOUT |
			EPOLLET |
			p.g.EPOLLONESHOT
		if p.g.EPOLLONESHOT != EPOLLONESHOT {
			if op == syscall.EPOLL_CTL_ADD {
				return syscall.EpollCtl(p.epfd, op, fd, &syscall.EpollEvent{
					Fd:     int32(fd),
					Events: events,
				})
			}
			return nil
		}
		return syscall.EpollCtl(p.epfd, op, fd, &syscall.EpollEvent{
			Fd:     int32(fd),
			Events: events,
		})
	default:
		return syscall.EpollCtl(
			p.epfd, op, fd,
			&syscall.EpollEvent{
				Fd: int32(fd),
				Events: syscall.EPOLLERR |
					syscall.EPOLLHUP |
					syscall.EPOLLRDHUP |
					syscall.EPOLLPRI |
					syscall.EPOLLIN |
					syscall.EPOLLOUT,
			},
		)
	}
}

// func (p *poller) deleteEvent(fd int) error {
// 	return syscall.EpollCtl(
// 		p.epfd,
// 		syscall.EPOLL_CTL_DEL,
// 		fd,
// 		&syscall.EpollEvent{Fd: int32(fd)},
// 	)
// }

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

	fd, err := syscall.EpollCreate1(0)
	if err != nil {
		return nil, err
	}

	r0, _, e0 := syscall.Syscall(syscall.SYS_EVENTFD2, 0, syscall.O_NONBLOCK, 0)
	if e0 != 0 {
		_ = syscall.Close(fd)
		return nil, e0
	}

	err = syscall.EpollCtl(fd, syscall.EPOLL_CTL_ADD, int(r0),
		&syscall.EpollEvent{Fd: int32(r0),
			Events: syscall.EPOLLIN,
		},
	)
	if err != nil {
		_ = syscall.Close(fd)
		_ = syscall.Close(int(r0))
		return nil, err
	}

	p := &poller{
		g:          g,
		epfd:       fd,
		evtfd:      int(r0),
		index:      index,
		isListener: isListener,
		pollType:   "POLLER",
	}

	return p, nil
}

//go:norace
func (c *Conn) ResetPollerEvent() {
	p := c.p
	g := p.g
	fd := c.fd
	if g.isOneshot && !c.closed {
		if len(c.writeList) == 0 {
			_ = p.resetRead(fd)
		} else {
			_ = p.modWrite(fd)
		}
	}
}
